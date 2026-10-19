package trading

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"trading-platform/libs/contracts"
)

// exitChain: own straddle 22950 (111/222) and an ATM hedge pair 23100 (333/444).
type exitChain struct{}

func (exitChain) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	return &OptionChainSnapshot{Chain: []OptionChainRow{
		{Strike: 22950, CEToken: 111, PEToken: 222, CEBid: 190, CEAsk: 190.5, PEBid: 38.5, PEAsk: 38.6, CELtp: 190.2, PELtp: 38.55},
		{Strike: 23100, CEToken: 333, PEToken: 444, CEBid: 92, CEAsk: 92.3, PEBid: 90.6, PEAsk: 90.9, CELtp: 92.1, PELtp: 90.7},
	}}, nil
}
func (exitChain) PushSnapshot(ctx context.Context, snap TradeSnapshot) error { return nil }

// exitBroker fills IOC limits by rule; MARKET always fills.
//   - 222 (own PE): the first IOC (buffer x1) fills half, the re-send the rest.
//   - 444 (hedge PE): IOC never fills -> MARKET fallback.
type exitBroker struct {
	mu       sync.Mutex
	registry *OrderEventRegistry
	orders   []OrderIntent
}

func (b *exitBroker) ExecuteOrderIntent(ctx context.Context, in OrderIntent) (*ExecutionResult, error) {
	b.mu.Lock()
	b.orders = append(b.orders, in)
	id := fmt.Sprintf("X%d", len(b.orders))
	b.mu.Unlock()
	fill := in.Quantity
	if in.OrderType == "LIMIT" {
		switch in.Token {
		case 222:
			if *in.LimitPrice < 38.6+2.5 { // first attempt: ask + 2
				fill = in.Quantity / 2 / 65 * 65
			}
		case 444:
			fill = 0
		}
	}
	status := "FILLED"
	if fill < in.Quantity {
		status = "CANCELLED" // IOC: the rest is cancelled by the exchange
	}
	b.registry.Publish(contracts.OrderUpdate{TradeID: in.TradeUID, BrokerOrderID: id, Status: status, FilledQty: fill, AvgFillPrice: 1})
	return &ExecutionResult{IntentID: in.IntentID, BrokerOrderID: id, Status: "SUBMITTED"}, nil
}
func (b *exitBroker) GetVerifiedFills(ctx context.Context) ([]BrokerFill, error) { return nil, nil }

func TestCompleteExitRounds_WholePositionIOCThenMarket(t *testing.T) {
	registry := NewOrderEventRegistry()
	registry.SetHealthy(true)
	b := &exitBroker{registry: registry}
	s := &Service{Store: NewMemoryStore(), Snapshot: exitChain{}, OrderEvents: registry,
		freezeQtyBySymbol: map[string]int64{"NIFTY": 3510}}
	tr := StoredTrade{TradeUID: "TRD_EXIT_COMPLETE", Symbol: "NIFTY", Expiry: "19-OCT-26", Lots: 150, LotSize: 65,
		CEToken: 111, PEToken: 222, Status: "SQUARING_OFF"}
	inv := []exitLeg{
		{mtmLeg: mtmLeg{Leg: "CE", Token: 111, Strike: 22950, NetShort: 1690}, side: "BUY", qty: 1690},
		{mtmLeg: mtmLeg{Leg: "PE", Token: 222, Strike: 22950, NetShort: 1950}, side: "BUY", qty: 1950},
		{mtmLeg: mtmLeg{Leg: "CE", Token: 333, Strike: 23100, NetShort: -390}, side: "SELL", qty: 390},
		{mtmLeg: mtmLeg{Leg: "PE", Token: 444, Strike: 23100, NetShort: 390}, side: "BUY", qty: 390},
	}

	remCE, remPE, unconf, err := s.completeExitRounds(tr.TradeUID, tr, b, inv, "TIME")
	if err != nil || len(unconf) > 0 {
		t.Fatalf("err=%v unconfirmed=%v", err, unconf)
	}
	if remCE != 0 || remPE != 0 {
		t.Fatalf("own straddle left CE %d PE %d, want 0/0", remCE, remPE)
	}

	sent := map[int64]int64{}
	filledLimit := map[int64]bool{}
	for _, o := range b.orders {
		if o.Quantity > 130 {
			t.Fatalf("order %d qty %d > 130 (ceil(150/100) = 2 lots)", o.Token, o.Quantity)
		}
		if o.OrderType == "LIMIT" {
			if o.TimeInForce != "IOC" {
				t.Fatalf("limit order on %d not IOC", o.Token)
			}
			bid, ask := bidAskForToken(mustChain(), o.Token)
			k := 0.0
			if o.Side == "BUY" {
				k = (*o.LimitPrice - ask) / 2
			} else {
				k = (bid - *o.LimitPrice) / 2
			}
			if r := math.Round(k); math.Abs(k-r) > 0.03 || r < 1 || r > 3 {
				t.Fatalf("%s %d limit %.2f is not bid/ask -+ 2 x attempt (k=%.2f)", o.Side, o.Token, *o.LimitPrice, k)
			}
		} else if o.Token != 444 {
			t.Fatalf("MARKET sent on %d, only the never-filling 444 should fall back", o.Token)
		}
		if o.Token == 444 && o.OrderType == "LIMIT" {
			filledLimit[444] = true
		}
		sent[o.Token] += o.Quantity
	}
	if !filledLimit[444] {
		t.Fatalf("444 never tried an IOC limit before MARKET")
	}
	// 222 re-sent its unfilled halves: more sent than held, all else exact.
	if sent[111] != 1690 || sent[333] != 390 {
		t.Fatalf("sent CE %d / hedge CE %d, want 1690 / 390", sent[111], sent[333])
	}
	if sent[222] <= 1950 {
		t.Fatalf("own PE sent %d: the half-filled IOCs were not re-sent", sent[222])
	}
}

func mustChain() *OptionChainSnapshot {
	c, _ := exitChain{}.GetOptionChain(context.Background(), "", "")
	return c
}

func TestAggressiveExitReason(t *testing.T) {
	for _, r := range []string{"SL", "PORTFOLIO", "EMERGENCY", "manual"} {
		if !aggressiveExitReason(r) {
			t.Fatalf("%s should keep the aggressive path", r)
		}
	}
	for _, r := range []string{"TIME", "TP", "SQUARE_OFF"} {
		if aggressiveExitReason(r) {
			t.Fatalf("%s should take the complete-position exit", r)
		}
	}
}
