package trading

import (
	"log"
	"math"
	"time"
)

func getLegLtpsFromSnapshot(snap TradeSnapshot, ceToken, peToken int64, ceFallback, peFallback float64) (ceLTP, peLTP float64) {
	ceLTP = ceFallback
	peLTP = peFallback

	for _, leg := range snap.LivePositions {
		if leg.Token == ceToken && leg.OptionType == "CE" {
			ceLTP = leg.LTP
		}
		if leg.Token == peToken && leg.OptionType == "PE" {
			peLTP = leg.LTP
		}
	}

	return ceLTP, peLTP
}

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
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-rt.StopCh:
			return
		case <-ticker.C:
			s.runMonitorCycle(rt.Trade.TradeUID)

			// Stop once the trade is closed by ANY path. Only the autonomous
			// exits used to tear their runtime down, so a manual square-off left
			// this goroutine running for the life of the process, logging a
			// stale status every minute for a position that no longer exists.
			if latest, ok := s.Store.LoadTrade(rt.Trade.TradeUID); ok && isTerminalTradeStatus(latest.Status) {
				s.Store.DeleteRuntime(rt.Trade.TradeUID)
				return
			}

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

	// Perform point-risk hedge eligibility once per clock minute.
	// SL, TP, and time exits below continue on every monitor tick.
	now := time.Now()
	currentMinute := now.Truncate(time.Minute)

	if rt.LastMinuteCheck.IsZero() || !rt.LastMinuteCheck.Equal(currentMinute) {
		rt.LastMinuteCheck = currentMinute

		lotSize := rt.Trade.LotSize
		if lotSize <= 0 {
			lotSize = 1
		}

		const minimumHedgePoints = 19.5

		absDelta := math.Abs(snap.NetDelta)
		dynamicRiskBreached := snap.PointsAllowed > 0 && snap.PointsOut > snap.PointsAllowed
		minimumPointsReached := snap.PointsOut >= minimumHedgePoints

		action := "OK"
		switch {
		case dynamicRiskBreached && !minimumPointsReached:
			action = "HEDGE_SKIPPED_BELOW_19_5"
		case dynamicRiskBreached && minimumPointsReached && absDelta < float64(lotSize):
			action = "HEDGE_SKIPPED_BELOW_LOT"
		case dynamicRiskBreached && minimumPointsReached:
			action = "HEDGE_ELIGIBLE"
		}

		log.Printf(
			"[MINUTE-CHECK] trade=%s status=%s pnl=%.2f (r=%.2f,u=%.2f) delta=%.4f abs_delta=%.4f lot_size=%d gamma=%.6f risk=%.2f/%.2f min_points=%.2f action=%s",
			rt.Trade.TradeUID,
			snap.Status,
			snap.TotalPNL,
			snap.RealizedPNL,
			snap.UnrealizedPNL,
			snap.NetDelta,
			absDelta,
			lotSize,
			snap.NetGamma,
			snap.PointsOut,
			snap.PointsAllowed,
			minimumHedgePoints,
			action,
		)
	}

	// ── Delta hedge threshold logic ──────────────────────────────────────────
	// Hedge qualification is assessed only in the minute block above.
	// It requires both a point-risk breach and absolute delta >= one lot.
	// Actual broker execution remains disabled until explicitly enabled.

	// ── Risk monitors: alert-only, never submit broker orders ───────────────
	// These alerts intentionally do NOT call SquareOff, ExecuteOrderIntent,
	// ManualHedge, or any broker-mutating path. They are safe for monitor tests.
	totalPNL := snap.UnrealizedPNL + snap.RealizedPNL

	if cfg.SLPnLLimit < 0 && totalPNL <= cfg.SLPnLLimit {
		log.Printf(
			"[RISK][SL_ALERT] trade=%s total_pnl=%.2f limit=%.2f broker_orders_sent=0",
			rt.Trade.TradeUID,
			totalPNL,
			cfg.SLPnLLimit,
		)
	}

	if cfg.TPPnLTarget > 0 && totalPNL >= cfg.TPPnLTarget {
		log.Printf(
			"[RISK][TP_ALERT] trade=%s total_pnl=%.2f target=%.2f broker_orders_sent=0",
			rt.Trade.TradeUID,
			totalPNL,
			cfg.TPPnLTarget,
		)
	}

	if !cfg.SquareOffTime.IsZero() && !time.Now().Before(cfg.SquareOffTime) {
		log.Printf(
			"[RISK][EXIT_TIME_ALERT] trade=%s now=%s target=%s broker_orders_sent=0",
			rt.Trade.TradeUID,
			time.Now().Format(time.RFC3339),
			cfg.SquareOffTime.Format(time.RFC3339),
		)
	}

	return nil
}

// ResumeRuntime re-attaches a background monitor to a trade that was already
// open before this process started (e.g. after a restart), so live PnL/Greeks
// monitoring never silently stops just because the service was redeployed.
func (s *Service) ResumeRuntime(trade StoredTrade) {
	s.startRuntime(trade)
}

// isTerminalTradeStatus reports whether a trade has finished and nothing
// should be monitoring it any more.
func isTerminalTradeStatus(status string) bool {
	switch status {
	case "CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MANUAL", "FAILED":
		return true
	}
	return false
}
