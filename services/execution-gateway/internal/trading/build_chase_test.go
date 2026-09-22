package trading

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// chaseBroker models an order book where a SELL limit fills only if its price
// is at or below the token's bid -- so a build priced from a stale LTP rests
// unfilled until it is re-priced, like the 2026-09-21 NOV PE.
type chaseBroker struct {
	mu        sync.Mutex
	bid       map[int64]float64
	orders    map[string]*chaseFakeOrder
	order     []string
	modifyErr error
	modifies  []float64
	cancels   []string
}

type chaseFakeOrder struct {
	intent    OrderIntent
	price     float64
	filled    int64
	cancelled bool
}

func newChaseBroker(ceBid, peBid float64) *chaseBroker {
	return &chaseBroker{bid: map[int64]float64{111: ceBid, 222: peBid}, orders: map[string]*chaseFakeOrder{}}
}

func (b *chaseBroker) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := fmt.Sprintf("B%d", len(b.order)+1)
	o := &chaseFakeOrder{intent: in}
	if in.LimitPrice != nil {
		o.price = *in.LimitPrice
	}
	if in.OrderType == "MARKET" || o.price <= b.bid[in.Token] {
		o.filled = in.Quantity
	}
	b.orders[id] = o
	b.order = append(b.order, id)
	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (b *chaseBroker) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []BrokerFill
	for _, id := range b.order {
		o := b.orders[id]
		if o.filled > 0 {
			out = append(out, BrokerFill{BrokerOrderID: id, Token: o.intent.Token, Side: o.intent.Side,
				FilledQty: o.filled, AveragePrice: o.price, Status: "FILLED", Verified: true, Source: "TEST"})
		}
	}
	return out, nil
}

func (b *chaseBroker) ModifyOrderPrice(ctx context.Context, id string, price float64, qty int64, lot int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.modifyErr != nil {
		return b.modifyErr
	}
	o := b.orders[id]
	o.price = price
	b.modifies = append(b.modifies, price)
	if !o.cancelled && o.filled == 0 && price <= b.bid[o.intent.Token] {
		o.filled = o.intent.Quantity
	}
	return nil
}

func (b *chaseBroker) CancelOrder(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.orders[id].cancelled = true
	b.cancels = append(b.cancels, id)
	return nil
}

// noModifyBroker exposes only submit + verify: no modify, no cancel.
type noModifyBroker struct{ inner *chaseBroker }

func (n noModifyBroker) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	return n.inner.ExecuteOrderIntent(ctx, in)
}
func (n noModifyBroker) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return n.inner.GetVerifiedFills(ctx)
}

// staleThenLiveChain returns a stale PE price on the first call (deploy) and
// the live one afterwards.
type staleThenLiveChain struct {
	mu    sync.Mutex
	calls int
}

func (c *staleThenLiveChain) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	c.mu.Lock()
	c.calls++
	first := c.calls == 1
	c.mu.Unlock()
	pe := 404.3
	if first {
		pe = 410.5
	}
	return &OptionChainSnapshot{
		Symbol: "NIFTY", ATM: 23650, Expiry: "23-NOV-26", LotSize: 65, SyntheticSpot: 23650, SyntheticFuture: 23650,
		Chain: []OptionChainRow{{Strike: 23650, IsATM: true, CEToken: 111, PEToken: 222,
			CELtp: 409.4, PELtp: pe, CEDelta: 0.5, PEDelta: -0.5}},
	}, nil
}
func (c *staleThenLiveChain) PushSnapshot(ctx context.Context, s TradeSnapshot) error { return nil }

var fastTiming = buildTiming{verifyAttempts: 2, verifyDelay: 5 * time.Millisecond, chaseRounds: 3, chaseVerifyAttempts: 2, chaseVerifyDelay: 5 * time.Millisecond}

// persistingStore is a MemoryStore that also accepts verified fills, as the
// Postgres store does; without it the build (correctly) refuses to call the
// trade fully verified.
type persistingStore struct{ *MemoryStore }

func (persistingStore) PersistVerifiedFills(ctx context.Context, broker, account string, fills []BrokerFill) (FillPersistenceReport, error) {
	return FillPersistenceReport{Processed: len(fills), Persisted: len(fills)}, nil
}

func deployWith(t *testing.T, exec Executor) (*Service, *MemoryStore, *DeployStraddleResponse) {
	t.Helper()
	store := NewMemoryStore()
	svc := &Service{Store: persistingStore{store}, BrokerFactory: &fakeBrokerFactory{executor: exec}, Snapshot: &staleThenLiveChain{}, buildTiming: fastTiming}
	resp, err := svc.DeployStraddle(context.Background(), DeployStraddleRequest{BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Lots: 1})
	if err != nil {
		t.Fatalf("DeployStraddle: %v", err)
	}
	t.Cleanup(func() {
		if rt, ok := store.LoadRuntime(resp.TradeUID); ok {
			close(rt.StopCh)
			<-rt.DoneCh
		}
	})
	return svc, store, resp
}

func TestBuildChase_FillsTheLeftoverByRepricingTheSameOrder(t *testing.T) {
	// CE fills at its first price. PE was priced 408.50 from a stale LTP, the
	// bid is 402: it fills only once the chase re-prices below it.
	b := newChaseBroker(1000, 402)
	_, store, resp := deployWith(t, b)

	if resp.Status != "ACTIVE" {
		t.Fatalf("status = %s, want ACTIVE after the chase filled the leftover", resp.Status)
	}
	if len(b.order) != 2 {
		t.Fatalf("%d orders exist, want exactly the original 2 -- a chase must re-price, never add an order that could double-fill", len(b.order))
	}
	want := []float64{402.3, 400.3} // live 404.30 - 2*round, on the 0.05 tick
	if len(b.modifies) != 2 || !almostEqual(b.modifies[0], want[0]) || !almostEqual(b.modifies[1], want[1]) {
		t.Fatalf("modifies = %v, want %v", b.modifies, want)
	}
	if len(b.cancels) != 0 {
		t.Fatalf("cancelled %v although everything filled", b.cancels)
	}
	tr, _ := store.LoadTrade(resp.TradeUID)
	if tr.CEQty != 65 || tr.PEQty != 65 {
		t.Fatalf("stored quantities %d/%d, want 65/65", tr.CEQty, tr.PEQty)
	}
}

// deployExpectBuildRatioFailure asserts the ratio-mismatch path leaves the
// trade record VISIBLE with the real verified quantities and a
// RECONCILIATION_REQUIRED status, rather than deleting it. Deleting it used
// to be the behavior here, but that erases the only record of a position
// that may genuinely still be open at the broker (confirmed live
// 2026-09-22: a lopsided build left a real naked CE position untracked for
// over two hours because its trade row -- and with it any chance of the
// platform ever surfacing it again -- would have been deleted).
func deployExpectBuildRatioFailure(t *testing.T, exec Executor) (*Service, *MemoryStore, error) {
	t.Helper()
	store := NewMemoryStore()
	svc := &Service{Store: persistingStore{store}, BrokerFactory: &fakeBrokerFactory{executor: exec}, Snapshot: &staleThenLiveChain{}, buildTiming: fastTiming}
	_, err := svc.DeployStraddle(context.Background(), DeployStraddleRequest{BrokerName: "GREEKSOFT", AccountID: "147", Symbol: "NIFTY", Lots: 1})
	if err == nil {
		t.Fatal("DeployStraddle: expected build ratio mismatch error")
	}
	if !strings.Contains(err.Error(), "build ratio mismatch") {
		t.Fatalf("DeployStraddle error = %v, want build ratio mismatch", err)
	}
	trades := store.AllTrades()
	if len(trades) != 1 {
		t.Fatalf("stored trades after mismatch = %d, want 1 (the real fill must stay visible)", len(trades))
	}
	if trades[0].Status != "RECONCILIATION_REQUIRED" {
		t.Fatalf("status = %s, want RECONCILIATION_REQUIRED", trades[0].Status)
	}
	if trades[0].CEQty != 65 || trades[0].PEQty != 0 {
		t.Fatalf("stored quantities %d/%d, want 65/0: record what filled, not what was intended", trades[0].CEQty, trades[0].PEQty)
	}
	return svc, store, err
}

func TestBuildChase_GivesUpAndCancelsWhatStillRests(t *testing.T) {
	b := newChaseBroker(1000, 300) // no realistic price fills the PE
	_, _, err := deployExpectBuildRatioFailure(t, b)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if len(b.order) != 2 {
		t.Fatalf("%d orders exist, want 2 (no new orders)", len(b.order))
	}
	if len(b.modifies) != 3 {
		t.Fatalf("modifies = %v, want 3 rounds", b.modifies)
	}
	if len(b.cancels) != 1 || b.orders[b.cancels[0]].intent.LegType != "PE" {
		t.Fatalf("cancels = %v, want exactly the unfilled PE order", b.cancels)
	}
}

func TestBuildChase_ModifyFailureStopsAndCancels(t *testing.T) {
	b := newChaseBroker(1000, 300)
	b.modifyErr = errors.New("broker said no")
	_, _, err := deployExpectBuildRatioFailure(t, b)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if len(b.modifies) != 0 || len(b.order) != 2 {
		t.Fatalf("modifies=%v orders=%d: a failed modify must not lead to any new order", b.modifies, len(b.order))
	}
	if len(b.cancels) != 1 {
		t.Fatalf("cancels = %v, want the resting PE cancelled so nothing works unmonitored", b.cancels)
	}
}

func TestBuildChase_ExecutorWithoutModifyIsLeftAsBefore(t *testing.T) {
	b := newChaseBroker(1000, 300)
	_, _, err := deployExpectBuildRatioFailure(t, noModifyBroker{b})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if len(b.modifies) != 0 || len(b.cancels) != 0 || len(b.order) != 2 {
		t.Fatalf("modifies=%v cancels=%v orders=%d", b.modifies, b.cancels, len(b.order))
	}
}

func TestChasePrice(t *testing.T) {
	cases := []struct {
		live, buf float64
		round     int
		want      float64
	}{
		{404.3, 2, 1, 402.3}, {404.3, 2, 2, 400.3}, {404.3, 2, 3, 398.3},
		{100, 2, 9, 90},    // never more than 10% below live
		{0.10, 2, 1, 0.10}, // tick floor path
	}
	for _, c := range cases {
		if got := chasePrice(c.live, c.buf, c.round); !almostEqual(got, c.want) {
			t.Fatalf("chasePrice(%v,%v,%d) = %v, want %v", c.live, c.buf, c.round, got, c.want)
		}
	}
}

// The 2026-09-21 incident: a trade whose PE never filled still said 65/65, so
// its square-off bought 65 PE that had never been sold.
type openQtyStore struct {
	*MemoryStore
	ce, pe int64
}

func (s *openQtyStore) TradeOpenQuantities(ctx context.Context, uid string) (int64, int64, error) {
	return s.ce, s.pe, nil
}

func TestSquareOff_PartialTradeUsesFilledOrdersNotIntendedQuantity(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_PARTIAL_PE_UNFILLED")
	tr.Status = "PARTIAL" // stored quantities still say CE 65 / PE 65
	store := &openQtyStore{MemoryStore: NewMemoryStore(), ce: 65, pe: 0}
	store.SaveTrade(tr)
	exec := &fakeSLExecutor{}
	svc := &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}}

	if err := svc.SquareOff(tr.TradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff: %v", err)
	}
	if len(exec.submitted) == 0 {
		t.Fatal("nothing placed; the open CE should have been bought back")
	}
	for _, in := range exec.submitted {
		if in.Token == tr.PEToken {
			t.Fatalf("square-off traded the PE (%+v) although no PE was ever sold", in)
		}
		if in.Side != "BUY" || in.Token != tr.CEToken {
			t.Fatalf("unexpected order %+v", in)
		}
	}
	got, _ := store.LoadTrade(tr.TradeUID)
	if got.Status != "CLOSEDSQF" {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestSquareOff_PartialTradeWithNothingFilledPlacesNothing(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_PARTIAL_NOTHING")
	tr.Status = "PARTIAL"
	store := &openQtyStore{MemoryStore: NewMemoryStore()}
	store.SaveTrade(tr)
	exec := &fakeSLExecutor{}
	svc := &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}}

	if err := svc.SquareOff(tr.TradeUID, "manual"); err == nil {
		t.Fatal("want an error: nothing is open")
	}
	if len(exec.submitted) != 0 {
		t.Fatalf("placed %d orders for a trade with nothing filled", len(exec.submitted))
	}
}

func TestOpenShortQuantities(t *testing.T) {
	ce, pe := openShortQuantities([]OrderExecution{
		{Leg: "CE", Side: "SELL", FilledQty: 65}, {Leg: "PE", Side: "SELL", FilledQty: 65},
		{Leg: "PE", Side: "SELL", FilledQty: 0},               // unfilled
		{Leg: "CE", Side: "BUY", FilledQty: 65},               // exit
		{Leg: "PE", Side: "BUY", FilledQty: 130, AvgPrice: 0}, // fills count even without a price
	})
	if ce != 0 || pe != 0 {
		t.Fatalf("open short CE/PE = %d/%d, want 0/0 (never negative)", ce, pe)
	}
	ce, pe = openShortQuantities([]OrderExecution{{Leg: "CE", Side: "SELL", FilledQty: 65}})
	if ce != 65 || pe != 0 {
		t.Fatalf("open short CE/PE = %d/%d, want 65/0", ce, pe)
	}
}
