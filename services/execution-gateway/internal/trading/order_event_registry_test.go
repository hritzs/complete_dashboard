package trading

import (
	"context"
	"testing"
	"time"
	"trading-platform/libs/contracts"
)

func TestOrderEventRegistryHealth(t *testing.T) {
	r := NewOrderEventRegistry()

	if r.Healthy() {
		t.Fatal("new registry unexpectedly healthy")
	}

	r.SetHealthy(true)
	if !r.Healthy() {
		t.Fatal("registry did not become healthy")
	}

	r.SetHealthy(false)
	if r.Healthy() {
		t.Fatal("registry did not become unhealthy")
	}
}

func TestOrderEventRegistryEventBeforeWaiter(t *testing.T) {
	r := NewOrderEventRegistry()
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "FILLED", FilledQty: 65, InternalTime: time.Now()})
	started := time.Now()
	u, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, time.Second)
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if u.Status != "FILLED" || u.FilledQty != 65 {
		t.Fatalf("unexpected update: %+v", u)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatalf("cached terminal event did not return immediately")
	}
}

func TestOrderEventRegistryAckThenFill(t *testing.T) {
	r := NewOrderEventRegistry()
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "ACKED", InternalTime: time.Now()})
	go func() {
		time.Sleep(20 * time.Millisecond)
		r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "FILLED", FilledQty: 65, InternalTime: time.Now()})
	}()
	u, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, time.Second)
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if u.Status != "FILLED" {
		t.Fatalf("status=%s want FILLED", u.Status)
	}
}

func TestOrderEventRegistryPartialDoesNotRelease(t *testing.T) {
	r := NewOrderEventRegistry()
	go func() {
		time.Sleep(10 * time.Millisecond)
		r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "PARTIAL_FILL", FilledQty: 20, InternalTime: time.Now()})
		time.Sleep(20 * time.Millisecond)
		r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "FILLED", FilledQty: 65, InternalTime: time.Now()})
	}()
	u, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, time.Second)
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if u.FilledQty != 65 {
		t.Fatalf("filled=%d want 65", u.FilledQty)
	}
}

func TestOrderEventRegistryRejectedIsTerminal(t *testing.T) {
	r := NewOrderEventRegistry()
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "REJECTED", ReasonText: "RMS Rejected", InternalTime: time.Now()})
	u, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, time.Second)
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if u.Status != "REJECTED" {
		t.Fatalf("status=%s want REJECTED", u.Status)
	}
}

func TestOrderEventRegistryWrongTradeCannotRelease(t *testing.T) {
	r := NewOrderEventRegistry()
	r.Publish(contracts.OrderUpdate{TradeID: "OTHER", BrokerOrderID: "O1", Status: "FILLED", FilledQty: 65, InternalTime: time.Now()})
	_, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func TestOrderEventRegistryTimeoutReturnsLatest(t *testing.T) {
	r := NewOrderEventRegistry()
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "ACKED", FilledQty: 0, InternalTime: time.Now()})
	u, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if u.Status != "ACKED" {
		t.Fatalf("latest status=%s want ACKED", u.Status)
	}
}

func TestOrderEventRegistryOlderAckCannotOverwriteFill(t *testing.T) {
	r := NewOrderEventRegistry()
	now := time.Now()
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "FILLED", FilledQty: 65, InternalTime: now})
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "ACKED", FilledQty: 0, InternalTime: now.Add(time.Second)})
	u, ok := r.Latest("T1", "O1")
	if !ok {
		t.Fatal("missing latest update")
	}
	if u.Status != "FILLED" || u.FilledQty != 65 {
		t.Fatalf("terminal state regressed: %+v", u)
	}
}

func TestOrderEventRegistryDesiredQtyMakesTerminal(t *testing.T) {
	r := NewOrderEventRegistry()
	r.Publish(contracts.OrderUpdate{TradeID: "T1", BrokerOrderID: "O1", Status: "PARTIAL_FILL", FilledQty: 65, InternalTime: time.Now()})
	u, err := r.WaitTerminal(context.Background(), "T1", "O1", 65, time.Second)
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if u.FilledQty != 65 {
		t.Fatalf("filled=%d want 65", u.FilledQty)
	}
}
