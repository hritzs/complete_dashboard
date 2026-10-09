package trading

// Close-quantity guard: the last line in front of EVERY order a trade
// sends (submitOrderIntent, and the Full / Partial Exit and manual hedge
// paths that call the broker directly). Whatever path decided the size --
// Full Exit, Partial Exit, the per-trade MTM / ATM-straddle rules, the
// portfolio MTM pass, Square off ALL, hedge-leg and wing closes, a manual
// leg, a Straddle Build exit -- an order can never close more than the
// trade really holds on that instrument:
//
//   - an order AGAINST the trade's open position on that token (a BUY
//     while net short, a SELL while net long) is capped at that position,
//     less any of the trade's same-side orders on the token still in
//     flight (sent in the last minute, not terminal) -- so it can reduce
//     the position to zero, never flip it into a new opposite position;
//   - an exit order (SQF / PSQF) on a token where the trade holds nothing
//     is refused (it would OPEN a position) -- unless the trade has no
//     recorded fill at all yet (fills still arriving: the exit's own
//     stored-quantity fallback applies, logged);
//   - opening / adding orders (builds, a hedge on a new strike, wings
//     bought) are untouched.
//
// Positions come from the trade's own orders and fills (exchange fills,
// else the order's confirmed filled quantity -- the same source the exits
// size from), so a lagging fill never makes the guard stricter than the
// exit itself.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// tokenPosition: the trade's net filled position on one token (+ long,
// - short), every filled contract of the trade, and the remaining quantity
// of its in-flight same-side orders on the token (excluding excludeIntent).
func (s *PostgresBackedStore) tokenPosition(ctx context.Context, tradeUID string, token int64, side, excludeIntent string) (net, tradeFilled, inflight int64, err error) {
	if s == nil || s.db == nil {
		return 0, 0, 0, fmt.Errorf("postgres store is unavailable")
	}
	err = s.db.QueryRowContext(ctx, `
		WITH o AS (
			SELECT o.side, o.quantity, o.status, o.created_at, o.intent_id, c.broker_token,
			       COALESCE((
			           SELECT CASE WHEN x.r > 0 THEN x.r ELSE x.g END
			           FROM (
			               SELECT COALESCE(SUM(f.fill_quantity) FILTER (WHERE NOT (COALESCE(o.broker_name,'') <> '' AND UPPER(f.fill_id) LIKE UPPER(o.broker_name) || ':%')), 0) AS r,
			                      COALESCE(MAX(f.fill_quantity) FILTER (WHERE COALESCE(o.broker_name,'') <> '' AND UPPER(f.fill_id) LIKE UPPER(o.broker_name) || ':%'), 0) AS g
			               FROM fills f WHERE f.order_id = o.id
			           ) x
			           WHERE x.r > 0 OR x.g > 0
			       ), o.filled_qty) AS filled
			FROM orders o
			LEFT JOIN contracts c ON c.id = o.contract_id
			WHERE o.trade_uid = $1
		)
		SELECT
			COALESCE(SUM(CASE WHEN broker_token = $2 AND UPPER(side) = 'BUY' THEN filled
			                  WHEN broker_token = $2 AND UPPER(side) = 'SELL' THEN -filled END), 0),
			COALESCE(SUM(filled), 0),
			COALESCE(SUM(GREATEST(quantity - filled, 0)) FILTER (
				WHERE broker_token = $2 AND UPPER(side) = UPPER($3)
				  AND intent_id IS DISTINCT FROM $4
				  AND UPPER(status) IN ('SUBMITTED', 'ACKED', 'PENDING', 'OPEN', 'NEW', 'PARTIALLY_FILLED')
				  AND created_at > now() - interval '60 seconds'), 0)
		FROM o`, tradeUID, token, side, excludeIntent).Scan(&net, &tradeFilled, &inflight)
	return net, tradeFilled, inflight, err
}

// closeGuardDecision is the pure rule (no I/O): the quantity allowed for an
// order of side on a token where the trade's net position is net, with
// inflight same-side quantity still pending. exit: an SQF / PSQF order.
// Returns the allowed quantity (<= qty, whole lots when lot > 0) and why.
func closeGuardDecision(side string, qty, lot, net, tradeFilled, inflight int64, exit bool) (int64, string) {
	buy := strings.EqualFold(side, "BUY")
	against := (buy && net < 0) || (!buy && net > 0)
	if !against {
		if exit && net == 0 && tradeFilled > 0 {
			return 0, "the trade holds nothing on this instrument -- an exit here would open a new position"
		}
		return qty, "" // opening / adding (or fills not recorded yet)
	}
	open := net
	if open < 0 {
		open = -open
	}
	allowed := open - inflight
	if lot > 0 && allowed >= lot {
		allowed = allowed / lot * lot // whole lots (an odd remainder below a lot stays closable)
	}
	if allowed <= 0 {
		return 0, fmt.Sprintf("open %d already covered by %d in flight -- nothing left to close", open, inflight)
	}
	if qty > allowed {
		return allowed, fmt.Sprintf("capped from %d to %d (open %d, in flight %d)", qty, allowed, open, inflight)
	}
	return qty, ""
}

// closeQtyGuard applies the rule to an order about to be sent for a trade:
// caps intent.Quantity or refuses (error). Unknown position (no DB): the
// order goes as sized, logged.
func (s *Service) closeQtyGuard(ctx context.Context, tradeUID string, intent *OrderIntent) error {
	if tradeUID == "" || intent == nil || intent.Token == 0 || intent.Quantity <= 0 {
		return nil
	}
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	net, tradeFilled, inflight, err := pg.tokenPosition(qctx, tradeUID, intent.Token, intent.Side, intent.IntentID)
	if err != nil {
		log.Printf("[CLOSE-GUARD] ⚠ trade=%s token=%d %s %d: position unavailable (%v) -- sent as sized", tradeUID, intent.Token, intent.Side, intent.Quantity, err)
		return nil
	}
	phase := strings.ToUpper(strings.TrimSpace(intent.Phase))
	exit := phase == "SQF" || phase == "PSQF"
	if exit && net == 0 && tradeFilled == 0 {
		log.Printf("[CLOSE-GUARD] trade=%s token=%d %s %d (%s): no fill recorded for the trade yet -- sent as sized (exit's stored-quantity fallback)", tradeUID, intent.Token, intent.Side, intent.Quantity, phase)
		return nil
	}
	allowed, why := closeGuardDecision(intent.Side, intent.Quantity, intent.LotSize, net, tradeFilled, inflight, exit)
	if allowed <= 0 {
		log.Printf("🚫 [CLOSE-GUARD] ORDER REFUSED, NOT SENT: trade=%s token=%d %s %d (%s) -- %s (net position %+d)", tradeUID, intent.Token, intent.Side, intent.Quantity, phase, why, net)
		return fmt.Errorf("close guard: %s %d on token %d refused -- %s", intent.Side, intent.Quantity, intent.Token, why)
	}
	if allowed < intent.Quantity {
		log.Printf("⚠ [CLOSE-GUARD] trade=%s token=%d %s (%s) %s (net position %+d)", tradeUID, intent.Token, intent.Side, phase, why, net)
		intent.Quantity = allowed
	}
	return nil
}
