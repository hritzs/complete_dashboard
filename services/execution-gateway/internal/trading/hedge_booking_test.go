package trading

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"trading-platform/libs/contracts"
)

func TestBookHedgeFills(t *testing.T) {
	cases := []struct {
		name             string
		ceQty, peQty     int
		ceSide, peSide   string
		filledCE, filled int64
		wantCE, wantPE   int
		wantErr          bool
	}{
		{"sell CE buy PE", 65, 65, "SELL", "BUY", 65, 65, 130, 0, false},
		{"buy CE sell PE", 65, 65, "BUY", "SELL", 65, 65, 0, 130, false},
		{"partial fill books only what filled", 65, 65, "BUY", "SELL", 65, 30, 0, 95, false},
		{"nothing filled books nothing", 65, 65, "BUY", "SELL", 0, 0, 65, 65, false},
		{"buy exceeding open short is refused", 0, 65, "BUY", "SELL", 65, 65, 0, 65, true},
		{"unknown side is refused", 65, 65, "HOLD", "SELL", 65, 65, 65, 65, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce, pe, err := bookHedgeFills(tc.ceQty, tc.peQty, tc.ceSide, tc.peSide, tc.filledCE, tc.filled)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if ce != tc.wantCE || pe != tc.wantPE {
				t.Fatalf("got CE/PE %d/%d, want %d/%d", ce, pe, tc.wantCE, tc.wantPE)
			}
		})
	}
}

// fakeHedgeChainSnapshot gives live bid/ask for TRD_HEDGE_*'s tokens
// (111 CE, 222 PE, matching newTestSquareOffTrade) so bidAskLimitPrice has
// something to price a hedge LIMIT order off -- without it every hedge
// order would be skipped as "no live quote".
type fakeHedgeChainSnapshot struct{}

func (fakeHedgeChainSnapshot) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	return &OptionChainSnapshot{
		Symbol: "NIFTY", ATM: 23400, Expiry: "22-SEP-26", LotSize: 65,
		SyntheticSpot: 23405, SyntheticFuture: 23405,
		Chain: []OptionChainRow{{
			Strike: 23400, IsATM: true, CEToken: 111, PEToken: 222,
			CELtp: 100, PELtp: 90, CEBid: 99.5, CEAsk: 100.5, PEBid: 89.5, PEAsk: 90.5,
		}},
	}, nil
}
func (fakeHedgeChainSnapshot) PushSnapshot(ctx context.Context, snap TradeSnapshot) error { return nil }

// liveHedgeExecutor fills every leg's LIMIT order in full on first
// submission (publishing a live FILLED event immediately, same timing
// model as liveConfirmingExecutor) and also implements OrderModifier/
// OrderCanceller (now required by ManualHedgeLots's modify-in-place
// tranche design) -- unused in the happy path since nothing needs a
// second attempt, but must be present or ManualHedgeLots refuses outright.
type liveHedgeExecutor struct {
	mu        sync.Mutex
	registry  *OrderEventRegistry
	submitted []OrderIntent
}

func (e *liveHedgeExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	id := "H" + string(rune('1'+len(e.submitted)))
	e.submitted = append(e.submitted, in)
	e.mu.Unlock()

	e.registry.Publish(contracts.OrderUpdate{
		TradeID: in.TradeUID, BrokerOrderID: id, Status: "FILLED", FilledQty: in.Quantity,
	})
	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (e *liveHedgeExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return nil, nil
}

func (e *liveHedgeExecutor) ModifyOrderPrice(ctx context.Context, brokerOrderID string, price float64, quantity int64, lotSize int) error {
	return nil
}

func (e *liveHedgeExecutor) CancelOrder(ctx context.Context, brokerOrderID string) error {
	return nil
}

var (
	_ Executor              = (*liveHedgeExecutor)(nil)
	_ VerifiedFillsProvider = (*liveHedgeExecutor)(nil)
	_ OrderModifier         = (*liveHedgeExecutor)(nil)
	_ OrderCanceller        = (*liveHedgeExecutor)(nil)
)

// tranchedHedgeExecutor simulates a hedge needing MORE than one tranche
// (i.e. the delta-neutralizing quantity exceeds the per-order max, 1755
// for NIFTY): the very first order ever submitted
// for each leg partial-fills to 60% with a non-terminal status (ACKED --
// still resting), forcing executeHedgeTranche to modify it; the modify
// then reports the SAME order's cumulative fill reaching its own full
// target (matching GreekSoft's real per-order-cumulative FilledQty
// semantics and ModifyOrderPrice's real contract of echoing quantity, not
// shrinking it -- see the comment on hedgeLegState). Every other order
// (tranche 2's fresh one) fills completely on first submission.
type tranchedHedgeExecutor struct {
	mu              sync.Mutex
	submitted       []OrderIntent
	modifyCalls     []string
	tradeUIDByOrder map[string]string
	firstOrderSeen  map[string]bool
	nextID          int
	registry        *OrderEventRegistry
}

func newTranchedHedgeExecutor(registry *OrderEventRegistry) *tranchedHedgeExecutor {
	return &tranchedHedgeExecutor{
		registry:        registry,
		tradeUIDByOrder: map[string]string{},
		firstOrderSeen:  map[string]bool{},
	}
}

func (e *tranchedHedgeExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	e.nextID++
	id := fmt.Sprintf("T%d", e.nextID)
	e.submitted = append(e.submitted, in)
	e.tradeUIDByOrder[id] = in.TradeUID
	isFirstForLeg := !e.firstOrderSeen[in.LegType]
	e.firstOrderSeen[in.LegType] = true
	e.mu.Unlock()

	if isFirstForLeg {
		partial := in.Quantity * 3 / 5 // 60%, still resting -- not a terminal status
		e.registry.Publish(contracts.OrderUpdate{TradeID: in.TradeUID, BrokerOrderID: id, Status: "ACKED", FilledQty: partial})
	} else {
		e.registry.Publish(contracts.OrderUpdate{TradeID: in.TradeUID, BrokerOrderID: id, Status: "FILLED", FilledQty: in.Quantity})
	}
	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (e *tranchedHedgeExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return nil, nil
}

func (e *tranchedHedgeExecutor) ModifyOrderPrice(ctx context.Context, brokerOrderID string, price float64, quantity int64, lotSize int) error {
	e.mu.Lock()
	e.modifyCalls = append(e.modifyCalls, brokerOrderID)
	tradeUID := e.tradeUIDByOrder[brokerOrderID]
	e.mu.Unlock()
	// Completes at the order's own echoed target -- proves the caller
	// passed the FIXED original quantity, not a shrunk "remaining".
	e.registry.Publish(contracts.OrderUpdate{TradeID: tradeUID, BrokerOrderID: brokerOrderID, Status: "FILLED", FilledQty: quantity})
	return nil
}

func (e *tranchedHedgeExecutor) CancelOrder(ctx context.Context, brokerOrderID string) error {
	return nil
}

var (
	_ Executor              = (*tranchedHedgeExecutor)(nil)
	_ VerifiedFillsProvider = (*tranchedHedgeExecutor)(nil)
	_ OrderModifier         = (*tranchedHedgeExecutor)(nil)
	_ OrderCanceller        = (*tranchedHedgeExecutor)(nil)
)

// TestManualHedgeLots_SplitsAcrossMultipleTranchesWhenAboveFreezeLimit
// proves the actual feature requested: a hedge whose full delta-
// neutralizing quantity exceeds the exchange freeze-quantity ceiling
// (NIFTY per-order max 1755) must still execute IN FULL, split across
// sequential tranches, each capped at the ceiling -- not silently
// truncated to one tranche and left under-hedged. Uses the exact numbers
// from the request: 53 lots x 65 = 3445 total qty, tranche 1 capped at
// floor(1800/65)=27 lots=1755, tranche 2 gets the real remainder
// 3445-1755=1690.
func TestManualHedgeLots_SplitsAcrossMultipleTranchesWhenAboveFreezeLimit(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_HEDGE_MULTITRANCHE")
	tr.CEQty, tr.PEQty, tr.Lots = 4000, 4000, 53
	store := NewMemoryStore()
	store.SaveTrade(tr)
	store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, NetDelta: -3500}) // negative -> CE=BUY, PE=SELL

	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := newTranchedHedgeExecutor(registry)
	svc := &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: exec},
		OrderEvents:   registry,
		Snapshot:      fakeHedgeChainSnapshot{},
		// What PreloadFreezeQty stores for NIFTY from the live contract
		// (freezQty 1801, lot 65 -> 27 lots). Without it, orders go 1 lot
		// at a time (no hardcoded fallback).
		freezeQtyBySymbol: map[string]int64{"NIFTY": 1755},
	}

	if err := svc.ManualHedgeLots(context.Background(), tr.TradeUID, 53); err != nil {
		t.Fatalf("ManualHedgeLots: %v", err)
	}

	var freshCE, freshPE []OrderIntent
	for _, in := range exec.submitted {
		switch in.LegType {
		case "CE":
			freshCE = append(freshCE, in)
		case "PE":
			freshPE = append(freshPE, in)
		}
	}
	if len(freshCE) != 2 || len(freshPE) != 2 {
		t.Fatalf("fresh orders per leg = CE:%d PE:%d, want 2 and 2 (one per tranche, no duplicate order while tranche 1's was still resting)", len(freshCE), len(freshPE))
	}
	if freshCE[0].Quantity != 1755 || freshPE[0].Quantity != 1755 {
		t.Fatalf("tranche 1 quantity = CE:%d PE:%d, want 1755/1755", freshCE[0].Quantity, freshPE[0].Quantity)
	}
	if freshCE[1].Quantity != 1690 || freshPE[1].Quantity != 1690 {
		t.Fatalf("tranche 2 quantity = CE:%d PE:%d, want 1690/1690 (the real remainder, not the full 3445 again)", freshCE[1].Quantity, freshPE[1].Quantity)
	}
	if len(exec.modifyCalls) < 2 {
		t.Fatalf("modify calls = %d, want at least 2 (one per leg, completing tranche 1's partial fill via re-price, not a duplicate order)", len(exec.modifyCalls))
	}

	got, ok := store.LoadTrade(tr.TradeUID)
	if !ok {
		t.Fatalf("trade not found after hedge")
	}
	// CE side BUY: 4000 - 3445 = 555. PE side SELL: 4000 + 3445 = 7445.
	if got.CEQty != 555 || got.PEQty != 7445 {
		t.Fatalf("CE/PE after hedge = %d/%d, want 555/7445 (the full 3445 qty applied across both tranches)", got.CEQty, got.PEQty)
	}
}

func newHedgeTestService(tr StoredTrade, netDelta float64) (*Service, *MemoryStore, *liveHedgeExecutor) {
	store := NewMemoryStore()
	store.SaveTrade(tr)
	store.SaveSnapshot(TradeSnapshot{TradeUID: tr.TradeUID, NetDelta: netDelta})
	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := &liveHedgeExecutor{registry: registry}
	return &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: exec},
		OrderEvents:   registry,
		Snapshot:      fakeHedgeChainSnapshot{},
		// What startup loads for NIFTY from the live contract (freezQty
		// 1801, lot 65 -> 27 lots = 1755). Without it every order falls
		// back to 1 lot.
		freezeQtyBySymbol: map[string]int64{"NIFTY": 1755},
	}, store, exec
}

func TestManualHedge_BooksVerifiedFillsAndExitClosesEverything(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_HEDGE_ROUNDTRIP")
	svc, store, exec := newHedgeTestService(tr, -65) // exactly one lot

	if err := svc.ManualHedge(context.Background(), tr.TradeUID); err != nil {
		t.Fatalf("ManualHedge: %v", err)
	}

	if len(exec.submitted) != 2 {
		t.Fatalf("placed %d orders, want 2", len(exec.submitted))
	}
	for _, in := range exec.submitted {
		if in.OrderType != "LIMIT" {
			t.Fatalf("hedge leg %s order type %q, want LIMIT (modify-in-place tranche design)", in.LegType, in.OrderType)
		}
		if in.LimitPrice == nil || *in.LimitPrice <= 0 {
			t.Fatalf("hedge leg %s has no live-bid/ask-derived limit price", in.LegType)
		}
		if in.Phase != "HEDGE" || in.HedgeGroupID == "" {
			t.Fatalf("hedge leg %s phase=%q group=%q", in.LegType, in.Phase, in.HedgeGroupID)
		}
		if in.Token != tr.CEToken && in.Token != tr.PEToken {
			t.Fatalf("hedge traded token %d, not one of the trade's own tokens", in.Token)
		}
	}

	got, _ := store.LoadTrade(tr.TradeUID)
	// negative delta: BUY CE (65 -> 0), SELL PE (65 -> 130)
	if got.CEQty != 0 || got.PEQty != 130 {
		t.Fatalf("after hedge CE/PE = %d/%d, want 0/130", got.CEQty, got.PEQty)
	}
	if got.Status != "ACTIVE" {
		t.Fatalf("status = %s, want ACTIVE", got.Status)
	}

	hedgeOrders := len(exec.submitted)
	if err := svc.SquareOff(tr.TradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff after hedge: %v", err)
	}

	closed, _ := store.LoadTrade(tr.TradeUID)
	if closed.CEQty != 0 || closed.PEQty != 0 || closed.Status != "CLOSEDSQF" {
		t.Fatalf("after exit CE/PE=%d/%d status=%s, want 0/0 CLOSEDSQF", closed.CEQty, closed.PEQty, closed.Status)
	}

	var boughtPE int64
	for _, in := range exec.submitted[hedgeOrders:] {
		if in.Token == tr.PEToken && in.Side == "BUY" {
			boughtPE += in.Quantity
		}
	}
	if boughtPE != 130 {
		t.Fatalf("exit bought back %d PE, want 130 (the original 65 plus the 65 the hedge added)", boughtPE)
	}
}

// TestManualHedgeExecute_TradeScopedBooksIntoTradeUsingItsOwnTokens is a
// regression test for a real, confirmed-live incident (2026-09-25): the
// Testing tab's "EXECUTE HEDGE" button traded whatever the CURRENT live
// ATM row's tokens were (since the caller supplied no ce_token/pe_token,
// exactly like the real UI request that triggered this), which can be a
// different strike than the trade's own once spot drifts -- and never
// booked the fill into the trade at all, so a later SquareOff had no idea
// the hedge existed and left it open as a naked, untracked position that
// had to be closed by hand. ManualHedgeExecute now delegates its
// trade-scoped case to ManualHedgeLots, which always trades the trade's
// own tokens and always books the verified fill.
func TestManualHedgeExecute_TradeScopedBooksIntoTradeUsingItsOwnTokens(t *testing.T) {
	tr := newTestSquareOffTrade("TRD_HEDGE_EXECUTE_ROUNDTRIP")
	// Live (snapshot) delta: exactly one lot short -> one lot long synthetic.
	svc, store, exec := newHedgeTestService(tr, -65)

	// No CEToken/PEToken in the request, matching the real UI call. The
	// request's net_delta is deliberately stale and of the OPPOSITE sign
	// (the portfolio row sends the build-time StoredTrade.NetDelta, which
	// is never updated): Hedge Now must size and direct the hedge from the
	// server's own live snapshot delta, never from this.
	resp, err := svc.ManualHedgeExecute(context.Background(), ManualHedgeTestRequest{
		TradeUID: tr.TradeUID,
		NetDelta: +500,
	})
	if err != nil {
		t.Fatalf("ManualHedgeExecute: %v", err)
	}
	if !resp.Success {
		t.Fatalf("ManualHedgeExecute reported failure: %s", resp.Error)
	}

	if len(exec.submitted) != 2 {
		t.Fatalf("placed %d orders, want 2", len(exec.submitted))
	}
	for _, in := range exec.submitted {
		if in.Token != tr.CEToken && in.Token != tr.PEToken {
			t.Fatalf("hedge traded token %d, not one of the trade's own tokens (%d/%d)", in.Token, tr.CEToken, tr.PEToken)
		}
	}

	got, _ := store.LoadTrade(tr.TradeUID)
	// negative delta: BUY CE (65 -> 0), SELL PE (65 -> 130) -- same
	// booking ManualHedge itself produces, proving the fill was actually
	// recorded on the trade this time.
	if got.CEQty != 0 || got.PEQty != 130 {
		t.Fatalf("after hedge CE/PE = %d/%d, want 0/130 (fill must be booked into the trade)", got.CEQty, got.PEQty)
	}

	if err := svc.SquareOff(tr.TradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff after hedge: %v", err)
	}
	closed, _ := store.LoadTrade(tr.TradeUID)
	if closed.CEQty != 0 || closed.PEQty != 0 || closed.Status != "CLOSEDSQF" {
		t.Fatalf("after exit CE/PE=%d/%d status=%s, want 0/0 CLOSEDSQF -- the hedge must not be left open", closed.CEQty, closed.PEQty, closed.Status)
	}
}

func TestManualHedge_RefusalsPlaceNothing(t *testing.T) {
	t.Run("non-ACTIVE trade", func(t *testing.T) {
		tr := newTestSquareOffTrade("TRD_HEDGE_PARTIAL")
		tr.Status = "PARTIAL"
		svc, _, exec := newHedgeTestService(tr, -65)
		if err := svc.ManualHedge(context.Background(), tr.TradeUID); err == nil {
			t.Fatal("want error for non-ACTIVE trade")
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("placed %d orders on a refused hedge", len(exec.submitted))
		}
	})

	t.Run("delta under one lot is a no-op", func(t *testing.T) {
		// 30 < 65: rounding down to whole lots gives 0, so nothing is placed.
		tr := newTestSquareOffTrade("TRD_HEDGE_SMALLDELTA")
		svc, _, exec := newHedgeTestService(tr, -30)
		lots, err := svc.ManualHedgeNow(context.Background(), tr.TradeUID)
		if err != nil || lots != 0 {
			t.Fatalf("want nil no-op with 0 lots, got lots=%d err=%v", lots, err)
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("placed %d orders for a sub-one-lot delta", len(exec.submitted))
		}
	})

	t.Run("BUY leg larger than open short", func(t *testing.T) {
		tr := newTestSquareOffTrade("TRD_HEDGE_NOSHORT")
		tr.CEQty = 0 // negative delta would BUY CE, but there is no short CE to buy back
		svc, store, exec := newHedgeTestService(tr, -65)
		if err := svc.ManualHedge(context.Background(), tr.TradeUID); err == nil {
			t.Fatal("want refusal")
		}
		if len(exec.submitted) != 0 {
			t.Fatalf("placed %d orders on a refused hedge", len(exec.submitted))
		}
		got, _ := store.LoadTrade(tr.TradeUID)
		if got.CEQty != 0 || got.PEQty != 65 {
			t.Fatalf("refused hedge changed quantities to %d/%d", got.CEQty, got.PEQty)
		}
	})
}
