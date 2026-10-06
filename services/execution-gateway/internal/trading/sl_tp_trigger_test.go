package trading

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
	"trading-platform/libs/contracts"
)

func TestSlThresholdForTrade_UsesOriginalLotsNotLiveQuantity(t *testing.T) {
	trade := StoredTrade{
		Lots:    10,
		LotSize: 65,
		CEQty:   650, // matches 10 lots initially
		PEQty:   650,
		Config:  MonitorConfig{SLPointsPerLot: 30},
	}

	lotsBefore, thresholdBefore := slThresholdForTrade(trade)
	if lotsBefore != 10 {
		t.Fatalf("lotsBefore = %v, want 10 (trade.Lots)", lotsBefore)
	}
	wantThreshold := -30.0 * 10.0
	if thresholdBefore != wantThreshold {
		t.Fatalf("thresholdBefore = %v, want %v", thresholdBefore, wantThreshold)
	}

	// Simulate a partial square-off that shrinks live CE/PE quantity to
	// 40% of original -- the threshold must NOT shrink with it (that's
	// the whole point of fixing it to the original size).
	trade.CEQty = 260
	trade.PEQty = 260

	lotsAfter, thresholdAfter := slThresholdForTrade(trade)
	if lotsAfter != lotsBefore {
		t.Fatalf("lotsAfter = %v, want unchanged %v after a partial square-off shrunk live quantity", lotsAfter, lotsBefore)
	}
	if thresholdAfter != thresholdBefore {
		t.Fatalf("thresholdAfter = %v, want unchanged %v -- the SL threshold must not drift tighter as the position is reduced", thresholdAfter, thresholdBefore)
	}
}

func TestSlThresholdForTrade_FallsBackToLiveQuantityWhenLotsUnset(t *testing.T) {
	// An older trade row predating the Lots field (or one where it was
	// never populated) should still produce a sane threshold rather than
	// dividing by zero or returning a zero/undefined lot count.
	trade := StoredTrade{
		Lots:    0,
		LotSize: 65,
		CEQty:   650,
		PEQty:   650,
		Config:  MonitorConfig{SLPointsPerLot: 30},
	}

	lots, threshold := slThresholdForTrade(trade)
	wantLots := float64(650+650) / (2.0 * 65.0)
	if lots != wantLots {
		t.Fatalf("lots = %v, want %v (fallback to live CEQty/PEQty)", lots, wantLots)
	}
	wantThreshold := -30.0 * wantLots
	if threshold != wantThreshold {
		t.Fatalf("threshold = %v, want %v", threshold, wantThreshold)
	}
}

func almostEqual(a, b float64) bool {
	const epsilon = 1e-9
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}

func TestBpsOfSpotThreshold(t *testing.T) {
	// Cross-checked against MonitorConfig.SpotStopLossBps's own worked
	// example: "A 1-bps NIFTY stop at 24,400 is 2.44 PnL-per-straddle
	// points."
	if got := bpsOfSpotThreshold(24400, 1); !almostEqual(got, 2.44) {
		t.Fatalf("bpsOfSpotThreshold(24400, 1) = %v, want 2.44", got)
	}
	// SLPnLBpsOfSpot's own worked example (corrected from an earlier,
	// numerically wrong "338.8" in the doc comment): spot 24,200, 14bps.
	if got := bpsOfSpotThreshold(24200, 14); !almostEqual(got, 33.88) {
		t.Fatalf("bpsOfSpotThreshold(24200, 14) = %v, want 33.88", got)
	}
	if got := bpsOfSpotThreshold(0, 14); got != 0 {
		t.Fatalf("bpsOfSpotThreshold(0, 14) = %v, want 0", got)
	}
}

// fakeSLExecutor is a minimal Executor + VerifiedFillsProvider scoped
// narrowly to proving SquareOff's reason-to-final-status behavior -- not
// a general-purpose mock (the previous, unused MockExecutor was removed
// as dead code in the 2026-09-17 cleanup pass; this is a purpose-built
// replacement for exactly this test, not a resurrection). It assumes
// every submitted order fills immediately and completely.
type fakeSLExecutor struct {
	submitted []OrderIntent
}

func (f *fakeSLExecutor) ExecuteOrderIntent(ctx context.Context, intent OrderIntent) (*ExecutionResult, error) {
	f.submitted = append(f.submitted, intent)
	return &ExecutionResult{
		IntentID:      intent.IntentID,
		BrokerOrderID: "FAKE-" + intent.IntentID,
		Status:        "SUBMITTED",
	}, nil
}

func (f *fakeSLExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	fills := make([]BrokerFill, 0, len(f.submitted))
	for _, intent := range f.submitted {
		fills = append(fills, BrokerFill{
			BrokerOrderID: "FAKE-" + intent.IntentID,
			Token:         intent.Token,
			Side:          intent.Side,
			FilledQty:     intent.Quantity,
			AveragePrice:  100.0,
			Status:        "FILLED",
			Verified:      true,
			Source:        "FAKE_TEST_EXECUTOR",
		})
	}
	return fills, nil
}

var _ Executor = (*fakeSLExecutor)(nil)
var _ VerifiedFillsProvider = (*fakeSLExecutor)(nil)

type fakeBrokerFactory struct {
	executor Executor
}

func (f *fakeBrokerFactory) GetExecutor(userID, brokerName, accountID string) (Executor, error) {
	return f.executor, nil
}

var _ BrokerFactory = (*fakeBrokerFactory)(nil)

func newTestSquareOffTrade(tradeUID string) StoredTrade {
	return StoredTrade{
		TradeUID: tradeUID,
		Status:   "ACTIVE",
		Symbol:   "NIFTY",
		CEToken:  111,
		PEToken:  222,
		CEQty:    65,
		PEQty:    65,
		LotSize:  65,
		Lots:     1,
	}
}

// liveConfirmingExecutor simulates the real live-Iris-confirmation timing
// that exposed the double-square-off bug: each order is accepted
// synchronously (status SUBMITTED) and IMMEDIATELY confirmed FILLED via a
// live event publish to the registry (matching how fast real Iris pushes
// actually arrive, ~10-100ms per the live logs) -- but GetVerifiedFills
// (the reconciler-DB-backed polling path) deliberately returns nothing,
// simulating the DB write pipeline lagging behind under load. If SquareOff
// were still using the old polling-only verification, it would see "no
// verified fills yet" and submit a second, duplicate round of orders for
// the same already-filling quantity -- exactly what happened live
// 2026-09-23 on a 77-lot square-off (order count came out exactly 2x:
// 52 BUY CE against 26 SELL CE, 48 BUY PE against 24 SELL PE).
type liveConfirmingExecutor struct {
	mu        sync.Mutex
	registry  *OrderEventRegistry
	submitted []OrderIntent
}

func (e *liveConfirmingExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	id := "L" + string(rune('1'+len(e.submitted)))
	e.submitted = append(e.submitted, in)
	e.mu.Unlock()

	e.registry.Publish(contracts.OrderUpdate{
		TradeID: in.TradeUID, BrokerOrderID: id, Status: "FILLED", FilledQty: in.Quantity,
	})

	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (e *liveConfirmingExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return nil, nil // deliberately empty -- simulates the reconciler DB not having caught up yet
}

func (e *liveConfirmingExecutor) submittedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.submitted)
}

// TestSquareOff_UsesLiveConfirmationNotJustDBPolling proves the actual fix
// for the confirmed-live double-square-off: when the live OrderEventRegistry
// is healthy and has the real terminal confirmation for every submitted
// order, SquareOff must reach the target in exactly the orders that were
// actually needed -- never submitting a second round because a
// DB-polling check (which here returns nothing) looked incomplete.
func TestSquareOff_UsesLiveConfirmationNotJustDBPolling(t *testing.T) {
	store := NewMemoryStore()
	tradeUID := "TRD_TEST_LIVE_SQF"
	tr := newTestSquareOffTrade(tradeUID)
	tr.Lots = 2
	tr.CEQty = 130 // 2 lots
	tr.PEQty = 130
	store.SaveTrade(tr)

	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := &liveConfirmingExecutor{registry: registry}

	svc := &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: exec},
		OrderEvents:   registry,
	}

	if err := svc.SquareOff(tradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff returned error: %v", err)
	}

	tr2, ok := store.LoadTrade(tradeUID)
	if !ok {
		t.Fatalf("trade not found after SquareOff")
	}
	if tr2.Status != "CLOSEDSQF" {
		t.Fatalf("status = %q, want CLOSEDSQF", tr2.Status)
	}
	if tr2.CEQty != 0 || tr2.PEQty != 0 {
		t.Fatalf("CEQty/PEQty = %d/%d, want 0/0", tr2.CEQty, tr2.PEQty)
	}

	// 2 lots CE + 2 lots PE at one lot per order ("65-65" chunking) is 4
	// orders total. If the old polling-only path were still in use, the
	// empty GetVerifiedFills would make every attempt look incomplete,
	// and the retry loop would keep submitting more (up to
	// maxExecutionAttempts=4 full rounds -- 16 orders instead of 4).
	if got := exec.submittedCount(); got != 4 {
		t.Fatalf("orders submitted = %d, want exactly 4 (2 lots x 2 legs, no duplicate round from a stale/slow DB poll)", got)
	}
}

// delayedLiveExecutor simulates the REAL observed confirmation timing from
// the incident this fix addresses: querying latency_samples for the actual
// 77-lot trade (2026-09-23, trade_id=212) showed real iris_confirmation/
// iris_fill latency ranging 6.5ms-375ms across 324 samples (avg ~51-57ms).
// Each order here gets published as FILLED on its own goroutine after a
// randomized delay drawn from that real range, so many orders are
// concurrently "in flight" at once -- matching the real load (100+ BUY
// orders fired within ~8 seconds) that caused the DB-polling path to miss
// fills still landing. GetVerifiedFills is deliberately always empty, the
// worst case for the DB path, proving the live path alone is what carries
// this.
type delayedLiveExecutor struct {
	mu        sync.Mutex
	registry  *OrderEventRegistry
	submitted []OrderIntent
}

func (e *delayedLiveExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	id := fmt.Sprintf("D%d", len(e.submitted)+1)
	e.submitted = append(e.submitted, in)
	e.mu.Unlock()

	delayMs := 6 + rand.Intn(370) // 6ms-376ms, matching the real observed range
	go func() {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		e.registry.Publish(contracts.OrderUpdate{
			TradeID: in.TradeUID, BrokerOrderID: id, Status: "FILLED", FilledQty: in.Quantity,
		})
	}()

	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (e *delayedLiveExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return nil, nil
}

func (e *delayedLiveExecutor) submittedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.submitted)
}

// TestSquareOff_HandlesRealisticConcurrentLoadWithoutDuplicating replays
// the actual incident at realistic scale: a 20-lot square-off (40 orders
// across CE+PE at one lot each), each confirmed only via a delayed live
// event drawn from the real observed latency distribution, with the DB
// poll path providing nothing. Must still converge to exactly 40 orders,
// never a duplicate round from treating in-flight orders as failed.
func TestSquareOff_HandlesRealisticConcurrentLoadWithoutDuplicating(t *testing.T) {
	store := NewMemoryStore()
	tradeUID := "TRD_TEST_REALISTIC_LOAD_SQF"
	tr := newTestSquareOffTrade(tradeUID)
	tr.Lots = 20
	tr.CEQty = 1300 // 20 lots x 65
	tr.PEQty = 1300
	store.SaveTrade(tr)

	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := &delayedLiveExecutor{registry: registry}

	svc := &Service{
		Store:         store,
		BrokerFactory: &fakeBrokerFactory{executor: exec},
		OrderEvents:   registry,
	}

	if err := svc.SquareOff(tradeUID, "manual"); err != nil {
		t.Fatalf("SquareOff returned error: %v", err)
	}

	tr2, ok := store.LoadTrade(tradeUID)
	if !ok {
		t.Fatalf("trade not found after SquareOff")
	}
	if tr2.CEQty != 0 || tr2.PEQty != 0 {
		t.Fatalf("CEQty/PEQty = %d/%d, want 0/0", tr2.CEQty, tr2.PEQty)
	}
	if got := exec.submittedCount(); got != 40 {
		t.Fatalf("orders submitted = %d, want exactly 40 (20 lots x 2 legs) -- more means a duplicate round was fired under realistic concurrent load", got)
	}
}

// TestSquareOff_ReasonDeterminesFinalStatus proves the reason-to-final-
// status mapping added for the autonomous SL/TP-exit changes: a real,
// verified square-off closes with CLOSED_SL for reason="SL", CLOSED_TP
// for reason="TP", and the existing CLOSEDSQF for every other reason
// (e.g. a manual square-off) -- preserving the audit distinction between
// "closed because of a real stop-loss/take-profit" and "closed some
// other way" that a single shared terminal status would erase.
func TestSquareOff_ReasonDeterminesFinalStatus(t *testing.T) {
	cases := []struct {
		reason     string
		wantStatus string
	}{
		{reason: "SL", wantStatus: "CLOSED_SL"},
		{reason: "TP", wantStatus: "CLOSED_TP"},
		{reason: "TIME", wantStatus: "CLOSED_TIME"},
		{reason: "manual", wantStatus: "CLOSEDSQF"},
	}

	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			store := NewMemoryStore()
			tradeUID := "TRD_TEST_" + tc.reason
			store.SaveTrade(newTestSquareOffTrade(tradeUID))

			svc := &Service{
				Store:         store,
				BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}},
			}

			if err := svc.SquareOff(tradeUID, tc.reason); err != nil {
				t.Fatalf("SquareOff(reason=%q) returned error: %v", tc.reason, err)
			}

			tr, ok := store.LoadTrade(tradeUID)
			if !ok {
				t.Fatalf("trade %s not found after SquareOff", tradeUID)
			}
			if tr.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", tr.Status, tc.wantStatus)
			}
			if tr.CEQty != 0 || tr.PEQty != 0 {
				t.Fatalf("CEQty/PEQty = %d/%d, want 0/0 (fully verified exit)", tr.CEQty, tr.PEQty)
			}
		})
	}
}
