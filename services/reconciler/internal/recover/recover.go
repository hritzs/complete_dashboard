// Package recover periodically reconciles orders/trades stuck in a
// non-terminal state against GreekSoft's REST order book, as a catch-up
// mechanism -- not the primary path (Iris push is). This is what
// execution-gateway's boot log "reason=non-active recovery requires
// reconciliation" (main.go) has been waiting for: PARTIAL/
// RECONCILIATION_REQUIRED trades previously had zero automated follow-up.
package recover

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	greeksoft "trading-platform/libs/broker-greeksoft"
)

type Recoverer struct {
	db     *sql.DB
	client *greeksoft.Client
	apply  func(ctx context.Context, orderBookEntry greeksoft.OrderBookEntry) error
}

func NewRecoverer(db *sql.DB, client *greeksoft.Client, apply func(ctx context.Context, orderBookEntry greeksoft.OrderBookEntry) error) *Recoverer {
	return &Recoverer{db: db, client: client, apply: apply}
}

// Run polls once at startup and then on the given interval until ctx is
// done. Each pass: find trades in a non-terminal status with no recent
// order_events activity, pull the current order book, and hand every
// still-open order row to apply (which normalizes+persists it exactly
// like a live Iris frame would, so both paths converge on the same
// idempotent write logic).
func (r *Recoverer) Run(ctx context.Context, interval time.Duration) {
	r.runOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Recoverer) runOnce(ctx context.Context) {
	staleOrderIDs, err := r.findStaleOpenOrders(ctx)
	if err != nil {
		log.Printf("[RECOVER] find stale open orders failed: %v", err)
		return
	}
	if len(staleOrderIDs) == 0 {
		return
	}

	log.Printf("[RECOVER] %d order(s) stuck in a non-terminal state with no recent activity; polling order book", len(staleOrderIDs))

	book, err := r.client.GetOrderBookTyped(ctx, "ALL")
	if err != nil {
		log.Printf("[RECOVER] GetOrderBookTyped failed: %v", err)
		return
	}

	byGOrderID := make(map[string]greeksoft.OrderBookEntry, len(book.Data))
	for _, entry := range book.Data {
		byGOrderID[fmt.Sprintf("%d", entry.OrdID)] = entry
	}

	for _, brokerOrderID := range staleOrderIDs {
		entry, ok := byGOrderID[brokerOrderID]
		if !ok {
			continue // not in today's order book at all -- nothing to reconcile against yet
		}
		if err := r.apply(ctx, entry); err != nil {
			log.Printf("[RECOVER] apply broker_order_id=%s failed: %v", brokerOrderID, err)
		}
	}
}

// findStaleOpenOrders returns broker_order_ids for orders placed today
// that are still non-terminal and haven't had an order_events row in the
// last 2 minutes -- a live Iris frame would normally have kept them
// fresher than that.
func (r *Recoverer) findStaleOpenOrders(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.broker_order_id
		FROM orders o
		WHERE o.created_at::date = CURRENT_DATE
		  AND o.broker_order_id IS NOT NULL
		  AND o.status NOT IN ('FILLED','CANCELLED','REJECTED')
		  AND NOT EXISTS (
		      SELECT 1 FROM order_events e
		      WHERE e.order_id = o.id
		        AND e.event_timestamp > NOW() - INTERVAL '2 minutes'
		  )
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
