package trading

// Before a full exit sizes from fills, every order of the trade still OPEN
// at the broker (sent, not FILLED / CANCELLED / REJECTED -- a resting hedge
// or build remainder, an order whose cancel failed, one left by a restart)
// is cancelled first and given a moment to settle. The exit then closes
// exactly what the COMPLETED orders hold (fills), and nothing can fill into
// the trade after it is flat. Normally there is none: the hedge and the
// build chase cancel their own leftovers; this is the safety net.

import (
	"context"
	"log"
	"strings"
	"time"
)

type openTradeOrder struct {
	BrokerOrderID, Phase, Status string
	Qty, Filled                  int64
}

// OpenTradeOrders: the trade's orders with a broker id that are not in a
// final state (last 2 days).
func (s *PostgresBackedStore) OpenTradeOrders(ctx context.Context, tradeUID string) ([]openTradeOrder, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT broker_order_id, COALESCE(phase, ''), COALESCE(status, ''), quantity, COALESCE(filled_qty, 0)
		FROM orders
		WHERE trade_uid = $1 AND COALESCE(broker_order_id, '') <> ''
		  AND UPPER(COALESCE(status, '')) NOT IN ('FILLED', 'CANCELLED', 'CANCELED', 'REJECTED', 'EXPIRED')
		  AND created_at > now() - interval '2 days'
		ORDER BY created_at`, tradeUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openTradeOrder
	for rows.Next() {
		var o openTradeOrder
		if err := rows.Scan(&o.BrokerOrderID, &o.Phase, &o.Status, &o.Qty, &o.Filled); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// cancelOpenOrdersBeforeExit cancels the trade's still-open orders and waits
// up to 2 s for each to settle (a fill landing meanwhile is then counted by
// the exit's fill-based sizing). Best effort: a failed cancel is logged and
// the exit goes on (the close guard still caps every exit order at the
// filled position).
func (s *Service) cancelOpenOrdersBeforeExit(tradeUID, reason string) {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	open, err := pg.OpenTradeOrders(ctx, tradeUID)
	cancel()
	if err != nil {
		log.Printf("[EXIT] trade=%s open-order check before the %s exit failed: %v -- exiting on fills", tradeUID, reason, err)
		return
	}
	if len(open) == 0 {
		return
	}
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return
	}
	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		log.Printf("[EXIT] trade=%s %d open order(s) before the %s exit NOT cancelled: %v", tradeUID, len(open), reason, err)
		return
	}
	canceller, ok := executor.(OrderCanceller)
	if !ok {
		log.Printf("[EXIT] trade=%s %d open order(s) before the %s exit: broker cannot cancel", tradeUID, len(open), reason)
		return
	}
	for _, o := range open {
		log.Printf("[EXIT] trade=%s cancelling open order %s (%s %s, filled %d/%d) before the %s exit -- the exit closes only completed fills",
			tradeUID, o.BrokerOrderID, strings.ToUpper(o.Phase), o.Status, o.Filled, o.Qty, reason)
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Second)
		cerr := canceller.CancelOrder(cctx, o.BrokerOrderID)
		ccancel()
		if cerr != nil {
			log.Printf("[EXIT] ⚠ trade=%s cancel of %s failed: %v (it may already be complete)", tradeUID, o.BrokerOrderID, cerr)
			continue
		}
		if s.OrderEvents != nil {
			wctx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
			if upd, werr := s.OrderEvents.WaitTerminal(wctx, tradeUID, o.BrokerOrderID, o.Qty, 2*time.Second); werr == nil {
				log.Printf("[EXIT] trade=%s order %s now %s (filled %d)", tradeUID, o.BrokerOrderID, upd.Status, upd.FilledQty)
			}
			wcancel()
		}
	}
}
