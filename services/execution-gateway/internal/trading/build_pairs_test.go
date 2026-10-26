package trading

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// pairExecutor records the order of submissions and how many were in
// flight at once (each call holds briefly so overlapping calls show).
type pairExecutor struct {
	mu                sync.Mutex
	inFlight, maxSeen int
	sent              []OrderIntent
}

func (e *pairExecutor) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	e.mu.Lock()
	e.inFlight++
	e.maxSeen = max(e.maxSeen, e.inFlight)
	e.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	e.mu.Lock()
	e.inFlight--
	e.sent = append(e.sent, in)
	id := fmt.Sprintf("P%d", len(e.sent))
	e.mu.Unlock()
	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}

func (e *pairExecutor) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) { return nil, nil }

// The build sends a CE and a PE together, never more than two in flight,
// every order exactly as planned: 2026-10-26's 178 CE / 206 PE lots at 2
// lots per order, the ratio fixed before the first order.
func TestExecuteBuild_SendsPlanTwoAtATime(t *testing.T) {
	legs := []LegData{
		{Token: 51330, Symbol: "NIFTY", OptionType: "CE", Action: "SELL", TotalLots: 178, LotSize: 65, ExpectedPrice: 143.75},
		{Token: 51331, Symbol: "NIFTY", OptionType: "PE", Action: "SELL", TotalLots: 206, LotSize: 65, ExpectedPrice: 124.50},
	}
	chunks, err := GenerateChunkedOrders("BUI_T", legs, 192, 3510, 2)
	if err != nil {
		t.Fatal(err)
	}
	var plan []ExecOrder
	for _, c := range chunks {
		plan = append(plan, c...)
	}
	exec := &pairExecutor{}
	svc := &Service{Store: persistingStore{NewMemoryStore()}, OrderEvents: NewOrderEventRegistry(), buildTiming: fastTiming}
	trade := StoredTrade{TradeUID: "T-PAIRS", BrokerName: "GREEKSOFT", AccountID: "147", LotSize: 65, CEQty: 178 * 65, PEQty: 206 * 65}
	if _, err := svc.executeBuild(context.Background(), exec, trade, chunks); err != nil {
		t.Fatal(err)
	}
	if exec.maxSeen != 2 {
		t.Fatalf("max orders in flight %d, want 2", exec.maxSeen)
	}
	if len(exec.sent) != len(plan) {
		t.Fatalf("sent %d of %d planned orders", len(exec.sent), len(plan))
	}
	var ce, pe int64
	for i := 0; i < len(plan); i += 2 { // each pair is the plan's next two, in either finishing order
		want := map[string]bool{plan[i].UID: true}
		if i+1 < len(plan) {
			want[plan[i+1].UID] = true
		}
		for j := i; j < min(i+2, len(plan)); j++ {
			if !want[exec.sent[j].IntentID] {
				t.Fatalf("order %d (%s) is not one of the plan's pair %v", j, exec.sent[j].IntentID, want)
			}
			if exec.sent[j].LegType == "CE" {
				ce += exec.sent[j].Quantity
			} else {
				pe += exec.sent[j].Quantity
			}
		}
	}
	if ce != 178*65 || pe != 206*65 {
		t.Fatalf("sent CE %d PE %d, want %d / %d", ce, pe, 178*65, 206*65)
	}
	pairs, mixed := 0, 0
	for i := 0; i+1 < len(plan); i += 2 {
		pairs++
		if plan[i].OptionType != plan[i+1].OptionType {
			mixed++
		}
	}
	t.Logf("%d orders in %d rounds; %d of %d full rounds are one CE + one PE", len(plan), (len(plan)+1)/2, mixed, pairs)
}
