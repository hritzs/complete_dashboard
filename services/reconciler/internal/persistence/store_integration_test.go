//go:build integration

// Integration test against a real Postgres instance -- run explicitly
// with: go test -tags=integration ./internal/persistence/... -v
// (requires POSTGRES_DSN or defaults to the local dev DB). It creates its
// own trade_uid-namespaced test rows and cleans them up afterward; it
// does not touch any pre-existing trade/order data.
package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"trading-platform/services/reconciler/internal/normalize"
)

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
	// Registered via t.Cleanup (LIFO order), not a plain defer in the test
	// body -- row-deletion cleanups registered later by the test must run
	// BEFORE this close, or they'd silently execute against a closed
	// connection and leave test rows behind.
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApplyOrderUpdate_Integration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	testUID := fmt.Sprintf("RECONCILER_TEST_%d", time.Now().UnixNano())
	brokerOrderID := fmt.Sprintf("999%d", time.Now().Unix()%1000000)

	// Reuse any existing contract row rather than inserting a fake one
	// (broker_token/exchange is UNIQUE and we don't want to guess at a
	// value that might collide with real data).
	var contractID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM contracts LIMIT 1`).Scan(&contractID); err != nil {
		t.Skipf("no contracts row available to test against: %v", err)
	}

	var tradeID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO trades (trade_uid, symbol, status, created_at)
		VALUES ($1, 'TESTSYM', 'ACTIVE', NOW())
		RETURNING id
	`, testUID).Scan(&tradeID); err != nil {
		t.Fatalf("insert test trade: %v", err)
	}

	var orderID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO orders (trade_id, contract_id, broker_order_id, side, quantity, order_type, status, created_at, updated_at, filled_qty, pending_qty, trade_uid)
		VALUES ($1, $2, $3, 'BUY', 50, 'LIMIT', 'SUBMITTED', NOW(), NOW(), 0, 50, $4)
		RETURNING id
	`, tradeID, contractID, brokerOrderID, testUID).Scan(&orderID); err != nil {
		t.Fatalf("insert test order: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM latency_samples WHERE order_id = $1`, orderID)
		_, _ = db.ExecContext(ctx, `DELETE FROM fills WHERE trade_id = $1`, tradeID)
		_, _ = db.ExecContext(ctx, `DELETE FROM order_events WHERE order_id = $1`, orderID)
		_, _ = db.ExecContext(ctx, `DELETE FROM trade_legs WHERE trade_id = $1`, tradeID)
		_, _ = db.ExecContext(ctx, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = db.ExecContext(ctx, `DELETE FROM trades WHERE id = $1`, tradeID)
	})

	store := NewStore(db)

	// Step 1: a "Pending"/Acked update -- no fill yet.
	ackUpdate := normalize.Parse(&normalize.GreeksoftOrderResponse{
		GOrderID:       brokerOrderID,
		OrderStatus:    "Pending",
		QtyFilledToday: "0",
		PendingQty:     "50",
		Price:          "100.00",
		LuTime:         fmt.Sprintf("%d", time.Now().Unix()),
	})
	result, err := store.ApplyOrderUpdate(ctx, ackUpdate, []byte(`{"test":"ack"}`))
	if err != nil {
		t.Fatalf("ApplyOrderUpdate (ack) failed: %v", err)
	}
	if result.TradeUID != testUID {
		t.Fatalf("TradeUID = %q, want %q", result.TradeUID, testUID)
	}
	if result.Fill != nil {
		t.Fatalf("expected no fill on the ack update")
	}

	var status string
	var filledQty int64
	if err := db.QueryRowContext(ctx, `SELECT status, filled_qty FROM orders WHERE id = $1`, orderID).Scan(&status, &filledQty); err != nil {
		t.Fatalf("re-read order: %v", err)
	}
	if status != "ACKED" || filledQty != 0 {
		t.Fatalf("after ack: status=%q filled_qty=%d, want ACKED/0", status, filledQty)
	}

	var eventCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM order_events WHERE order_id = $1`, orderID).Scan(&eventCount); err != nil {
		t.Fatalf("count order_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("order_events count = %d, want 1", eventCount)
	}

	if err := store.RecordConfirmationLatencyIfFirst(ctx, result.OrderID, result.TradeUID, brokerOrderID, 42*time.Millisecond); err != nil {
		t.Fatalf("RecordConfirmationLatencyIfFirst (first push) failed: %v", err)
	}
	var latencyCount int
	var latencyUS int64
	if err := db.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(latency_us),0) FROM latency_samples WHERE order_id = $1`, orderID).Scan(&latencyCount, &latencyUS); err != nil {
		t.Fatalf("query latency_samples: %v", err)
	}
	if latencyCount != 1 || latencyUS != 42000 {
		t.Fatalf("latency_samples after first push: count=%d latency_us=%d, want 1/42000", latencyCount, latencyUS)
	}

	// Step 2: a "Traded" update with a full fill.
	fillUpdate := normalize.Parse(&normalize.GreeksoftOrderResponse{
		GOrderID:       brokerOrderID,
		OrderStatus:    "Traded",
		QtyFilledToday: "50",
		PendingQty:     "0",
		Price:          "101.50",
		LuTime:         fmt.Sprintf("%d", time.Now().Unix()),
	})
	result, err = store.ApplyOrderUpdate(ctx, fillUpdate, []byte(`{"test":"fill"}`))
	if err != nil {
		t.Fatalf("ApplyOrderUpdate (fill) failed: %v", err)
	}
	if result.Fill == nil {
		t.Fatalf("expected a fill to be recorded")
	}
	if result.Fill.InstrumentID == 0 {
		t.Fatalf("expected a non-zero InstrumentID (contract broker_token)")
	}

	var fillCount int
	var fillQty int64
	var fillPrice float64
	if err := db.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(fill_quantity),0), COALESCE(AVG(fill_price),0) FROM fills WHERE order_id = $1`, orderID).Scan(&fillCount, &fillQty, &fillPrice); err != nil {
		t.Fatalf("query fills: %v", err)
	}
	if fillCount != 1 || fillQty != 50 || fillPrice != 101.50 {
		t.Fatalf("fills: count=%d qty=%d price=%v, want 1/50/101.50", fillCount, fillQty, fillPrice)
	}

	var legQty int64
	var legStatus string
	if err := db.QueryRowContext(ctx, `SELECT current_quantity, status FROM trade_legs WHERE trade_id = $1 AND contract_id = $2`, tradeID, contractID).Scan(&legQty, &legStatus); err != nil {
		t.Fatalf("query trade_legs: %v", err)
	}
	if legQty != 50 || legStatus != "OPEN" {
		t.Fatalf("trade_legs: current_quantity=%d status=%q, want 50/OPEN (net BUY 50, no offsetting SELL)", legQty, legStatus)
	}

	// The anti-skew gate: a second push for the same order (this fill)
	// must NOT add a second latency_samples row, even though
	// ApplyOrderUpdate returns a (larger) ConfirmationLatency for it too.
	if err := store.RecordConfirmationLatencyIfFirst(ctx, result.OrderID, result.TradeUID, brokerOrderID, 9*time.Second); err != nil {
		t.Fatalf("RecordConfirmationLatencyIfFirst (second push) failed: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(latency_us),0) FROM latency_samples WHERE order_id = $1`, orderID).Scan(&latencyCount, &latencyUS); err != nil {
		t.Fatalf("re-query latency_samples: %v", err)
	}
	if latencyCount != 1 || latencyUS != 42000 {
		t.Fatalf("latency_samples after second push: count=%d latency_us=%d, want still 1/42000 (gate must ignore non-first pushes)", latencyCount, latencyUS)
	}

	// Step 3: redeliver the exact same "Traded" frame again (simulating a
	// duplicate push) -- must be a no-op, not a second fill.
	result, err = store.ApplyOrderUpdate(ctx, fillUpdate, []byte(`{"test":"fill-redelivered"}`))
	if err != nil {
		t.Fatalf("ApplyOrderUpdate (redelivered fill) failed: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM fills WHERE order_id = $1`, orderID).Scan(&fillCount); err != nil {
		t.Fatalf("re-query fills: %v", err)
	}
	if fillCount != 1 {
		t.Fatalf("fills count after redelivery = %d, want still 1 (idempotent)", fillCount)
	}
}

func TestApplyOrderUpdate_OrderNotFound(t *testing.T) {
	db := testDB(t)
	store := NewStore(db)

	update := normalize.Parse(&normalize.GreeksoftOrderResponse{
		GOrderID:    fmt.Sprintf("NOTFOUND_%d", time.Now().UnixNano()),
		OrderStatus: "Pending",
	})
	_, err := store.ApplyOrderUpdate(context.Background(), update, []byte(`{}`))
	if err != ErrOrderNotFound {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
}
