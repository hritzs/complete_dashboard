package trading

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"trading-platform/libs/contracts"
)

// refusingHedgeExecutor: every order is REJECTED by the broker (RMS), or --
// silent -- never confirmed at all (the 19 Oct 12:15 restart window).
type refusingHedgeExecutor struct {
	mu       sync.Mutex
	registry *OrderEventRegistry
	silent   bool
	n        int
}

func (e *refusingHedgeExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	e.n++
	id := fmt.Sprintf("R%d", e.n)
	e.mu.Unlock()
	if !e.silent {
		e.registry.Publish(contracts.OrderUpdate{TradeID: in.TradeUID, BrokerOrderID: id, Status: "REJECTED"})
	}
	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}
func (e *refusingHedgeExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) {
	return nil, nil
}
func (e *refusingHedgeExecutor) ModifyOrderPrice(ctx context.Context, id string, p float64, q int64, lot int) error {
	return nil
}
func (e *refusingHedgeExecutor) CancelOrder(ctx context.Context, id string) error { return nil }

func runRefusalTranche(t *testing.T, silent bool) (int64, int64, bool, bool) {
	t.Helper()
	tr := newTestSquareOffTrade("TRD_HEDGE_REFUSED")
	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	exec := &refusingHedgeExecutor{registry: registry, silent: silent}
	svc := &Service{Store: NewMemoryStore(), OrderEvents: registry, Snapshot: fakeHedgeChainSnapshot{},
		freezeQtyBySymbol: map[string]int64{"NIFTY": 1755}}
	return svc.executeHedgeTranche(context.Background(), exec, exec, exec, true, tr, tr.TradeUID, 1,
		"BUY", "SELL", 111, 222, 65)
}

func TestHedgeTranche_BrokerRefusalIsConfirmed(t *testing.T) {
	ce, pe, complete, refused := runRefusalTranche(t, false)
	if ce != 0 || pe != 0 || complete || !refused {
		t.Fatalf("both legs REJECTED: got ce=%d pe=%d complete=%t refused=%t, want refused", ce, pe, complete, refused)
	}
}

func TestHedgeTranche_MissingConfirmationIsNotARefusal(t *testing.T) {
	_, _, complete, refused := runRefusalTranche(t, true)
	if complete || refused {
		t.Fatalf("no confirmation at all must never count as a refusal (got refused=%t)", refused)
	}
}
