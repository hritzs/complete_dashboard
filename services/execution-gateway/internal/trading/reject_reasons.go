package trading

// Broker rejections, complete and readable. Every rejected order of a trade
// is listed (not only the latest) with the broker's full message, and a
// plain-language cause worked out from it for the known RMS checks:
//
//	MTM loss limit      "Calculated MTM Limit -110006.00 exceeds Maximum allowed MTM ... Limit 50000.00"
//	order value limit   "Single order value exceeded Allowed Value: 22500000 Ordered Value: 25070847"
//	price band (DPR)    "DPR check for Order failed ... Price: 0.05 Ordered DPR:[47.05 - 1393.05]"
//	margin              anything mentioning margin
//
// Anything else shows the broker's own sentence.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	rejMTM   = regexp.MustCompile(`Calculated MTM Limit\s+(-?[\d.]+)\s+exceeds Maximum allowed MTM[^|]*?Limit\s+([\d.]+)`)
	rejValue = regexp.MustCompile(`Single order value exceeded Allowed Value:\s*([\d.]+)\s+Ordered Value:\s*([\d.]+)`)
	rejDPR   = regexp.MustCompile(`DPR check[^|]*?Price:\s*([\d.]+)\s+Ordered DPR:\[\s*([\d.]+)\s*-\s*([\d.]+)\s*\]`)
	rejOrder = regexp.MustCompile(`Order No\.\s*\d+\s+`)
)

func rejNum(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }

// rejRupees: 22500000 -> "₹2.25 cr", 110006 -> "₹1,10,006".
func rejRupees(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	out := ""
	switch {
	case v >= 1e7:
		out = fmt.Sprintf("₹%.2f cr", v/1e7)
	default:
		n := strconv.FormatInt(int64(v+0.5), 10)
		if len(n) > 3 { // Indian grouping
			head, tail := n[:len(n)-3], n[len(n)-3:]
			var parts []string
			for len(head) > 2 {
				parts = append([]string{head[len(head)-2:]}, parts...)
				head = head[:len(head)-2]
			}
			if head != "" {
				parts = append([]string{head}, parts...)
			}
			n = strings.Join(parts, ",") + "," + tail
		}
		out = "₹" + n
	}
	if neg {
		return "-" + out
	}
	return out
}

// rejectCause turns the broker's message into one plain sentence and a
// short kind (MTM_LIMIT / ORDER_VALUE / PRICE_BAND / MARGIN / OTHER).
func rejectCause(reason string) (kind, cause string) {
	r := strings.TrimSpace(reason)
	switch {
	case rejMTM.MatchString(r):
		m := rejMTM.FindStringSubmatch(r)
		return "MTM_LIMIT", fmt.Sprintf("Broker MTM loss limit: the account's MTM %s is beyond the %s allowed -- the broker refuses every NEW order (hedges included) until the limit is raised or the MTM recovers",
			rejRupees(rejNum(m[1])), rejRupees(rejNum(m[2])))
	case rejValue.MatchString(r):
		m := rejValue.FindStringSubmatch(r)
		return "ORDER_VALUE", fmt.Sprintf("Order too large for the broker's single-order value limit: %s ordered, %s allowed (counted as (strike + premium) x quantity)",
			rejRupees(rejNum(m[2])), rejRupees(rejNum(m[1])))
	case rejDPR.MatchString(r):
		m := rejDPR.FindStringSubmatch(r)
		return "PRICE_BAND", fmt.Sprintf("Price ₹%.2f is outside the allowed price band ₹%.2f - ₹%.2f", rejNum(m[1]), rejNum(m[2]), rejNum(m[3]))
	case strings.Contains(strings.ToLower(r), "margin"):
		return "MARGIN", "Insufficient margin for this order"
	case r == "":
		return "OTHER", "rejected by the broker (no reason text received)"
	}
	s := r
	if loc := rejOrder.FindStringIndex(s); loc != nil {
		s = s[loc[1]:]
	}
	if i := strings.Index(s, "|"); i > 0 {
		s = s[:i]
	}
	return "OTHER", strings.TrimSpace(s)
}

// allRejectsSQL: every REJECTED order of the trades created in [$1, $2),
// oldest first, with the broker's longest reason text.
const allRejectsSQL = `
SELECT o.trade_uid, o.updated_at, o.side, o.quantity, COALESCE(o.limit_price, 0), COALESCE(o.phase, ''),
       COALESCE(c.option_type, ''), COALESCE(c.strike_price, 0), COALESCE(o.broker_order_id, ''),
       COALESCE((SELECT oe.reason_text FROM order_events oe
                  WHERE oe.order_id = o.id AND oe.status = 'REJECTED'
                    AND COALESCE(oe.reason_text, '') NOT IN ('', 'BROKER_EXECUTION', 'BROKER_RESPONSE')
                  ORDER BY length(oe.reason_text) DESC LIMIT 1), '')
FROM orders o LEFT JOIN contracts c ON c.id = o.contract_id
WHERE o.status = 'REJECTED'
  AND o.trade_uid IN (SELECT trade_uid FROM trades WHERE created_at >= $1 AND created_at < $2)
ORDER BY o.trade_uid, o.updated_at, o.id`

// allRejects returns every rejected order per trade (oldest first).
func (s *PostgresBackedStore) allRejects(ctx context.Context, from, to time.Time) (map[string][]TradeReject, error) {
	rows, err := s.db.QueryContext(ctx, allRejectsSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("query rejects: %w", err)
	}
	defer rows.Close()
	out := map[string][]TradeReject{}
	for rows.Next() {
		var (
			uid string
			r   TradeReject
		)
		if err := rows.Scan(&uid, &r.Time, &r.Side, &r.Qty, &r.LimitPrice, &r.Phase, &r.Leg, &r.Strike, &r.BrokerOrderID, &r.Reason); err != nil {
			return nil, fmt.Errorf("scan reject: %w", err)
		}
		r.Kind, r.Cause = rejectCause(r.Reason)
		if strings.TrimSpace(r.Reason) == "" {
			r.Reason = r.Cause
		}
		out[uid] = append(out[uid], r)
	}
	return out, rows.Err()
}
