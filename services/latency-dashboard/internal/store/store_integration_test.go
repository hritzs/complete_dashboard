//go:build integration

// Integration test against a real Postgres instance -- run explicitly
// with: go test -tags=integration ./internal/store/... -v (requires
// POSTGRES_DSN or defaults to the local dev DB). Inserts its own
// stage='TEST_STAGE' rows and cleans them up; never touches real
// iris_confirmation data.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

const testStage = "TEST_STAGE_latency_dashboard"

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/trading?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("postgres not reachable, skipping integration test: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertSample(t *testing.T, db *sql.DB, brokerOrderID string, latencyUS int64) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO latency_samples (stage, broker_order_id, latency_us)
		VALUES ($1, $2, $3)
	`, testStage, brokerOrderID, latencyUS)
	if err != nil {
		t.Fatalf("insert test latency sample: %v", err)
	}
}

// statsForStage is a test-only helper that runs the same query Stats does
// but against testStage, since Stats itself hardcodes 'iris_confirmation'
// (deliberately -- see store.go) and this test must not pollute or read
// real production latency data.
func statsForStage(t *testing.T, db *sql.DB, window time.Duration) Stats {
	t.Helper()
	var stats Stats
	var avg, p50, p95, p99 sql.NullFloat64
	var maxUS sql.NullInt64
	err := db.QueryRowContext(context.Background(), `
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
	`, testStage, fmt.Sprintf("%d seconds", int64(window.Seconds()))).Scan(
		&stats.Count, &avg, &p50, &p95, &p99, &maxUS,
	)
	if err != nil {
		t.Fatalf("query test stats: %v", err)
	}
	stats.AvgUS = avg.Float64
	stats.P50US = p50.Float64
	stats.P95US = p95.Float64
	stats.P99US = p99.Float64
	stats.MaxUS = maxUS.Int64
	return stats
}

func TestStats_Percentiles(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE stage = $1`, testStage)
	})
	_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE stage = $1`, testStage)

	// Known values: 100,200,300,...,1000 us -- p50=550, max=1000, count=10.
	for i := int64(1); i <= 10; i++ {
		insertSample(t, db, fmt.Sprintf("TESTORD%d", i), i*100)
	}

	stats := statsForStage(t, db, time.Hour)
	if stats.Count != 10 {
		t.Fatalf("Count = %d, want 10", stats.Count)
	}
	if stats.MaxUS != 1000 {
		t.Fatalf("MaxUS = %d, want 1000", stats.MaxUS)
	}
	if stats.P50US != 550 {
		t.Fatalf("P50US = %v, want 550 (percentile_cont interpolates between 500 and 600)", stats.P50US)
	}
	if stats.AvgUS != 550 {
		t.Fatalf("AvgUS = %v, want 550", stats.AvgUS)
	}
}

func TestStats_WindowExcludesOldSamples(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE stage = $1`, testStage)
	})
	_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE stage = $1`, testStage)

	insertSample(t, db, "RECENT", 500)
	_, err := db.ExecContext(ctx, `
		INSERT INTO latency_samples (stage, broker_order_id, latency_us, recorded_at)
		VALUES ($1, 'OLD', 999999, NOW() - interval '1 hour')
	`, testStage)
	if err != nil {
		t.Fatalf("insert old test sample: %v", err)
	}

	stats := statsForStage(t, db, 5*time.Minute)
	if stats.Count != 1 {
		t.Fatalf("Count = %d, want 1 (the old sample must be excluded by the window)", stats.Count)
	}
	if stats.MaxUS != 500 {
		t.Fatalf("MaxUS = %d, want 500 (must not include the old 999999 sample)", stats.MaxUS)
	}
}

func TestRecent_Integration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := NewStore(db)

	// Recent() hardcodes stage='iris_confirmation' by design (see store.go
	// doc comment), so this test uses a uniquely-prefixed broker_order_id
	// under the real stage rather than a fake one, and cleans up by that
	// prefix only.
	_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE broker_order_id LIKE 'LDTEST%'`)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE broker_order_id LIKE 'LDTEST%'`)
	})

	_, err := db.ExecContext(ctx, `
		INSERT INTO latency_samples (stage, broker_order_id, latency_us)
		VALUES ($1, 'LDTEST1', 111)
	`, stageIrisConfirmation)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	samples, err := store.Recent(ctx, 500)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	found := false
	for _, s := range samples {
		if s.BrokerOrderID == "LDTEST1" && s.LatencyUS == 111 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected to find the inserted LDTEST1 sample in Recent() results")
	}
}
