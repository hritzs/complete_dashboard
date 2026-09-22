//go:build integration

// Integration test against a real Postgres instance -- run explicitly with:
//
//	go test -tags=integration ./internal/trading/... -run AllTrades -v
//
// (requires POSTGRES_DSN or defaults to the local dev DB). Inserts its own
// trade row and deletes it in a defer; never touches real trade data.
package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func testPGStore(t *testing.T) (*PostgresBackedStore, *sql.DB) {
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
		t.Skipf("postgres not reachable, skipping: %v", err)
	}
	return NewPostgresBackedStore(db), db
}

// This reproduces the 2026-09-22 bug: AllTrades() (backing /api/straddles,
// which the Portfolio UI's Monitor Status / Net Greeks cards read from) never
// selected the config column, so every trade's Config, Lots, LotSize, Strike
// and tokens silently read as the Go zero value in the UI, and an unset
// SquareOffHardTime (year-1) rendered through a timezone formatter as a bogus
// wall-clock time instead of "Not configured".
func TestAllTrades_HydratesConfigLotsAndTokens(t *testing.T) {
	store, db := testPGStore(t)
	defer db.Close()

	uid := "TRD_TEST_ALLTRADES_HYDRATION_" + time.Now().Format("150405.000000")
	tr := StoredTrade{
		TradeUID: uid, UserID: "U001", BrokerName: "GREEKSOFT", AccountID: "147",
		Symbol: "NIFTY", Strike: 23450, Status: "ACTIVE", LotSize: 65, Lots: 1,
		CEToken: 57003, PEToken: 57006, CEQty: 65, PEQty: 65,
		CreatedAt: time.Now(),
		Config: MonitorConfig{
			HedgeDiv: 57, StraddleDiv: 4, SLPnLBpsOfSpot: 14,
			SquareOffHardTime: time.Date(2026, 9, 22, 9, 27, 0, 0, time.Local),
		},
	}
	store.SaveTrade(tr)
	defer func() {
		_, _ = db.Exec(`DELETE FROM trades WHERE trade_uid = $1`, uid)
	}()

	all := store.AllTrades()
	var got *StoredTrade
	for i := range all {
		if all[i].TradeUID == uid {
			got = &all[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("trade %s not found in AllTrades() (got %d trades)", uid, len(all))
	}

	if got.LotSize != 65 || got.Lots != 1 || got.Strike != 23450 {
		t.Fatalf("lot_size/lots/strike not hydrated: %+v", got)
	}
	if got.CEToken != 57003 || got.PEToken != 57006 {
		t.Fatalf("tokens not hydrated: %+v", got)
	}
	if got.Config.HedgeDiv != 57 || got.Config.StraddleDiv != 4 || got.Config.SLPnLBpsOfSpot != 14 {
		t.Fatalf("MonitorConfig not hydrated: %+v", got.Config)
	}
	if got.Config.SquareOffHardTime.Year() != 2026 {
		t.Fatalf("square_off_hard_time not hydrated: %v", got.Config.SquareOffHardTime)
	}

	// A trade that never had a hard square-off time set must round-trip as
	// the zero value, not silently gain one -- the UI's own year-2000 guard
	// is what turns this into "Not configured", not the backend.
	uid2 := uid + "_NOSQF"
	tr2 := tr
	tr2.TradeUID = uid2
	tr2.Config.SquareOffHardTime = time.Time{}
	store.SaveTrade(tr2)
	defer func() {
		_, _ = db.Exec(`DELETE FROM trades WHERE trade_uid = $1`, uid2)
	}()

	all = store.AllTrades()
	for i := range all {
		if all[i].TradeUID == uid2 {
			if all[i].Config.SquareOffHardTime.Year() > 1 {
				t.Fatalf("expected the zero-value time to round-trip as zero, got %v", all[i].Config.SquareOffHardTime)
			}
			raw, _ := json.Marshal(all[i].Config.SquareOffHardTime)
			if string(raw) != `"0001-01-01T00:00:00Z"` {
				t.Fatalf("unexpected zero-time JSON encoding: %s", raw)
			}
			return
		}
	}
	t.Fatalf("trade %s not found in AllTrades()", uid2)
}
