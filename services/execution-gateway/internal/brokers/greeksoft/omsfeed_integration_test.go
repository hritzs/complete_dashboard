//go:build integration

// Integration test against a real Postgres instance -- run explicitly
// with: go test -tags=integration ./internal/brokers/greeksoft/... -v
// (requires POSTGRES_DSN or defaults to the local dev DB). Creates its
// own trade_uid-namespaced test rows and cleans them up afterward.
package greeksoft

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
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
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOMSFeed_GetVerifiedFills_Integration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	testUID := fmt.Sprintf("OMSFEED_TEST_%d", time.Now().UnixNano())
	filledOrderID := fmt.Sprintf("OMSF%d", time.Now().Unix()%1000000)
	unfilledOrderID := fmt.Sprintf("OMSF%d", (time.Now().Unix()%1000000)+1)

	var contractID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM contracts LIMIT 1`).Scan(&contractID); err != nil {
		t.Skipf("no contracts row available to test against: %v", err)
	}
	var brokerToken int64
	if err := db.QueryRowContext(ctx, `SELECT broker_token FROM contracts WHERE id = $1`, contractID).Scan(&brokerToken); err != nil {
		t.Fatalf("lookup contract broker_token: %v", err)
	}

	var tradeID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO trades (trade_uid, symbol, status, created_at)
		VALUES ($1, 'TESTSYM', 'ACTIVE', NOW())
		RETURNING id
	`, testUID).Scan(&tradeID); err != nil {
		t.Fatalf("insert test trade: %v", err)
	}

	var filledOrderPK, unfilledOrderPK int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO orders (trade_id, contract_id, broker_order_id, side, quantity, order_type, status, created_at, updated_at, filled_qty, pending_qty, trade_uid)
		VALUES ($1, $2, $3, 'SELL', 65, 'LIMIT', 'FILLED', NOW(), NOW(), 65, 0, $4)
		RETURNING id
	`, tradeID, contractID, filledOrderID, testUID).Scan(&filledOrderPK); err != nil {
		t.Fatalf("insert filled test order: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO orders (trade_id, contract_id, broker_order_id, side, quantity, order_type, status, created_at, updated_at, filled_qty, pending_qty, trade_uid)
		VALUES ($1, $2, $3, 'SELL', 65, 'LIMIT', 'SUBMITTED', NOW(), NOW(), 0, 65, $4)
		RETURNING id
	`, tradeID, contractID, unfilledOrderID, testUID).Scan(&unfilledOrderPK); err != nil {
		t.Fatalf("insert unfilled test order: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM fills WHERE trade_id = $1`, tradeID)
		_, _ = db.ExecContext(ctx, `DELETE FROM orders WHERE trade_id = $1`, tradeID)
		_, _ = db.ExecContext(ctx, `DELETE FROM trades WHERE id = $1`, tradeID)
	})

	// Two partial fills for the filled order: 40 @ 100.00, 25 @ 103.00 ->
	// qty-weighted avg = (40*100 + 25*103) / 65 = 101.1538...
	if _, err := db.ExecContext(ctx, `
		INSERT INTO fills (order_id, trade_id, fill_id, fill_quantity, fill_price, fill_timestamp)
		VALUES ($1, $2, 'OMSFEED_FILL_1', 40, 100.00, NOW())
	`, filledOrderPK, tradeID); err != nil {
		t.Fatalf("insert fill 1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO fills (order_id, trade_id, fill_id, fill_quantity, fill_price, fill_timestamp)
		VALUES ($1, $2, 'OMSFEED_FILL_2', 25, 103.00, NOW())
	`, filledOrderPK, tradeID); err != nil {
		t.Fatalf("insert fill 2: %v", err)
	}

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthServer.Close()

	feed := NewOMSFeed(db, healthServer.URL)
	fills, err := feed.GetVerifiedFills(ctx)
	if err != nil {
		t.Fatalf("GetVerifiedFills failed: %v", err)
	}

	var found *struct {
		FilledQty    int64
		AveragePrice float64
		Token        int64
		Side         string
	}
	for _, f := range fills {
		if f.BrokerOrderID == filledOrderID {
			found = &struct {
				FilledQty    int64
				AveragePrice float64
				Token        int64
				Side         string
			}{f.FilledQty, f.AveragePrice, f.Token, f.Side}
		}
		if f.BrokerOrderID == unfilledOrderID {
			t.Fatalf("unfilled order %s must not appear in verified fills", unfilledOrderID)
		}
	}
	if found == nil {
		t.Fatalf("expected to find filled order %s in verified fills, got %+v", filledOrderID, fills)
	}
	if found.FilledQty != 65 {
		t.Errorf("FilledQty = %d, want 65 (40+25 summed across both fill rows)", found.FilledQty)
	}
	wantAvg := (40*100.00 + 25*103.00) / 65.0
	if diff := found.AveragePrice - wantAvg; diff > 0.01 || diff < -0.01 {
		t.Errorf("AveragePrice = %v, want ~%v (quantity-weighted average)", found.AveragePrice, wantAvg)
	}
	if found.Token != brokerToken {
		t.Errorf("Token = %d, want %d (the contract's broker_token)", found.Token, brokerToken)
	}
	if found.Side != "SELL" {
		t.Errorf("Side = %q, want SELL", found.Side)
	}
}

func TestOMSFeed_GetVerifiedFills_FallsBackWhenReconcilerUnhealthy(t *testing.T) {
	db := testDB(t)

	feed := NewOMSFeed(db, "http://127.0.0.1:1") // nothing listens here
	_, err := feed.GetVerifiedFills(context.Background())
	if err == nil {
		t.Fatal("expected an error when the reconciler health check fails, so the caller (Executor.GetVerifiedFills) falls back to REST")
	}
}
