package trading

// Per-order VALUE cap. Besides the exchange freeze quantity, the broker's
// RMS rejects any single order whose value -- (strike + premium) x quantity
// -- is above a fixed amount: "Single order value exceeded Allowed Value:
// 22500000" (2026-10-12 09:23: 17- and 26-lot hedge orders near 22,600 were
// rejected; only 15 lots fit). The freeze quantity alone allows 54 lots
// (~7.9 crore), so every order path -- hedges, SL / square-off chunks,
// builds, wings, mock -- is also capped by value here.
//
//	max lots = floor( limit / (underlying x 1.04) / lot )
//
// The 4% margin covers strikes above the money (strike + premium is what
// the broker multiplies). The cap is OFF by default; GS_MAX_ORDER_VALUE
// sets the limit in rupees (the broker's was 22500000 on 2026-10-12) and
// switches it on.

import (
	"context"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Off unless GS_MAX_ORDER_VALUE is set (e.g. 22500000): the user wants
	// no platform-side cap for now.
	orderValueDefault = 0.0
	orderValueMargin  = 1.04
)

var orderValueRef struct {
	mu   sync.Mutex
	px   map[string]float64
	at   map[string]time.Time
	told map[string]int64
}

func orderValueLimit() float64 {
	if v := strings.TrimSpace(os.Getenv("GS_MAX_ORDER_VALUE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			return f
		}
	}
	return orderValueDefault
}

// underlyingFor: the symbol's live underlying (cached 5 s).
func (s *Service) underlyingFor(symbol string) float64 {
	orderValueRef.mu.Lock()
	if orderValueRef.px == nil {
		orderValueRef.px, orderValueRef.at, orderValueRef.told = map[string]float64{}, map[string]time.Time{}, map[string]int64{}
	}
	px, at := orderValueRef.px[symbol], orderValueRef.at[symbol]
	orderValueRef.mu.Unlock()
	if px > 0 && time.Since(at) < 5*time.Second {
		return px
	}
	if s.Snapshot == nil {
		return px
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := s.Snapshot.GetOptionChain(ctx, symbol, "")
	if err != nil || c == nil {
		return px // the last known one
	}
	u := lutUnderlying(c)
	if u <= 0 {
		u = c.ATM
	}
	if u > 0 {
		orderValueRef.mu.Lock()
		orderValueRef.px[symbol], orderValueRef.at[symbol] = u, time.Now()
		orderValueRef.mu.Unlock()
		return u
	}
	return px
}

// capByOrderValue lowers qty (a lot multiple) to what one order may be
// worth; at least one lot.
func (s *Service) capByOrderValue(symbol string, qty, lotSize int64) int64 {
	limit := orderValueLimit()
	if limit <= 0 || lotSize <= 0 || qty <= lotSize {
		return qty
	}
	u := s.underlyingFor(symbol)
	if u <= 0 {
		return qty
	}
	lots := int64(math.Floor(limit / (u * orderValueMargin) / float64(lotSize)))
	if lots < 1 {
		lots = 1
	}
	capQty := lots * lotSize
	if capQty >= qty {
		return qty
	}
	orderValueRef.mu.Lock()
	if orderValueRef.told[symbol] != capQty {
		orderValueRef.told[symbol] = capQty
		log.Printf("[ORDER-CAP] %s: max per order %d (%d lots) by the broker's order-value limit ₹%.0f at underlying %.2f (freeze quantity alone allows %d)",
			symbol, capQty, lots, limit, u, qty)
	}
	orderValueRef.mu.Unlock()
	return capQty
}
