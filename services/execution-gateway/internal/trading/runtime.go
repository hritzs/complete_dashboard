package trading

import (
    "log"
    "math"
    "time"
)

// startRuntime spins up a background monitor for the given trade.
// Ensure this function exists only here (remove any duplicate from service.go).
func (s *Service) startRuntime(trade StoredTrade) {
    // If a runtime is already running for this trade, do not start another.
    if _, ok := s.Store.LoadRuntime(trade.TradeUID); ok {
        return
    }

    rt := &RuntimeTrade{
        Trade:    trade,
        Snapshot: TradeSnapshot{TradeUID: trade.TradeUID},
        StopCh:   make(chan struct{}),
        DoneCh:   make(chan struct{}),
    }

    s.Store.SaveRuntime(rt)

    go s.runMonitor(rt)
}

// runMonitor periodically calls tickRuntime according to the monitor config.
func (s *Service) runMonitor(rt *RuntimeTrade) {
    defer close(rt.DoneCh)

    interval := time.Duration(rt.Trade.Config.PollIntervalSec) * time.Second
    if interval <= 0 {
        interval = 60 * time.Second
    }

    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-rt.StopCh:
            return
        case <-ticker.C:
            if err := s.tickRuntime(rt); err != nil {
                log.Printf("runtime tick error for %s: %v", rt.Trade.TradeUID, err)
            }
        }
    }
}

// tickRuntime applies runtime rules such as delta hedge thresholds using the latest snapshot.
func (s *Service) tickRuntime(rt *RuntimeTrade) error {
    snap, ok := s.Store.LoadSnapshot(rt.Trade.TradeUID)
    if !ok {
        return nil
    }

    rt.Snapshot = snap
    cfg := rt.Trade.Config

    // Example: delta hedge threshold logic.
    if cfg.HedgeThresholdDelta != 0 && math.Abs(snap.NetDelta) > cfg.HedgeThresholdDelta {
        log.Printf(
            "HEDGE: trade=%s netDelta=%.2f threshold=%.2f",
            rt.Trade.TradeUID,
            snap.NetDelta,
            cfg.HedgeThresholdDelta,
        )
        // TODO: send hedge order via executor (ManualHedge or auto-hedge).
    }

    // Example: add SL / profit-booking checks here later if needed.

    return nil
}