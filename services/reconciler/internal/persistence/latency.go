package persistence

import (
	"context"
	"time"
)

// Latency stages.
const (
	// StageIrisConfirmation is order submission -> the first Iris push seen
	// for the order (normally the exchange ack).
	StageIrisConfirmation = "iris_confirmation"
	// StageIrisFill is order submission -> the first Iris push reporting the
	// order FILLED.
	StageIrisFill = "iris_fill"
)

// EnsureLatencyIndex creates the unique (order_id, stage) index the upsert
// below relies on. Idempotent, so it is safe to run on every start.
func (s *Store) EnsureLatencyIndex(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_latency_samples_order_stage
		ON latency_samples (order_id, stage)
		WHERE order_id IS NOT NULL
	`)
	return err
}

// RecordLatency stores one latency sample per (order, stage), keeping the
// SMALLEST value seen. An order receives several pushes (ack, then fill)
// and they can be applied out of order, so "the first push" is not
// something that can be decided from how many order_events rows exist
// (the previous gate, `COUNT(*) = 1`, never passed in practice because an
// ack and its fill arrive milliseconds apart and both go through the
// retry path -- the table stayed empty). latency is measured from order
// creation to the moment the push was RECEIVED, so the smallest value is
// exactly the earliest push.
//
// Best-effort observability: it runs in its own statement, never inside
// ApplyOrderUpdate's transaction, so a failure cannot block or roll back
// the order-of-record write.
func (s *Store) RecordLatency(ctx context.Context, stage string, orderID int64, tradeUID, brokerOrderID string, latency time.Duration) error {
	tradeUIDArg := interface{}(nil)
	if tradeUID != "" {
		tradeUIDArg = tradeUID
	}
	if latency < 0 {
		latency = 0
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO latency_samples (stage, order_id, trade_uid, broker_order_id, latency_us)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (order_id, stage) WHERE order_id IS NOT NULL
		DO UPDATE SET latency_us = LEAST(latency_samples.latency_us, EXCLUDED.latency_us)
	`, stage, orderID, tradeUIDArg, brokerOrderID, latency.Microseconds())
	return err
}
