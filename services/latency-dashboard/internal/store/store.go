// Package store reads latency_samples from Postgres. This service never
// writes samples -- services/reconciler writes them directly (see
// services/reconciler/internal/persistence/latency.go) -- it only reads
// and aggregates for the dashboard UI.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	stageIrisConfirmation = "iris_confirmation"
	stageIrisFill         = "iris_fill"
)

type Sample struct {
	ID            int64     `json:"id"`
	OrderID       *int64    `json:"order_id,omitempty"`
	TradeUID      string    `json:"trade_uid,omitempty"`
	BrokerOrderID string    `json:"broker_order_id"`
	LatencyUS     int64     `json:"latency_us"`
	RecordedAt    time.Time `json:"recorded_at"`
}

// Stats is computed over a time window via Postgres's own percentile_cont
// rather than in application code -- at this platform's order volume
// (dozens to low hundreds/day), that's the right-sized tool and avoids
// needing a stats library.
type Stats struct {
	Count int64   `json:"count"`
	AvgUS float64 `json:"avg_us"`
	P50US float64 `json:"p50_us"`
	P95US float64 `json:"p95_us"`
	P99US float64 `json:"p99_us"`
	MaxUS int64   `json:"max_us"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Recent returns the most recent iris_confirmation samples, newest first.
func (s *Store) Recent(ctx context.Context, limit int) ([]Sample, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, trade_uid, broker_order_id, latency_us, recorded_at
		FROM latency_samples
		WHERE stage = $1
		ORDER BY recorded_at DESC
		LIMIT $2
	`, stageIrisConfirmation, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent latency samples: %w", err)
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var sample Sample
		var orderID sql.NullInt64
		var tradeUID sql.NullString
		if err := rows.Scan(&sample.ID, &orderID, &tradeUID, &sample.BrokerOrderID, &sample.LatencyUS, &sample.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan latency sample: %w", err)
		}
		if orderID.Valid {
			sample.OrderID = &orderID.Int64
		}
		sample.TradeUID = tradeUID.String
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latency samples: %w", err)
	}
	return out, nil
}

// Stats aggregates iris_confirmation samples recorded within the last
// window (e.g. 5*time.Minute).
func (s *Store) Stats(ctx context.Context, window time.Duration) (Stats, error) {
	return s.StatsForStage(ctx, stageIrisConfirmation, window)
}

// StatsForStage is Stats for one latency stage ("iris_confirmation" or
// "iris_fill").
func (s *Store) StatsForStage(ctx context.Context, stage string, window time.Duration) (Stats, error) {
	var stats Stats
	var avg, p50, p95, p99 sql.NullFloat64
	var maxUS sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			AVG(latency_us),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_us),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_us),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_us),
			MAX(latency_us)
		FROM latency_samples
		WHERE stage = $1
		  AND recorded_at > NOW() - $2::interval
	`, stage, fmt.Sprintf("%d seconds", int64(window.Seconds()))).Scan(
		&stats.Count, &avg, &p50, &p95, &p99, &maxUS,
	)
	if err != nil {
		return Stats{}, fmt.Errorf("query latency stats: %w", err)
	}

	stats.AvgUS = avg.Float64
	stats.P50US = p50.Float64
	stats.P95US = p95.Float64
	stats.P99US = p99.Float64
	stats.MaxUS = maxUS.Int64
	return stats, nil
}

// OrderEvidence is one GreekSoft order with the proof of HOW its state
// reached us: how many of its recorded events were Iris websocket pushes
// (raw frames carry a streaming_type) versus REST recovery reads.
type OrderEvidence struct {
	OrderID       int64     `json:"order_id"`
	TradeUID      string    `json:"trade_uid"`
	BrokerOrderID string    `json:"broker_order_id"`
	Side          string    `json:"side"`
	Quantity      int64     `json:"quantity"`
	Phase         string    `json:"phase"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	AckUS         *int64    `json:"ack_us"`
	FillUS        *int64    `json:"fill_us"`
	IrisEvents    int64     `json:"iris_events"`
	TotalEvents   int64     `json:"total_events"`
	// Source is IRIS_WS when at least one event came from the websocket,
	// REST_ONLY when events exist but none did, NONE when nothing was
	// recorded for the order at all.
	Source string `json:"source"`
}

func classifySource(iris, total int64) string {
	switch {
	case iris > 0:
		return "IRIS_WS"
	case total > 0:
		return "REST_ONLY"
	default:
		return "NONE"
	}
}

// Orders returns GreekSoft orders created within window, newest first.
func (s *Store) Orders(ctx context.Context, window time.Duration, limit int) ([]OrderEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id,
		       COALESCE(o.trade_uid, ''),
		       COALESCE(o.broker_order_id, ''),
		       o.side, o.quantity, COALESCE(o.phase, ''), o.status, o.created_at,
		       (SELECT latency_us FROM latency_samples l WHERE l.order_id = o.id AND l.stage = $3),
		       (SELECT latency_us FROM latency_samples l WHERE l.order_id = o.id AND l.stage = $4),
		       (SELECT COUNT(*) FROM order_events e
		         WHERE e.order_id = o.id AND e.raw_broker_response::text LIKE '%streaming_type%'),
		       (SELECT COUNT(*) FROM order_events e WHERE e.order_id = o.id)
		FROM orders o
		WHERE o.broker_name = 'GREEKSOFT'
		  AND o.created_at > NOW() - $1::interval
		ORDER BY o.created_at DESC
		LIMIT $2
	`, fmt.Sprintf("%d seconds", int64(window.Seconds())), limit, stageIrisConfirmation, stageIrisFill)
	if err != nil {
		return nil, fmt.Errorf("query order evidence: %w", err)
	}
	defer rows.Close()

	out := []OrderEvidence{}
	for rows.Next() {
		var o OrderEvidence
		var ack, fill sql.NullInt64
		if err := rows.Scan(&o.OrderID, &o.TradeUID, &o.BrokerOrderID, &o.Side, &o.Quantity,
			&o.Phase, &o.Status, &o.CreatedAt, &ack, &fill, &o.IrisEvents, &o.TotalEvents); err != nil {
			return nil, fmt.Errorf("scan order evidence: %w", err)
		}
		if ack.Valid {
			o.AckUS = &ack.Int64
		}
		if fill.Valid {
			o.FillUS = &fill.Int64
		}
		o.Source = classifySource(o.IrisEvents, o.TotalEvents)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order evidence: %w", err)
	}
	return out, nil
}
