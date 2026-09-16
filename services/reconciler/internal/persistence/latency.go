package persistence

import (
	"context"
	"time"
)

// RecordConfirmationLatencyIfFirst inserts one latency_samples row
// (stage='iris_confirmation') for orderID, but only if this is the
// first order_events row ever recorded for it -- i.e. the first Iris
// push processed for this order.
//
// ApplyResult.ConfirmationLatency is time.Since(orderCreatedAt)
// recomputed fresh on every ApplyOrderUpdate call, and one order
// receives multiple pushes over its life (e.g. an ack, then a fill).
// Sampling every push would record several ever-growing numbers per
// order and badly skew percentile/max stats toward whatever the
// slowest-terminal-status orders look like, rather than measuring real
// confirmation latency. The gate below keys off order_id (the internal,
// non-recycling PK) rather than broker_order_id, which this package has
// already documented as reused across trading days.
//
// This is best-effort observability: it deliberately runs in its own
// statement, not inside ApplyOrderUpdate's transaction, so a failure
// here can never roll back or block the order-of-record write.
func (s *Store) RecordConfirmationLatencyIfFirst(ctx context.Context, orderID int64, tradeUID, brokerOrderID string, latency time.Duration) error {
	tradeUIDArg := interface{}(nil)
	if tradeUID != "" {
		tradeUIDArg = tradeUID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO latency_samples (stage, order_id, trade_uid, broker_order_id, latency_us)
		SELECT 'iris_confirmation', $1::bigint, $2::text, $3::text, $4::bigint
		WHERE (SELECT COUNT(*) FROM order_events WHERE order_id = $1::bigint) = 1
	`, orderID, tradeUIDArg, brokerOrderID, latency.Microseconds())
	return err
}
