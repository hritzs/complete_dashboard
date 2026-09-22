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

// tickRuntime used to run its own once-a-minute hedge-eligibility log and
// three legacy alert-only checks (SLPnLLimit/TPPnLTarget/SquareOffTime --
// rupee-based fields nothing that builds a trade today ever sets). It shared
// rt.LastMinuteCheck with runMonitorCycle's own once-a-minute gate on the
// exact same *RuntimeTrade, and runMonitorCycle always runs first each tick
// (see runMonitor below), so it always "claimed" the minute first and this
// function's per-minute block silently never ran -- confirmed live
// 2026-09-22: zero [MINUTE-CHECK] lines were ever written despite trades
// running for several minutes. The real, currently-armed SL/TP/TIME checks
// (SLPnLBpsOfSpot/TPPnLBpsOfSpot/SquareOffHardTime) already run every tick in
// runMonitorCycle, which also now logs an unconditional once-a-minute status
// line for each of them plus a PnL/Greeks snapshot, in the same function that
// has the real thresholds in scope, rather than fixing this one's copy.
// Nothing else in this function had any effect (rt.Snapshot was written but
// never read elsewhere) so it is kept only as the loop's tick hook.
func (s *Service) tickRuntime(rt *RuntimeTrade) error {
	_, ok := s.Store.LoadSnapshot(rt.Trade.TradeUID)
	if !ok {
		return nil
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

func buildLegRatioSatisfied(requestedCE, requestedPE, verifiedCE, verifiedPE int64) bool {
	if requestedCE <= 0 && requestedPE <= 0 {
		return true
	}
	if verifiedCE <= 0 && verifiedPE <= 0 {
		return true
	}
	if requestedCE <= 0 || requestedPE <= 0 {
		return true
	}
	if verifiedCE == 0 || verifiedPE == 0 {
		return false
	}

	requestedRatio := float64(requestedCE) / float64(requestedPE)
	verifiedRatio := float64(verifiedCE) / float64(verifiedPE)
	return math.Abs(verifiedRatio-requestedRatio) <= 0.01
}
