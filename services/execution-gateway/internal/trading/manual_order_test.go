package trading

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestManualOrderChunks(t *testing.T) {
	cases := []struct {
		total, perOrder int
		want            []int
	}{
		{5, 0, []int{5}},       // no chunking requested -> one order
		{5, 10, []int{5}},      // chunk size >= total -> one order
		{5, 2, []int{2, 2, 1}}, // uneven split, last chunk smaller
		{6, 2, []int{2, 2, 2}}, // even split
		{1, 1, []int{1}},
	}
	for _, c := range cases {
		got := manualOrderChunks(c.total, c.perOrder)
		if len(got) != len(c.want) {
			t.Fatalf("chunks(%d,%d) = %v, want %v", c.total, c.perOrder, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("chunks(%d,%d) = %v, want %v", c.total, c.perOrder, got, c.want)
			}
		}
	}
}

func TestManualOrderRequest_Validate(t *testing.T) {
	base := ManualOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Token: 56985,
		Side: "SELL", OrderType: "LIMIT", Price: 100, TotalLots: 1, LotSize: 65,
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	bad := base
	bad.Token = 0
	if err := bad.validate(); err == nil {
		t.Fatal("missing token must be rejected")
	}

	bad = base
	bad.Side = "HOLD"
	if err := bad.validate(); err == nil {
		t.Fatal("invalid side must be rejected")
	}

	bad = base
	bad.OrderType = "STOP"
	if err := bad.validate(); err == nil {
		t.Fatal("invalid order_type must be rejected")
	}

	bad = base
	bad.OrderType, bad.Price = "LIMIT", 0
	if err := bad.validate(); err == nil {
		t.Fatal("LIMIT with no price must be rejected")
	}

	ok := base
	ok.OrderType, ok.Price = "MARKET", 0
	if err := ok.validate(); err != nil {
		t.Fatalf("MARKET with no price should be fine: %v", err)
	}
}

// recordingExecutor is a minimal Executor + VerifiedFillsProvider that
// records every submitted intent and reports it filled, scoped to this
// test file (not the SquareOff-focused fakeSLExecutor).
type recordingExecutor struct {
	mu        sync.Mutex
	submitted []OrderIntent
	failNth   int // 1-based; 0 means never fail
	fillPrice float64
}

func (f *recordingExecutor) ExecuteOrderIntent(ctx context.Context, intent OrderIntent) (*ExecutionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, intent)
	if f.failNth > 0 && len(f.submitted) == f.failNth {
		return nil, errors.New("broker rejected the order")
	}
	return &ExecutionResult{IntentID: intent.IntentID, BrokerOrderID: "BRK-" + intent.IntentID, Status: "SUBMITTED"}, nil
}

func (f *recordingExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	price := f.fillPrice
	if price == 0 {
		price = 100
	}
	fills := make([]BrokerFill, 0, len(f.submitted))
	for _, in := range f.submitted {
		fills = append(fills, BrokerFill{
			BrokerOrderID: "BRK-" + in.IntentID, Token: in.Token, Side: in.Side,
			FilledQty: in.Quantity, AveragePrice: price, Status: "FILLED", Verified: true,
		})
	}
	return fills, nil
}

var _ Executor = (*recordingExecutor)(nil)
var _ VerifiedFillsProvider = (*recordingExecutor)(nil)

// modifyCanceExecutor adds OrderModifier/OrderCanceller on top of
// recordingExecutor, to test ManualModifyOrder against.
type modifyCancelExecutor struct {
	recordingExecutor
	modifyCalls []float64
	modifyErr   error
}

func (m *modifyCancelExecutor) ModifyOrderPrice(ctx context.Context, brokerOrderID string, price float64, quantity int64, lotSize int) error {
	if m.modifyErr != nil {
		return m.modifyErr
	}
	m.modifyCalls = append(m.modifyCalls, price)
	return nil
}

var _ OrderModifier = (*modifyCancelExecutor)(nil)

func handlersWithExecutor(exec Executor) *Handlers {
	store := NewMemoryStore()
	svc := &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}}
	return NewHandlers(svc, store)
}

func postJSON(t *testing.T, h http.HandlerFunc, body interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(b))))
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// The bug this whole feature replaces: the UI's "SELL LIVE ATM" button
// showed a real-money confirmation dialog and called this exact endpoint,
// which silently logged and placed nothing. This proves a real order now
// reaches the executor.
func TestManualOrder_PlacesARealOrderAndReportsVerifiedFill(t *testing.T) {
	exec := &recordingExecutor{fillPrice: 117.25}
	h := handlersWithExecutor(exec)

	rec, out := postJSON(t, h.ManualOrder, ManualOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Token: 56985,
		Side: "SELL", OrderType: "LIMIT", Price: 117.25, TotalLots: 1, LotSize: 65,
	})
	if rec.Code != http.StatusOK || out["success"] != true {
		t.Fatalf("code=%d body=%v", rec.Code, out)
	}
	if len(exec.submitted) != 1 {
		t.Fatalf("submitted %d orders, want 1", len(exec.submitted))
	}
	in := exec.submitted[0]
	if in.Token != 56985 || in.Side != "SELL" || in.Quantity != 65 || in.OrderType != "LIMIT" {
		t.Fatalf("unexpected intent: %+v", in)
	}
	if in.LimitPrice == nil || *in.LimitPrice != 117.25 {
		t.Fatalf("limit price not passed through: %+v", in.LimitPrice)
	}

	legs, ok := out["legs"].([]interface{})
	if !ok || len(legs) != 1 {
		t.Fatalf("legs = %v", out["legs"])
	}
	leg := legs[0].(map[string]interface{})
	if leg["broker_order_id"] != "BRK-"+in.IntentID {
		t.Fatalf("leg = %v", leg)
	}
	if leg["verified_qty"].(float64) != 65 || leg["verified_price"].(float64) != 117.25 {
		t.Fatalf("verified fill not reported: %v", leg)
	}
}

func TestManualOrder_ChunksIntoMultipleOrders(t *testing.T) {
	exec := &recordingExecutor{}
	h := handlersWithExecutor(exec)

	_, out := postJSON(t, h.ManualOrder, ManualOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Token: 56985,
		Side: "SELL", OrderType: "MARKET", TotalLots: 5, LotsPerOrder: 2, LotSize: 65,
	})
	if out["success"] != true {
		t.Fatalf("body=%v", out)
	}
	if len(exec.submitted) != 3 {
		t.Fatalf("submitted %d orders, want 3 (2+2+1 lots)", len(exec.submitted))
	}
	wantQty := []int64{130, 130, 65}
	for i, in := range exec.submitted {
		if in.Quantity != wantQty[i] {
			t.Fatalf("order %d qty = %d, want %d", i, in.Quantity, wantQty[i])
		}
	}
}

func TestManualOrder_OneLegFailingStillReportsTheOthers(t *testing.T) {
	exec := &recordingExecutor{failNth: 2}
	h := handlersWithExecutor(exec)

	_, out := postJSON(t, h.ManualOrder, ManualOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Token: 56985,
		Side: "SELL", OrderType: "MARKET", TotalLots: 3, LotsPerOrder: 1, LotSize: 65,
	})
	if out["success"] != false {
		t.Fatalf("expected success=false when a leg failed: %v", out)
	}
	legs := out["legs"].([]interface{})
	if len(legs) != 3 {
		t.Fatalf("want all 3 legs reported (2 placed, 1 failed), got %d", len(legs))
	}
	failed := legs[1].(map[string]interface{})
	if failed["error"] == nil || failed["error"] == "" {
		t.Fatalf("failed leg missing its error: %v", failed)
	}
}

func TestManualOrder_RejectsInvalidRequestWithoutCallingTheBroker(t *testing.T) {
	exec := &recordingExecutor{}
	h := handlersWithExecutor(exec)

	rec, out := postJSON(t, h.ManualOrder, ManualOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", // no token, no side
	})
	if rec.Code != http.StatusBadRequest || out["success"] == true {
		t.Fatalf("code=%d body=%v", rec.Code, out)
	}
	if len(exec.submitted) != 0 {
		t.Fatalf("broker was called despite an invalid request: %d orders", len(exec.submitted))
	}
}

func TestManualModifyOrder_RepricesViaOrderModifier(t *testing.T) {
	exec := &modifyCancelExecutor{}
	h := handlersWithExecutor(exec)

	rec, out := postJSON(t, h.ManualModifyOrder, ManualModifyOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", BrokerOrderID: "120000037",
		Price: 402.30, Quantity: 65, LotSize: 65,
	})
	if rec.Code != http.StatusOK || out["success"] != true {
		t.Fatalf("code=%d body=%v", rec.Code, out)
	}
	if len(exec.modifyCalls) != 1 || exec.modifyCalls[0] != 402.30 {
		t.Fatalf("ModifyOrderPrice calls = %v", exec.modifyCalls)
	}
}

func TestManualModifyOrder_RejectsMissingFieldsWithoutCallingTheBroker(t *testing.T) {
	exec := &modifyCancelExecutor{}
	h := handlersWithExecutor(exec)

	for _, req := range []ManualModifyOrderRequest{
		{BrokerName: "GREEKSOFT", AccountID: "147", Price: 100, Quantity: 65},         // no broker_order_id
		{BrokerName: "GREEKSOFT", AccountID: "147", BrokerOrderID: "X", Quantity: 65}, // no price
		{BrokerName: "GREEKSOFT", AccountID: "147", BrokerOrderID: "X", Price: 100},   // no quantity
	} {
		rec, out := postJSON(t, h.ManualModifyOrder, req)
		if rec.Code != http.StatusBadRequest || out["success"] == true {
			t.Fatalf("req=%+v code=%d body=%v", req, rec.Code, out)
		}
	}
	if len(exec.modifyCalls) != 0 {
		t.Fatalf("broker was called despite invalid requests: %v", exec.modifyCalls)
	}
}

func TestManualModifyOrder_UnsupportedBrokerReports501(t *testing.T) {
	exec := &recordingExecutor{} // no OrderModifier
	h := handlersWithExecutor(exec)

	rec, out := postJSON(t, h.ManualModifyOrder, ManualModifyOrderRequest{
		BrokerName: "GREEKSOFT", AccountID: "147", BrokerOrderID: "X", Price: 100, Quantity: 65,
	})
	if rec.Code != http.StatusNotImplemented || out["success"] == true {
		t.Fatalf("code=%d body=%v", rec.Code, out)
	}
}
