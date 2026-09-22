package trading

import (
	"context"
	"sync"
	"testing"
	"trading-platform/libs/contracts"
)

// rejectingExecutor accepts every order synchronously (status SUBMITTED,
// matching the real GreekSoft executor -- see executor.go's
// GREEKSOFT_ORDER_SENT_AWAITING_IRIS comment) and then immediately publishes
// a REJECTED event to the given registry, simulating the real-time
// Iris/NATS confirmation of a broker-side (e.g. RMS margin) rejection.
type rejectingExecutor struct {
	mu        sync.Mutex
	registry  *OrderEventRegistry
	submitted []string
}

func (e *rejectingExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	id := "B" + string(rune('1'+len(e.submitted)))
	e.submitted = append(e.submitted, id)
	e.mu.Unlock()

	e.registry.Publish(contracts.OrderUpdate{
		TradeID: in.TradeUID, BrokerOrderID: id, Status: "REJECTED", ReasonText: "RMS margin rejection (test)",
	})

	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (e *rejectingExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return nil, nil
}

// Confirmed live 2026-09-22: after the first RMS margin rejection,
// executeBuild kept submitting -- 56 more orders, all rejected -- instead of
// stopping. It must now give up after a small run of consecutive real
// rejections instead of marching through every remaining chunk.
func TestExecuteBuild_StopsAfterConsecutiveRejectionsInsteadOfSubmittingEveryChunk(t *testing.T) {
	store := NewMemoryStore()
	registry := NewOrderEventRegistry()
	exec := &rejectingExecutor{registry: registry}
	svc := &Service{Store: persistingStore{store}, OrderEvents: registry, buildTiming: fastTiming}

	trade := StoredTrade{TradeUID: "T-CB", BrokerName: "GREEKSOFT", AccountID: "147", LotSize: 65, CEQty: 650, PEQty: 0}

	var chunks [][]ExecOrder
	for i := 0; i < 10; i++ {
		chunks = append(chunks, []ExecOrder{{
			UID: "U", Token: 111, Symbol: "NIFTY", OptionType: "CE", Action: "SELL", Quantity: 65, ExpectedPrice: 50,
		}})
	}

	// executeBuild reports the trip via outcome.FirstError, the same field
	// DeployStraddle's status switch already keys off of (RECONCILIATION_
	// REQUIRED when FirstError != nil) -- it does not always return a
	// non-nil Go error itself (see the plain "return outcome, nil" at the
	// end of the function), so that is the field to check here too.
	outcome, err := svc.executeBuild(context.Background(), exec, trade, chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.FirstError == nil {
		t.Fatal("expected outcome.FirstError to report the circuit breaker trip")
	}

	exec.mu.Lock()
	submittedCount := len(exec.submitted)
	exec.mu.Unlock()

	if submittedCount >= len(chunks) {
		t.Fatalf("submitted %d of %d chunks, want the circuit breaker to stop well before all of them", submittedCount, len(chunks))
	}
	if submittedCount == 0 {
		t.Fatal("submitted 0 orders -- the breaker must let at least the threshold's worth through before tripping")
	}
	t.Logf("circuit breaker stopped after %d of %d chunks", submittedCount, len(chunks))
}

// Without a registry (nil OrderEvents, as in every test that doesn't wire
// one up), the circuit breaker must never fire -- WaitTerminal/Latest on a
// nil registry are safe no-ops, not a crash or a false trip.
func TestExecuteBuild_NoCircuitBreakerWithoutARegistry(t *testing.T) {
	store := NewMemoryStore()
	exec := &recordingExecutor{fillPrice: 50}
	svc := &Service{Store: persistingStore{store}, buildTiming: fastTiming}

	trade := StoredTrade{TradeUID: "T-NOREG", BrokerName: "GREEKSOFT", AccountID: "147", LotSize: 65, CEQty: 195, PEQty: 0}

	var chunks [][]ExecOrder
	for i := 0; i < 3; i++ {
		chunks = append(chunks, []ExecOrder{{
			UID: "U", Token: 111, Symbol: "NIFTY", OptionType: "CE", Action: "SELL", Quantity: 65, ExpectedPrice: 50,
		}})
	}

	if _, err := svc.executeBuild(context.Background(), exec, trade, chunks); err != nil {
		t.Fatalf("executeBuild: %v", err)
	}

	exec.mu.Lock()
	submittedCount := len(exec.submitted)
	exec.mu.Unlock()
	if submittedCount != len(chunks) {
		t.Fatalf("submitted %d of %d chunks, want all of them (no registry means no circuit breaker)", submittedCount, len(chunks))
	}
}
