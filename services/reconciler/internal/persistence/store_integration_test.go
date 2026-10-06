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

	"trading-platform/libs/broker-greeksoft/normalize"
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

	if err := store.RecordLatency(ctx, StageIrisConfirmation, result.OrderID, result.TradeUID, brokerOrderID, 42*time.Millisecond); err != nil {
		t.Fatalf("RecordLatency (first push) failed: %v", err)
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
	if err := store.RecordLatency(ctx, StageIrisConfirmation, result.OrderID, result.TradeUID, brokerOrderID, 9*time.Second); err != nil {
		t.Fatalf("RecordLatency (second push) failed: %v", err)
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

// TestApplyOrderUpdate_LateOutOfOrderPushCannotRegressATerminalOrder
// confirmed live 2026-09-23: a 154-order build (77 lots x 2 legs, fired in
// well under a second) caused Iris pushes to arrive out of order for many
// orders -- a late "Pending"/ACKED push landing after the order's real
// FILLED push had already been applied silently overwrote status back to
// ACKED and filled_qty back to 0, even though the order had genuinely
// filled. 104 of 154 orders ended up permanently stuck showing
// SUBMITTED/ACKED in the DB. The fills/trade_legs accounting was never
// affected (it's driven by the separate, idempotent fills table), but the
// order row itself became a false "stuck order" the reconciler's recovery
// poller kept re-polling forever, and any caller trusting
// orders.filled_qty directly (like TradeOpenQuantities before it was
// switched to read from fills) would have under-counted a real position.
func TestApplyOrderUpdate_LateOutOfOrderPushCannotRegressATerminalOrder(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	testUID := fmt.Sprintf("RECONCILER_TEST_REGRESSION_%d", time.Now().UnixNano())
	brokerOrderID := fmt.Sprintf("998%d", time.Now().Unix()%1000000)

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
		VALUES ($1, $2, $3, 'BUY', 65, 'MARKET', 'SUBMITTED', NOW(), NOW(), 0, 65, $4)
		RETURNING id
	`, tradeID, contractID, brokerOrderID, testUID).Scan(&orderID); err != nil {
		t.Fatalf("insert test order: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM fills WHERE trade_id = $1`, tradeID)
		_, _ = db.ExecContext(ctx, `DELETE FROM order_events WHERE order_id = $1`, orderID)
		_, _ = db.ExecContext(ctx, `DELETE FROM trade_legs WHERE trade_id = $1`, tradeID)
		_, _ = db.ExecContext(ctx, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = db.ExecContext(ctx, `DELETE FROM trades WHERE id = $1`, tradeID)
	})

	store := NewStore(db)

	// The order fills in full first (as it really did at the broker).
	fillUpdate := normalize.Parse(&normalize.GreeksoftOrderResponse{
		GOrderID: brokerOrderID, OrderStatus: "Traded",
		QtyFilledToday: "65", PendingQty: "0", Price: "100.00",
		LuTime: fmt.Sprintf("%d", time.Now().Unix()),
	})
	if _, err := store.ApplyOrderUpdate(ctx, fillUpdate, []byte(`{"test":"fill"}`)); err != nil {
		t.Fatalf("ApplyOrderUpdate (fill) failed: %v", err)
	}

	// A late, out-of-order ACKED push arrives afterward (as if Iris's own
	// earlier ack got delayed in transit past the fill). Must NOT regress
	// the order.
	lateAck := normalize.Parse(&normalize.GreeksoftOrderResponse{
		GOrderID: brokerOrderID, OrderStatus: "Pending",
		QtyFilledToday: "0", PendingQty: "65", Price: "100.00",
		LuTime: fmt.Sprintf("%d", time.Now().Unix()),
	})
	if _, err := store.ApplyOrderUpdate(ctx, lateAck, []byte(`{"test":"late-ack"}`)); err != nil {
		t.Fatalf("ApplyOrderUpdate (late ack) failed: %v", err)
	}

	var status string
	var filledQty, pendingQty int64
	if err := db.QueryRowContext(ctx, `SELECT status, filled_qty, pending_qty FROM orders WHERE id = $1`, orderID).Scan(&status, &filledQty, &pendingQty); err != nil {
		t.Fatalf("re-read order: %v", err)
	}
	if status != "FILLED" || filledQty != 65 || pendingQty != 0 {
		t.Fatalf("after late out-of-order ACKED push: status=%q filled_qty=%d pending_qty=%d, want FILLED/65/0 (must stay as it was after the real fill)",
			status, filledQty, pendingQty)
	}

	// The late push must still be recorded for audit purposes, just not
	// applied to the order row itself.
	var eventCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM order_events WHERE order_id = $1`, orderID).Scan(&eventCount); err != nil {
		t.Fatalf("count order_events: %v", err)
	}
	if eventCount != 2 {
		t.Fatalf("order_events count = %d, want 2 (fill + late ack, both recorded)", eventCount)
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
