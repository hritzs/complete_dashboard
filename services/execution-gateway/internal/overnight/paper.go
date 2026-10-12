package overnight

// PAPER mode: the same engine, same GreekSoft prices, but orders never
// leave this process -- each one is filled at once at its limit (the live
// ask for a buy, the live bid for a sell).

import (
	"context"
	"fmt"
	"sync"
)

type paperBroker struct {
	mu     sync.Mutex
	seq    int
	orders map[string]Order
}

func newPaperBroker() *paperBroker { return &paperBroker{orders: map[string]Order{}} }

func (p *paperBroker) Place(_ context.Context, r PlaceReq) (string, error) {
	if r.Price <= 0 || r.Qty <= 0 {
		return "", fmt.Errorf("paper order needs a price and a quantity")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	id := fmt.Sprintf("PAPER-%d", p.seq)
	p.orders[id] = Order{ID: id, Status: "FILLED", Qty: r.Qty, Filled: r.Qty, Price: r.Price, AvgFill: r.Price}
	return id, nil
}

func (p *paperBroker) Modify(context.Context, string, float64, int64, int64) error { return nil }
func (p *paperBroker) Cancel(context.Context, string) error                        { return nil }

func (p *paperBroker) Orders(context.Context) ([]Order, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Order, 0, len(p.orders))
	for _, o := range p.orders {
		out = append(out, o)
	}
	return out, nil
}

func (p *paperBroker) FillPrice(_ context.Context, id string) (float64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	o, ok := p.orders[id]
	return o.AvgFill, ok
}

func (p *paperBroker) Positions(context.Context) (map[int64]int64, error) { return nil, nil }
func (p *paperBroker) MinuteCloses(context.Context, int64, string) (map[int64]float64, error) {
	return nil, fmt.Errorf("paper broker has no market data")
}
func (p *paperBroker) Limits(context.Context, int64) (int64, int64, error) {
	return 0, 0, fmt.Errorf("paper broker has no limits")
}
