package trading

// Empty trades. A build whose orders went out but never filled (rejected by
// the price band, cancelled unfilled -- e.g. the mock session of 2026-10-10)
// holds no position, yet it used to be parked as RECONCILIATION_REQUIRED
// "for a human" and then stayed on the Portfolio day after day. Two rules:
//
//   - right after a build: if EVERY order of the trade is confirmed
//     terminal (rejected / cancelled) with nothing filled, the trade is
//     FAILED -- there is nothing to reconcile;
//   - on a new day (at start and once a minute with the position check): a
//     trade from an EARLIER day that is still not closed and has no fill at
//     all is FAILED too -- its orders were day orders and are dead.
//
// A trade with any fill, or with an order whose outcome is unknown on its
// own day, is never touched here.

import (
	"context"
	"log"
	"time"
)

// tradeFillState: how many orders the trade has, how many are confirmed
// terminal without a fill, and the total filled quantity (orders + fills).
func (s *PostgresBackedStore) tradeFillState(ctx context.Context, tradeUID string) (orders, deadUnfilled int, filled int64, err error) {
	if s == nil || s.db == nil {
		return 0, 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE UPPER(COALESCE(o.status,'')) IN ('REJECTED','CANCELLED','CANCELED') AND COALESCE(o.filled_qty,0) = 0),
		       COALESCE(SUM(COALESCE(o.filled_qty,0)),0)
		         + COALESCE((SELECT SUM(f.fill_quantity) FROM fills f JOIN orders o2 ON o2.id = f.order_id WHERE o2.trade_uid = $1),0)
		FROM orders o WHERE o.trade_uid = $1`, tradeUID).Scan(&orders, &deadUnfilled, &filled)
	return orders, deadUnfilled, filled, err
}

// buildLeftNothing: every order of the trade is confirmed dead and unfilled.
func (s *Service) buildLeftNothing(ctx context.Context, tradeUID string) bool {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return false
	}
	orders, dead, filled, err := pg.tradeFillState(ctx, tradeUID)
	return err == nil && orders > 0 && dead == orders && filled == 0
}

// sweepEmptyOldTrades closes (FAILED) every not-closed trade created before
// today that never had a fill.
func (s *Service) sweepEmptyOldTrades() {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok || pg.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	y, m, d := time.Now().In(lutIST()).Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, lutIST())
	rows, err := pg.db.QueryContext(ctx, `
		SELECT t.trade_uid, t.status FROM trades t
		WHERE t.created_at < $1
		  AND t.status IN ('RECONCILIATION_REQUIRED','BUILDING','PARTIAL')`, dayStart)
	if err != nil {
		return
	}
	type row struct{ uid, status string }
	var cand []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.uid, &r.status) == nil {
			cand = append(cand, r)
		}
	}
	rows.Close()
	for _, r := range cand {
		_, _, filled, ferr := pg.tradeFillState(ctx, r.uid)
		if ferr != nil || filled != 0 {
			continue // has (or may have) a position: left for reconciliation
		}
		tr, ok := s.Store.LoadTrade(r.uid)
		if !ok || isTerminalTradeStatus(tr.Status) {
			continue
		}
		tr.Status, tr.CEQty, tr.PEQty, tr.LastUpdateTime, tr.ClosedAt = "FAILED", 0, 0, time.Now(), time.Now()
		s.Store.UpdateTrade(tr)
		log.Printf("[EMPTY-TRADE] %s (%s, an earlier day) never had a fill -- marked FAILED and off the Portfolio", r.uid, r.status)
	}
}
