package greeks

import (
    "sync"
    "time"
)

type TradeGreeks struct {
    TradeUID  string
    NetDelta  float64
    NetGamma  float64
    NetTheta  float64
    NetVega   float64
    UpdatedAt time.Time
}

type TradeLeg struct {
    TradeUID string
    Token    int64
    Quantity int
    Side     string // "BUY" or "SELL"
}

type Monitor interface {
    RegisterTrade(legs []TradeLeg)
    RemoveTrade(tradeUID string)
    UpdateLegGreeks(token int64, delta, gamma, theta, vega float64)
    GetGreeks(tradeUID string) (TradeGreeks, bool)
}

// noopMonitor is the default no-op implementation.
type noopMonitor struct{}

func NewNoopMonitor() Monitor {
    return &noopMonitor{}
}

func (n *noopMonitor) RegisterTrade(legs []TradeLeg)                         {}
func (n *noopMonitor) RemoveTrade(tradeUID string)                           {}
func (n *noopMonitor) UpdateLegGreeks(token int64, d, g, t, v float64)       {}
func (n *noopMonitor) GetGreeks(tradeUID string) (TradeGreeks, bool) {
    return TradeGreeks{}, false
}

// wsMonitor is a real implementation that aggregates leg Greeks per trade.
type wsMonitor struct {
    mu          sync.RWMutex
    tokenToLegs map[int64][]TradeLeg
    tradeGreeks map[string]TradeGreeks
}

func NewWSMonitor() Monitor {
    return &wsMonitor{
        tokenToLegs: make(map[int64][]TradeLeg),
        tradeGreeks: make(map[string]TradeGreeks),
    }
}

func (m *wsMonitor) RegisterTrade(legs []TradeLeg) {
    m.mu.Lock()
    defer m.mu.Unlock()

    for _, leg := range legs {
        m.tokenToLegs[leg.Token] = append(m.tokenToLegs[leg.Token], leg)
    }
}

func (m *wsMonitor) RemoveTrade(tradeUID string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    // Remove from tradeGreeks map
    delete(m.tradeGreeks, tradeUID)

    // Remove legs belonging to this trade from tokenToLegs
    for token, legs := range m.tokenToLegs {
        filtered := legs[:0]
        for _, leg := range legs {
            if leg.TradeUID != tradeUID {
                filtered = append(filtered, leg)
            }
        }
        if len(filtered) == 0 {
            delete(m.tokenToLegs, token)
        } else {
            m.tokenToLegs[token] = filtered
        }
    }
}

// UpdateLegGreeks should be called whenever you receive fresh Greeks for a token.
func (m *wsMonitor) UpdateLegGreeks(token int64, delta, gamma, theta, vega float64) {
    m.mu.Lock()
    defer m.mu.Unlock()

    legs, ok := m.tokenToLegs[token]
    if !ok || len(legs) == 0 {
        return
    }

    // For each leg that uses this token, update its trade's aggregated Greeks.
    now := time.Now()

    for _, leg := range legs {
        sign := 1.0
        if leg.Side == "SELL" {
            sign = -1.0
        }

        qty := float64(leg.Quantity)

        contribDelta := sign * qty * delta
        contribGamma := sign * qty * gamma
        contribTheta := sign * qty * theta
        contribVega := sign * qty * vega

        tg := m.tradeGreeks[leg.TradeUID]
        tg.TradeUID = leg.TradeUID
        tg.NetDelta += contribDelta
        tg.NetGamma += contribGamma
        tg.NetTheta += contribTheta
        tg.NetVega += contribVega
        tg.UpdatedAt = now

        m.tradeGreeks[leg.TradeUID] = tg
    }
}

func (m *wsMonitor) GetGreeks(tradeUID string) (TradeGreeks, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    tg, ok := m.tradeGreeks[tradeUID]
    return tg, ok
}