package trading

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

func countOrdersByPhaseForTrade(t *testing.T, db *sql.DB, tradeUID string, phase string) int {
	t.Helper()

	phase = strings.ToUpper(strings.TrimSpace(phase))

	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM orders
		WHERE trade_uid = $1
		  AND (
			UPPER(COALESCE(intent_id, '')) LIKE $2 || '_%'
			OR UPPER(COALESCE(order_uid, '')) LIKE $2 || '_%'
			OR UPPER(COALESCE(raw_broker_request::text, '')) LIKE '%"PHASE":"' || $2 || '"%'
			OR UPPER(COALESCE(raw_broker_request::text, '')) LIKE '%"PHASE": "' || $2 || '"%'
		  )
	`, tradeUID, phase).Scan(&count)
	if err != nil {
		t.Fatalf("countOrdersByPhaseForTrade failed phase=%s err=%v", phase, err)
	}

	return count
}

func TestSquareOffCreatesSQFIntentRowsForFilledBuild(t *testing.T) {
	db := openIntegrationDB(t)
	defer db.Close()

	store := NewPostgresBackedStore(db)
	svc := &Service{
		Store: store,
	}

	tradeUID := uniqueTradeUID("SQF_EXPECTED")
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

	beforeSQFOrders := countOrdersByPhaseForTrade(t, db, tradeUID, "SQF")

	err := svc.SquareOff(tradeUID, "manual")
	if err != nil {
		t.Fatalf("SquareOff returned error: %v", err)
	}

	afterSQFOrders := countOrdersByPhaseForTrade(t, db, tradeUID, "SQF")

	if afterSQFOrders <= beforeSQFOrders {
		t.Fatalf(
			"expected SquareOff to create SQF order rows; before=%d after=%d",
			beforeSQFOrders,
			afterSQFOrders,
		)
	}

	tr, ok := store.LoadTrade(tradeUID)
	if !ok {
		t.Fatalf("trade not found after SquareOff")
	}

	if tr.Status == "CLOSED" {
		t.Fatalf("SquareOff must not mark CLOSED until SQF orders are actually filled")
	}
}
