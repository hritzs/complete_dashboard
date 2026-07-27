package trading

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

const (
	testUserID     = "U_TEST"
	testBrokerName = "GREEKSOFT"
	testAccountID  = "HRITIK"
	testSymbol     = "NIFTY"
	testLotSize    = int64(65)
)

func openIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("POSTGRES_DSN is empty; skipping DB-backed integration test")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("db.Ping failed: %v", err)
	}

	return db
}

func newIntegrationHandlers(db *sql.DB) *Handlers {
	return &Handlers{
		Store: NewPostgresBackedStore(db),
	}
}

func cleanupTestTrade(t *testing.T, db *sql.DB, tradeUID string) {
	t.Helper()

	_, _ = db.Exec(`DELETE FROM order_events WHERE order_id IN (SELECT id FROM orders WHERE trade_uid = $1)`, tradeUID)
	_, _ = db.Exec(`DELETE FROM orders WHERE trade_uid = $1`, tradeUID)
	_, _ = db.Exec(`DELETE FROM trades WHERE trade_uid = $1`, tradeUID)
}

func insertDummyTrade(t *testing.T, db *sql.DB, tradeUID string, status string) {
	t.Helper()

	rawConfig := fmt.Sprintf(`{
		"trade_uid": %q,
		"user_id": %q,
		"broker_name": %q,
		"account_id": %q,
		"symbol": %q,
		"status": %q,
		"lot_size": %d,
		"lots": 1,
		"product_type": "NRML",
		"exchange_segment": "NSEFO",
		"strike": 24000,
		"expiry": "28JUL26",
		"ce_token": 63939,
		"pe_token": 63940
	}`, tradeUID, testUserID, testBrokerName, testAccountID, testSymbol, status, testLotSize)

	_, err := db.Exec(`
		INSERT INTO trades (
			trade_uid,
			user_id,
			broker_name,
			account_id,
			symbol,
			status,
			config,
			created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		ON CONFLICT (trade_uid) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			broker_name = EXCLUDED.broker_name,
			account_id = EXCLUDED.account_id,
			symbol = EXCLUDED.symbol,
			status = EXCLUDED.status,
			config = EXCLUDED.config
	`, tradeUID, testUserID, testBrokerName, testAccountID, testSymbol, status, rawConfig)
	if err != nil {
		t.Fatalf("insertDummyTrade failed: %v", err)
	}
}

type dummyOrderInput struct {
	TradeUID      string
	IntentID      string
	OrderUID      string
	BrokerOrderID string
	Phase         string
	LegType       string
	Token         int64
	Side          string
	Quantity      int64
	Status        string
	FilledQty     int64
	PendingQty    int64
	AvgFillPrice  float64
}

func insertDummyOrder(t *testing.T, db *sql.DB, in dummyOrderInput) {
	t.Helper()

	rawIntent := fmt.Sprintf(`{
		"intent_id": %q,
		"order_uid": %q,
		"trade_uid": %q,
		"broker_name": %q,
		"account_id": %q,
		"symbol": %q,
		"phase": %q,
		"leg_type": %q,
		"token": %d,
		"side": %q,
		"quantity": %d,
		"order_type": "LIMIT",
		"product_type": "NRML",
		"exchange_segment": "NSEFO"
	}`, in.IntentID, in.OrderUID, in.TradeUID, testBrokerName, testAccountID, testSymbol, in.Phase, in.LegType, in.Token, in.Side, in.Quantity)

	_, err := db.Exec(`
		INSERT INTO orders (
			intent_id,
			order_uid,
			trade_uid,
			broker_name,
			account_id,
			side,
			quantity,
			order_type,
			limit_price,
			status,
			raw_broker_request,
			broker_order_id,
			filled_qty,
			pending_qty,
			avg_fill_price,
			created_at,
			updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'LIMIT',0.05,$8,$9,$10,$11,$12,$13,NOW(),NOW())
		ON CONFLICT (intent_id) DO UPDATE SET
			order_uid = EXCLUDED.order_uid,
			trade_uid = EXCLUDED.trade_uid,
			broker_name = EXCLUDED.broker_name,
			account_id = EXCLUDED.account_id,
			side = EXCLUDED.side,
			quantity = EXCLUDED.quantity,
			status = EXCLUDED.status,
			raw_broker_request = EXCLUDED.raw_broker_request,
			broker_order_id = EXCLUDED.broker_order_id,
			filled_qty = EXCLUDED.filled_qty,
			pending_qty = EXCLUDED.pending_qty,
			avg_fill_price = EXCLUDED.avg_fill_price,
			updated_at = NOW()
	`,
		in.IntentID,
		in.OrderUID,
		in.TradeUID,
		testBrokerName,
		testAccountID,
		in.Side,
		in.Quantity,
		in.Status,
		rawIntent,
		in.BrokerOrderID,
		in.FilledQty,
		in.PendingQty,
		in.AvgFillPrice,
	)
	if err != nil {
		t.Fatalf("insertDummyOrder failed intent=%s err=%v", in.IntentID, err)
	}
}

func callJSON(t *testing.T, handler http.HandlerFunc, method string, path string, out any) {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("handler returned HTTP %d body=%s", rec.Code, rec.Body.String())
	}

	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("json decode failed: %v body=%s", err, rec.Body.String())
	}
}

func uniqueTradeUID(prefix string) string {
	return fmt.Sprintf("TEST_IT_%s_%d", prefix, time.Now().UnixNano())
}

func TestDummyPendingFillTradeRefreshLifecycle(t *testing.T) {
	db := openIntegrationDB(t)
	defer db.Close()

	h := newIntegrationHandlers(db)

	tradeUID := uniqueTradeUID("PENDING_FILL")
	cleanupTestTrade(t, db, tradeUID)
	defer cleanupTestTrade(t, db, tradeUID)

	insertDummyTrade(t, db, tradeUID, TradeStatusFailed)

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_CE",
		OrderUID:      "BUI_" + tradeUID + "_CE",
		BrokerOrderID: "TEST_BROKER_OPEN_CE",
		Phase:         "BUILD",
		LegType:       "CE",
		Token:         63939,
		Side:          "SELL",
		Quantity:      testLotSize,
		Status:        "OPEN",
		FilledQty:     0,
		PendingQty:    testLotSize,
		AvgFillPrice:  0,
	})

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_PE",
		OrderUID:      "BUI_" + tradeUID + "_PE",
		BrokerOrderID: "TEST_BROKER_OPEN_PE",
		Phase:         "BUILD",
		LegType:       "PE",
		Token:         63940,
		Side:          "SELL",
		Quantity:      testLotSize,
		Status:        "OPEN",
		FilledQty:     0,
		PendingQty:    testLotSize,
		AvgFillPrice:  0,
	})

	var refresh RefreshTradeLifecycleResponse
	callJSON(
		t,
		h.RefreshTradeLifecycle,
		http.MethodPost,
		"/api/trade/refresh-lifecycle?trade_uid="+tradeUID,
		&refresh,
	)

	if refresh.NewStatus != TradeStatusPendingFill {
		t.Fatalf("expected %s, got %s response=%+v", TradeStatusPendingFill, refresh.NewStatus, refresh)
	}

	if refresh.Counts.Total != 2 || refresh.Counts.Pending != 2 || refresh.Counts.Filled != 0 {
		t.Fatalf("unexpected counts: %+v", refresh.Counts)
	}

	var summary ExecutionSummaryResponse
	callJSON(
		t,
		h.ExecutionSummary,
		http.MethodGet,
		"/api/trade/execution-summary?trade_uid="+tradeUID,
		&summary,
	)

	if summary.TradeStatus != TradeStatusPendingFill {
		t.Fatalf("expected summary trade_status %s, got %s", TradeStatusPendingFill, summary.TradeStatus)
	}

	if len(summary.GroupedOrders["BUILD"]) != 2 {
		t.Fatalf("expected 2 BUILD orders, got %d", len(summary.GroupedOrders["BUILD"]))
	}

	var sqf SquareOffPlanResponse
	callJSON(
		t,
		h.SquareOffPlan,
		http.MethodGet,
		"/api/trade/squareoff-plan?trade_uid="+tradeUID,
		&sqf,
	)

	if len(sqf.Legs) != 0 {
		t.Fatalf("expected no squareoff legs for unfilled pending trade, got %+v", sqf.Legs)
	}
}

func TestDummyActiveFilledBuildSquareOffPlan(t *testing.T) {
	db := openIntegrationDB(t)
	defer db.Close()

	h := newIntegrationHandlers(db)

	tradeUID := uniqueTradeUID("ACTIVE_SQF")
	cleanupTestTrade(t, db, tradeUID)
	defer cleanupTestTrade(t, db, tradeUID)

	insertDummyTrade(t, db, tradeUID, TradeStatusPendingFill)

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_CE",
		OrderUID:      "BUI_" + tradeUID + "_CE",
		BrokerOrderID: "TEST_BROKER_FILLED_CE",
		Phase:         "BUILD",
		LegType:       "CE",
		Token:         63939,
		Side:          "SELL",
		Quantity:      testLotSize,
		Status:        "FILLED",
		FilledQty:     testLotSize,
		PendingQty:    0,
		AvgFillPrice:  101.25,
	})

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_PE",
		OrderUID:      "BUI_" + tradeUID + "_PE",
		BrokerOrderID: "TEST_BROKER_FILLED_PE",
		Phase:         "BUILD",
		LegType:       "PE",
		Token:         63940,
		Side:          "SELL",
		Quantity:      testLotSize,
		Status:        "FILLED",
		FilledQty:     testLotSize,
		PendingQty:    0,
		AvgFillPrice:  99.75,
	})

	var refresh RefreshTradeLifecycleResponse
	callJSON(
		t,
		h.RefreshTradeLifecycle,
		http.MethodPost,
		"/api/trade/refresh-lifecycle?trade_uid="+tradeUID,
		&refresh,
	)

	if refresh.NewStatus != TradeStatusActive {
		t.Fatalf("expected %s, got %s response=%+v", TradeStatusActive, refresh.NewStatus, refresh)
	}

	var sqf SquareOffPlanResponse
	callJSON(
		t,
		h.SquareOffPlan,
		http.MethodGet,
		"/api/trade/squareoff-plan?trade_uid="+tradeUID,
		&sqf,
	)

	if len(sqf.Legs) != 2 {
		t.Fatalf("expected 2 squareoff legs, got %d response=%+v", len(sqf.Legs), sqf)
	}

	for _, leg := range sqf.Legs {
		if leg.SquareOffSide != "BUY" {
			t.Fatalf("expected BUY squareoff side for short build leg, got %+v", leg)
		}
		if leg.SquareOffQuantity != testLotSize {
			t.Fatalf("expected squareoff quantity %d, got %+v", testLotSize, leg)
		}
		if leg.Lots != 1 {
			t.Fatalf("expected 1 lot, got %+v", leg)
		}
	}
}

func TestDummyExecutionSummaryGroupsBuildHedgeAndSQF(t *testing.T) {
	db := openIntegrationDB(t)
	defer db.Close()

	h := newIntegrationHandlers(db)

	tradeUID := uniqueTradeUID("GROUPS")
	cleanupTestTrade(t, db, tradeUID)
	defer cleanupTestTrade(t, db, tradeUID)

	insertDummyTrade(t, db, tradeUID, TradeStatusActive)

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_CE",
		OrderUID:      "BUI_" + tradeUID + "_CE",
		BrokerOrderID: "TEST_BUILD_CE",
		Phase:         "BUILD",
		LegType:       "CE",
		Token:         63939,
		Side:          "SELL",
		Quantity:      testLotSize,
		Status:        "FILLED",
		FilledQty:     testLotSize,
		PendingQty:    0,
		AvgFillPrice:  100,
	})

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_PE",
		OrderUID:      "BUI_" + tradeUID + "_PE",
		BrokerOrderID: "TEST_BUILD_PE",
		Phase:         "BUILD",
		LegType:       "PE",
		Token:         63940,
		Side:          "SELL",
		Quantity:      testLotSize,
		Status:        "FILLED",
		FilledQty:     testLotSize,
		PendingQty:    0,
		AvgFillPrice:  100,
	})

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "HEDGE_" + tradeUID + "_CE",
		OrderUID:      "HEDGE_" + tradeUID + "_CE",
		BrokerOrderID: "TEST_HEDGE_CE",
		Phase:         "HEDGE",
		LegType:       "CE",
		Token:         63939,
		Side:          "BUY",
		Quantity:      testLotSize,
		Status:        "FILLED",
		FilledQty:     testLotSize,
		PendingQty:    0,
		AvgFillPrice:  102,
	})

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "SQF_" + tradeUID + "_CE",
		OrderUID:      "SQF_" + tradeUID + "_CE",
		BrokerOrderID: "TEST_SQF_CE",
		Phase:         "SQF",
		LegType:       "CE",
		Token:         63939,
		Side:          "BUY",
		Quantity:      testLotSize,
		Status:        "FILLED",
		FilledQty:     testLotSize,
		PendingQty:    0,
		AvgFillPrice:  103,
	})

	var summary ExecutionSummaryResponse
	callJSON(
		t,
		h.ExecutionSummary,
		http.MethodGet,
		"/api/trade/execution-summary?trade_uid="+tradeUID,
		&summary,
	)

	if len(summary.GroupedOrders["BUILD"]) != 2 {
		t.Fatalf("expected 2 BUILD orders, got %d", len(summary.GroupedOrders["BUILD"]))
	}

	if len(summary.GroupedOrders["HEDGE"]) != 1 {
		t.Fatalf("expected 1 HEDGE order, got %d", len(summary.GroupedOrders["HEDGE"]))
	}

	if len(summary.GroupedOrders["SQF"]) != 1 {
		t.Fatalf("expected 1 SQF order, got %d grouped keys=%+v", len(summary.GroupedOrders["SQF"]), summary.GroupedOrders)
	}

	if summary.Counts["FILLED_OK"] != 4 {
		t.Fatalf("expected 4 FILLED_OK orders, got counts=%+v", summary.Counts)
	}
}
