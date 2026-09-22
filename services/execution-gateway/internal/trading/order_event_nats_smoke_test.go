package trading

import (
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"trading-platform/libs/contracts"
	"trading-platform/libs/go-common/events"
)

func TestOrderEventRegistryNATSSmoke(t *testing.T) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Skipf("NATS unavailable at %s: %v", url, err)
	}
	defer nc.Close()

	registry := NewOrderEventRegistry()
	sub, err := events.Subscribe[contracts.OrderUpdate](nc, events.TopicOrderUpdates, func(update contracts.OrderUpdate) {
		registry.Publish(update)
	}, func(err error) {
		t.Errorf("decode error: %v", err)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}

	sent := contracts.OrderUpdate{
		TradeID:       "NATS_SMOKE_TRADE",
		BrokerOrderID: "NATS_SMOKE_ORDER",
		Status:        "FILLED",
		FilledQty:     65,
		InternalTime:  time.Now(),
	}
	if err := events.Publish(nc, events.TopicOrderUpdates, sent); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush publish: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, ok := registry.Latest(sent.TradeID, sent.BrokerOrderID); ok {
			if got.Status != "FILLED" || got.FilledQty != 65 {
				t.Fatalf("unexpected update: %+v", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("NATS orders.update did not reach registry")
}
