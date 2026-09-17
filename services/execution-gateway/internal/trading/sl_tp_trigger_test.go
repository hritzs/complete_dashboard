package trading

import (
	"context"
	"testing"
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
