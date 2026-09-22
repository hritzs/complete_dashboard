package trading

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"trading-platform/libs/contracts"
)

type OrderEventRegistry struct {
	mu      sync.Mutex
	latest  map[string]contracts.OrderUpdate
	waiters map[string]map[uint64]chan contracts.OrderUpdate
	nextID  uint64
	healthy bool
}

func NewOrderEventRegistry() *OrderEventRegistry {
	return &OrderEventRegistry{latest: make(map[string]contracts.OrderUpdate), waiters: make(map[string]map[uint64]chan contracts.OrderUpdate)}
}
func (r *OrderEventRegistry) SetHealthy(healthy bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.healthy = healthy
	r.mu.Unlock()
}

func (r *OrderEventRegistry) Healthy() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.healthy
}

func orderEventKey(t, o string) string { return strings.TrimSpace(t) + "|" + strings.TrimSpace(o) }
func orderStatusRank(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "REJECTED":
		return 100
	case "FILLED":
		return 90
	case "CANCELLED", "CANCELED":
		return 80
	case "PARTIAL_FILL", "PARTIALLY_FILLED":
		return 70
	case "ACKED", "OPEN":
		return 50
	case "SUBMITTED":
		return 40
	}
	return 0
}
func orderTerminal(u contracts.OrderUpdate, q int64) bool {
	if q > 0 && u.FilledQty >= q {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(u.Status)) {
	case "FILLED", "REJECTED", "CANCELLED", "CANCELED":
		return true
	}
	return false
}
func shouldReplaceOrderUpdate(a, b contracts.OrderUpdate) bool {
	if orderStatusRank(b.Status) > orderStatusRank(a.Status) {
		return true
	}
	if b.FilledQty > a.FilledQty {
		return true
	}
	return orderStatusRank(b.Status) == orderStatusRank(a.Status) && b.InternalTime.After(a.InternalTime)
}
func (r *OrderEventRegistry) Publish(u contracts.OrderUpdate) {
	if r == nil {
		return
	}
	t := strings.TrimSpace(u.TradeID)
	o := strings.TrimSpace(u.BrokerOrderID)
	if t == "" || o == "" {
		return
	}
	k := orderEventKey(t, o)
	r.mu.Lock()
	a, ok := r.latest[k]
	if !ok || shouldReplaceOrderUpdate(a, u) {
		r.latest[k] = u
	}
	ls := make([]chan contracts.OrderUpdate, 0, len(r.waiters[k]))
	for _, c := range r.waiters[k] {
		ls = append(ls, c)
	}
	r.mu.Unlock()
	for _, c := range ls {
		select {
		case c <- u:
		default:
		}
	}
}
func (r *OrderEventRegistry) Latest(t, o string) (contracts.OrderUpdate, bool) {
	if r == nil {
		return contracts.OrderUpdate{}, false
	}
	k := orderEventKey(t, o)
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.latest[k]
	return u, ok
}
func (r *OrderEventRegistry) WaitTerminal(ctx context.Context, t, o string, q int64, d time.Duration) (contracts.OrderUpdate, error) {
	if r == nil {
		return contracts.OrderUpdate{}, fmt.Errorf("Iris registry unavailable")
	}
	t = strings.TrimSpace(t)
	o = strings.TrimSpace(o)
	if t == "" || o == "" {
		return contracts.OrderUpdate{}, fmt.Errorf("empty trade/order id")
	}
	if q <= 0 {
		return contracts.OrderUpdate{}, fmt.Errorf("invalid desired quantity %d", q)
	}
	k := orderEventKey(t, o)
	r.mu.Lock()
	if u, ok := r.latest[k]; ok && orderTerminal(u, q) {
		r.mu.Unlock()
		return u, nil
	}
	r.nextID++
	id := r.nextID
	c := make(chan contracts.OrderUpdate, 8)
	if r.waiters[k] == nil {
		r.waiters[k] = make(map[uint64]chan contracts.OrderUpdate)
	}
	r.waiters[k][id] = c
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if m := r.waiters[k]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(r.waiters, k)
			}
		}
		r.mu.Unlock()
	}()
	if d <= 0 {
		d = 2 * time.Second
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	for {
		select {
		case <-ctx.Done():
			return contracts.OrderUpdate{}, ctx.Err()
		case <-tm.C:
			if u, ok := r.Latest(t, o); ok {
				return u, fmt.Errorf("Iris timeout order=%s status=%s filled=%d/%d", o, u.Status, u.FilledQty, q)
			}
			return contracts.OrderUpdate{}, fmt.Errorf("Iris timeout order=%s no update", o)
		case u := <-c:
			if orderTerminal(u, q) {
				return u, nil
			}
		}
	}
}
