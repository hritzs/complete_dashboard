package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TradeSummary is one trade with what actually executed and its realized PnL.
type TradeSummary struct {
	TradeUID    string     `json:"trade_uid"`
	Symbol      string     `json:"symbol"`
	Expiry      string     `json:"expiry"`
	Strike      float64    `json:"strike"`
	Lots        int        `json:"lots"`
	LotSize     int        `json:"lot_size"`
	BrokerName  string     `json:"broker_name"`
	AccountID   string     `json:"account_id"`
	ProductType string     `json:"product_type"`
	Status      string     `json:"status"`
	CloseReason string     `json:"close_reason,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`

	// RealizedPnL is gross of brokerage/charges, from broker-confirmed order
	// fills (average-cost, matched quantity).
	RealizedPnL float64 `json:"realized_pnl"`
	// PnLPerStraddle is RealizedPnL / (Lots x LotSize).
	PnLPerStraddle float64 `json:"pnl_per_straddle"`
	// WingRealizedPnL is the wings' own realized PnL (margin-only legs),
	// shown separately -- never included in RealizedPnL.
	WingRealizedPnL float64 `json:"wing_realized_pnl"`

	// Config: the trade's settings (SL/TP/exit/divisors/wings) and its
	// modification log, so closed trades still show what they ran with.
	Config MonitorConfig `json:"config"`

	CE         *LegPnL          `json:"ce,omitempty"`
	PE         *LegPnL          `json:"pe,omitempty"`
	ExtraLegs  []*LegPnL        `json:"extra_legs,omitempty"` // e.g. an ATM hedge on another strike
	OpenCEQty  int64            `json:"open_ce_qty"`
	OpenPEQty  int64            `json:"open_pe_qty"`
	Executions []OrderExecution `json:"executions"`
}

const executionsSelect = `
	SELECT o.trade_uid, o.id, o.created_at, o.intent_id, COALESCE(o.phase, ''),
	       o.side, o.quantity,
	       -- Filled qty per order with the same rule as perOrderFillsSQL:
	       -- exchange (reconciler) fills when present, else the gateway's
	       -- snapshot, else the order row. orders.filled_qty alone can be
	       -- inflated by an over-counted gateway snapshot.
	       COALESCE((
	           SELECT CASE WHEN x.r > 0 THEN x.r ELSE x.g END
	           FROM (
	               SELECT COALESCE(SUM(f.fill_quantity) FILTER (WHERE NOT (COALESCE(o.broker_name,'') <> '' AND UPPER(f.fill_id) LIKE UPPER(o.broker_name) || ':%')), 0) AS r,
	                      COALESCE(MAX(f.fill_quantity) FILTER (WHERE COALESCE(o.broker_name,'') <> '' AND UPPER(f.fill_id) LIKE UPPER(o.broker_name) || ':%'), 0) AS g
	               FROM fills f WHERE f.order_id = o.id
	           ) x
	           WHERE x.r > 0 OR x.g > 0
	       ), o.filled_qty),
	       COALESCE(NULLIF(o.avg_fill_price, 0), NULLIF(o.average_price, 0),
	                (SELECT f.fill_price FROM fills f WHERE f.order_id = o.id ORDER BY f.id DESC LIMIT 1), 0),
	       o.status, COALESCE(o.broker_order_id, ''),
	       COALESCE(c.option_type, ''), COALESCE(c.strike_price, 0),
	       COALESCE(c.broker_token, 0)
	FROM orders o
	LEFT JOIN contracts c ON c.id = o.contract_id
`

func scanExecutions(rows *sql.Rows) (map[string][]OrderExecution, error) {
	out := map[string][]OrderExecution{}
	for rows.Next() {
		var (
			uid string
			e   OrderExecution
			px  float64
		)
		if err := rows.Scan(&uid, &e.OrderID, &e.Time, &e.intentID, &e.phase, &e.Side, &e.Quantity,
			&e.FilledQty, &px, &e.Status, &e.BrokerOrderID, &e.Leg, &e.Strike, &e.token); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		e.AvgPrice = px
		e.Kind = classifyExecutionKind(e.phase, e.intentID)
		out[uid] = append(out[uid], e)
	}
	return out, rows.Err()
}

// TradeRealizedPnL returns a trade's realized PnL from its orders.
func (s *PostgresBackedStore) TradeRealizedPnL(ctx context.Context, tradeUID string) (float64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("postgres store is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, executionsSelect+` WHERE o.trade_uid = $1 ORDER BY o.created_at, o.id`, tradeUID)
	if err != nil {
		return 0, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()
	byTrade, err := scanExecutions(rows)
	if err != nil {
		return 0, err
	}
	return totalRealizedPnL(computeLegPnL(byTrade[tradeUID])), nil
}

// TradeSummaries returns every trade created in [from, to) with its
// executions and realized PnL, newest first.
func (s *PostgresBackedStore) TradeSummaries(ctx context.Context, from, to time.Time) ([]TradeSummary, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store is unavailable")
	}

	orderRows, err := s.db.QueryContext(ctx, executionsSelect+`
		WHERE o.trade_uid IN (SELECT trade_uid FROM trades WHERE created_at >= $1 AND created_at < $2)
		ORDER BY o.created_at, o.id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query executions: %w", err)
	}
	execs, err := scanExecutions(orderRows)
	orderRows.Close()
	if err != nil {
		return nil, err
	}

	tradeRows, err := s.db.QueryContext(ctx, `
		SELECT trade_uid, COALESCE(user_id,''), COALESCE(broker_name,''), COALESCE(account_id,''),
		       symbol, status, created_at, closed_at, COALESCE(config, '{}'::jsonb)
		FROM trades
		WHERE created_at >= $1 AND created_at < $2
		ORDER BY created_at DESC`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query trades: %w", err)
	}
	defer tradeRows.Close()

	out := []TradeSummary{}
	for tradeRows.Next() {
		var (
			uid, user, broker, account, symbol, status string
			createdAt                                  time.Time
			closedAt                                   sql.NullTime
			raw                                        []byte
		)
		if err := tradeRows.Scan(&uid, &user, &broker, &account, &symbol, &status, &createdAt, &closedAt, &raw); err != nil {
			return nil, fmt.Errorf("scan trade: %w", err)
		}

		var stored StoredTrade
		_ = json.Unmarshal(raw, &stored)

		list := execs[uid]
		legs := computeLegPnL(list)
		ceKey, peKey := legKey("CE", stored.CEToken), legKey("PE", stored.PEToken)
		sum := TradeSummary{
			TradeUID: uid, Symbol: symbol, Expiry: stored.Expiry, Strike: stored.Strike,
			Lots: stored.Lots, LotSize: stored.LotSize,
			BrokerName: broker, AccountID: account, ProductType: stored.ProductType,
			Status: status, CloseReason: closeReasonForStatus(status), CreatedAt: createdAt,
			RealizedPnL: totalRealizedPnL(legs), CE: legs[ceKey], PE: legs[peKey],
			Executions: list,
			Config:     stored.Config,
		}
		sum.PnLPerStraddle = pnlPerStraddle(sum.RealizedPnL, stored.Lots, stored.LotSize)
		sum.WingRealizedPnL = totalRealizedPnL(computeWingLegPnL(list))
		for k, l := range legs {
			if k != ceKey && k != peKey {
				sum.ExtraLegs = append(sum.ExtraLegs, l)
			}
		}
		sort.Slice(sum.ExtraLegs, func(i, j int) bool {
			if sum.ExtraLegs[i].Leg != sum.ExtraLegs[j].Leg {
				return sum.ExtraLegs[i].Leg < sum.ExtraLegs[j].Leg
			}
			return sum.ExtraLegs[i].Strike < sum.ExtraLegs[j].Strike
		})
		if sum.Executions == nil {
			sum.Executions = []OrderExecution{}
		}
		if sum.CE != nil {
			sum.OpenCEQty = sum.CE.NetShortQty
		}
		if sum.PE != nil {
			sum.OpenPEQty = sum.PE.NetShortQty
		}

		// closed_at: the column is only populated for trades closed since it
		// started being written; older rows fall back to the stored JSON, then
		// to the time of the last exit order.
		if closeReasonForStatus(status) != "" {
			switch {
			case closedAt.Valid:
				t := closedAt.Time
				sum.ClosedAt = &t
			case stored.ClosedAt.Year() > 2000:
				t := stored.ClosedAt
				sum.ClosedAt = &t
			default:
				for i := len(list) - 1; i >= 0; i-- {
					if list[i].Kind == "EXIT" && list[i].FilledQty > 0 {
						t := list[i].Time
						sum.ClosedAt = &t
						break
					}
				}
			}
		}
		out = append(out, sum)
	}
	return out, tradeRows.Err()
}

// openShortQuantities is sold minus bought (filled quantity) per option leg,
// never negative. Unlike the PnL it does not need a price: any filled order
// changes the position.
func openShortQuantities(execs []OrderExecution) (ce, pe int64) {
	net := map[string]int64{}
	for _, e := range execs {
		if e.FilledQty <= 0 {
			continue
		}
		switch strings.ToUpper(e.Side) {
		case "SELL":
			net[e.Leg] += e.FilledQty
		case "BUY":
			net[e.Leg] -= e.FilledQty
		}
	}
	return maxInt64(net["CE"], 0), maxInt64(net["PE"], 0)
}

// TradeOpenQuantities returns the short CE and PE quantity a trade really
// has open. Sourced from the fills table (via a JOIN on orders/contracts
// for side and option_type), NOT orders.filled_qty -- confirmed live
// 2026-09-23 that orders.filled_qty (and orders.status) can be corrupted
// by a late, out-of-order, non-terminal Iris push regressing an
// already-terminal order (see the guard added to reconciler's
// ApplyOrderUpdate). fills rows are inserted exactly once per real fill
// (idempotent on (fill_id, order_id), never rewritten afterward), so they
// stay correct regardless of what happens to the order row's own status
// columns later. This function backs the quantity a real square-off order
// gets sized from for PARTIAL/RECONCILIATION_REQUIRED trades -- getting it
// wrong risks leaving a real residual position uncounted.
func (s *PostgresBackedStore) TradeOpenQuantities(ctx context.Context, tradeUID string) (int64, int64, error) {
	if s == nil || s.db == nil {
		return 0, 0, fmt.Errorf("postgres store is unavailable")
	}
	// Restricted to the straddle's OWN CE/PE tokens: this sizes a real
	// exit order placed on tr.CEToken/tr.PEToken, so an ATM hedge's fills
	// (same option_type, different strike) must not be pooled in -- that
	// hedge is closed separately by closeExtraOpenLegs.
	// per_order counts each order's real fill once (see perOrderFillsSQL);
	// summing raw fills rows double-counted every fill.
	rows, err := s.db.QueryContext(ctx, perOrderFillsSQL+`
		SELECT po.side, COALESCE(c.option_type, ''), COALESCE(SUM(po.qty), 0)
		FROM per_order po
		JOIN trades t ON t.id = po.trade_id
		JOIN contracts c ON c.id = po.contract_id
		WHERE t.trade_uid = $1
		  AND c.broker_token IN (
			(t.config->>'ce_token')::bigint,
			(t.config->>'pe_token')::bigint
		  )
		GROUP BY po.side, c.option_type
	`, tradeUID)
	if err != nil {
		return 0, 0, fmt.Errorf("query fills: %w", err)
	}
	defer rows.Close()

	net := map[string]int64{}
	for rows.Next() {
		var side, leg string
		var qty int64
		if err := rows.Scan(&side, &leg, &qty); err != nil {
			return 0, 0, fmt.Errorf("scan fill total: %w", err)
		}
		switch strings.ToUpper(strings.TrimSpace(side)) {
		case "SELL":
			net[leg] += qty
		case "BUY":
			net[leg] -= qty
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return maxInt64(net["CE"], 0), maxInt64(net["PE"], 0), nil
}
