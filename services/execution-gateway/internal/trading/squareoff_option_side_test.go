package trading

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

type sqfInsertedOrderForTest struct {
	IntentID string
	Side    string
	Qty     int64
	Raw     string
}

func loadSQFOrdersForTest(t *testing.T, db *sql.DB, tradeUID string) []sqfInsertedOrderForTest {
	t.Helper()

	rows, err := db.Query(`
		SELECT
			COALESCE(intent_id, ''),
			COALESCE(side, ''),
			COALESCE(quantity, 0),
			COALESCE(raw_broker_request::text, '')
		FROM orders
		WHERE trade_uid = $1
		  AND (
			UPPER(COALESCE(intent_id, '')) LIKE 'SCE_%'
			OR UPPER(COALESCE(intent_id, '')) LIKE 'SPE_%'
			OR UPPER(COALESCE(intent_id, '')) LIKE 'SQF_%'
			OR UPPER(COALESCE(order_uid, '')) LIKE 'SCE_%'
			OR UPPER(COALESCE(order_uid, '')) LIKE 'SPE_%'
			OR UPPER(COALESCE(order_uid, '')) LIKE 'SQF_%'
			OR UPPER(COALESCE(raw_broker_request::text, '')) LIKE '%"PHASE":"SQF"%'
			OR UPPER(COALESCE(raw_broker_request::text, '')) LIKE '%"PHASE": "SQF"%'
		  )
		ORDER BY id ASC
	`, tradeUID)
	if err != nil {
		t.Fatalf("load SQF orders failed: %v", err)
	}
	defer rows.Close()

	var out []sqfInsertedOrderForTest
	for rows.Next() {
		var row sqfInsertedOrderForTest
		if err := rows.Scan(&row.IntentID, &row.Side, &row.Qty, &row.Raw); err != nil {
			t.Fatalf("scan SQF row failed: %v", err)
		}
		out = append(out, row)
	}

	return out
}

func rawFieldStringForTest(t *testing.T, raw string, key string) string {
	t.Helper()

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("raw json parse failed: %v raw=%s", err, raw)
	}

	v, _ := m[key]
	return strings.ToUpper(strings.TrimSpace(v.(string)))
}

func rawFieldFloatForTest(t *testing.T, raw string, key string) float64 {
	t.Helper()

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("raw json parse failed: %v raw=%s", err, raw)
	}

	v, ok := m[key]
	if !ok {
		return 0
	}

	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	default:
		return 0
	}
}

func TestSquareOffCreatesCorrectOppositeOptionOrdersForShortStraddle(t *testing.T) {
	db := openIntegrationDB(t)
	defer db.Close()

	store := NewPostgresBackedStore(db)
	svc := &Service{Store: store}

	tradeUID := uniqueTradeUID("SQF_SIDE_SHORT")
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
		AvgFillPrice:  101.25,
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
		AvgFillPrice:  99.75,
	})

	if err := svc.SquareOff(tradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff failed: %v", err)
	}

	sqfOrders := loadSQFOrdersForTest(t, db, tradeUID)
	if len(sqfOrders) != 2 {
		t.Fatalf("expected 2 SQF orders, got %d: %+v", len(sqfOrders), sqfOrders)
	}

	seenCE := false
	seenPE := false

	for _, order := range sqfOrders {
		if strings.ToUpper(order.Side) != "BUY" {
			t.Fatalf("short straddle SQF must BUY back both legs, got side=%s raw=%s", order.Side, order.Raw)
		}

		if order.Qty != testLotSize {
			t.Fatalf("expected SQF qty %d, got %d raw=%s", testLotSize, order.Qty, order.Raw)
		}

		phase := rawFieldStringForTest(t, order.Raw, "phase")
		if phase != "SQF" {
			t.Fatalf("expected phase SQF, got %s raw=%s", phase, order.Raw)
		}

		legType := rawFieldStringForTest(t, order.Raw, "leg_type")
		token := int64(rawFieldFloatForTest(t, order.Raw, "token"))

		switch legType {
		case "CE":
			seenCE = true
			if token != 63939 {
				t.Fatalf("expected CE token 63939, got %d", token)
			}
		case "PE":
			seenPE = true
			if token != 63940 {
				t.Fatalf("expected PE token 63940, got %d", token)
			}
		default:
			t.Fatalf("unexpected SQF leg_type=%s raw=%s", legType, order.Raw)
		}
	}

	if !seenCE || !seenPE {
		t.Fatalf("expected both CE and PE SQF orders, seenCE=%v seenPE=%v", seenCE, seenPE)
	}

	tr, ok := store.LoadTrade(tradeUID)
	if !ok {
		t.Fatalf("trade missing after squareoff")
	}

	if tr.Status != "SQUARING_OFF" {
		t.Fatalf("expected trade status SQUARING_OFF, got %s", tr.Status)
	}
}

func TestSquareOffRejectsPendingFillTrade(t *testing.T) {
	db := openIntegrationDB(t)
	defer db.Close()

	store := NewPostgresBackedStore(db)
	svc := &Service{Store: store}

	tradeUID := uniqueTradeUID("SQF_REJECT_PENDING")
	cleanupTestTrade(t, db, tradeUID)
	defer cleanupTestTrade(t, db, tradeUID)

	insertDummyTrade(t, db, tradeUID, TradeStatusPendingFill)

	insertDummyOrder(t, db, dummyOrderInput{
		TradeUID:      tradeUID,
		IntentID:      "BUI_" + tradeUID + "_CE",
		OrderUID:      "BUI_" + tradeUID + "_CE",
		BrokerOrderID: "TEST_OPEN_CE",
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

	err := svc.SquareOff(tradeUID, "manual")
	if err == nil {
		t.Fatalf("expected SquareOff to reject PENDING_FILL trade")
	}

	sqfOrders := loadSQFOrdersForTest(t, db, tradeUID)
	if len(sqfOrders) != 0 {
		t.Fatalf("expected no SQF orders for PENDING_FILL trade, got %+v", sqfOrders)
	}
}
