package trading

// Hand-off: once a LIVE straddle build has finished building -- complete,
// stopped part-way (partial) or stopped by a rejection -- its position
// becomes a NORMAL trade: the trades row is filled in like any other build
// (strike, CE/PE tokens, quantities, entry prices, lots, SL / TP / exit
// time / hedge settings from the rule) and the standard monitor (PMS: hedge,
// SL, TP, exit time, Partial / Full Exit, Portfolio row) runs it from then
// on. The build's own runner stops monitoring (phase HANDED_OFF), so exactly
// one monitor owns the position.
//
// Only a plain straddle is handed off: one short CE token + one short PE
// token. A build spread over several strikes (ATM moved) or carrying hedge
// legs stays with the build's own monitor (logged once).

import (
	"fmt"
	"log"
	"math"
	"time"
)

const sbPhaseHandedOff = "HANDED_OFF"

// sbHandOffLocked hands the run's position to the standard monitor.
// Caller holds r.mu. Returns true if handed off.
func (s *Service) sbHandOffLocked(r *sbRunner) bool {
	if !r.isLive || r.tradeUID == "" || r.busy || len(r.pending) > 0 || s.Store == nil {
		return false
	}
	v := r.pms.View(r.chain)
	if v.Flat {
		return false
	}
	var ce, pe *PMSLeg
	why := ""
	for i := range v.Legs {
		l := v.Legs[i]
		if l.Qty == 0 {
			continue
		}
		switch {
		case l.Qty > 0:
			why = fmt.Sprintf("long %s %.0f leg (a hedge) is open", l.OptionType, l.Strike)
		case l.OptionType == "CE" && ce == nil:
			ce = &v.Legs[i]
		case l.OptionType == "PE" && pe == nil:
			pe = &v.Legs[i]
		default:
			why = fmt.Sprintf("more than one %s strike is open", l.OptionType)
		}
	}
	if why == "" && (ce == nil || pe == nil) {
		why = "only one leg is open"
	}
	if why != "" {
		if !r.handoffNoted {
			r.handoffNoted = true
			r.event("INFO", "not handed to the standard monitor (%s) -- the build's own monitor keeps running it (hedge / SL / TP / exit time)", why)
		}
		return false
	}
	tr, ok := s.Store.LoadTrade(r.tradeUID)
	if !ok {
		return false
	}
	cfg := r.cfg
	lot := r.lotSize
	if lot <= 0 {
		return false
	}
	tr.Strike = (ce.Strike + pe.Strike) / 2
	tr.CEToken, tr.PEToken = ce.Token, pe.Token
	tr.CEQty, tr.PEQty = int(-ce.Qty), int(-pe.Qty)
	tr.CELtp, tr.PELtp = ce.AvgPrice, pe.AvgPrice
	tr.LotSize = lot
	tr.Lots = (tr.CEQty + tr.PEQty) / (2 * lot)
	if tr.Lots <= 0 {
		tr.Lots = 1
	}
	tr.NetDelta = v.NetDelta
	if r.chain != nil {
		tr.Underlying = chooseUnderlying(*r.chain)
		if tr.Expiry == "" {
			tr.Expiry = r.chain.Expiry
		}
	}
	if tr.Expiry == "" {
		tr.Expiry = r.expiry
	}
	target := int(cfg.Straddles)
	if tr.CEQty < target || tr.PEQty < target {
		tr.Config.PartialFill = true
		tr.Config.RequestedCEQty, tr.Config.RequestedPEQty = target, target
	}
	tr.Config.BuyBuffer, tr.Config.SellBuffer = 2.0, 2.0
	tr.Config.PollIntervalSec = 1
	tr.Config.StraddleDiv, tr.Config.HedgeDiv = cfg.StraddleDiv, cfg.HedgeDiv
	if tr.Config.StraddleDiv <= 0 {
		tr.Config.StraddleDiv = 4
	}
	if tr.Config.HedgeDiv <= 0 {
		tr.Config.HedgeDiv = 57
	}
	if cfg.HedgeMinBps > 0 {
		hm := cfg.HedgeMinBps
		tr.Config.HedgeMinThresholdBps = &hm
	}
	now := time.Now()
	if err := applyBuildRiskConfig(&tr.Config, &BuildRiskConfig{ExitTime: cfg.ExitTime, SlBps: cfg.SLBps, TpBps: cfg.TPBps}, now); err != nil {
		log.Printf("[SBUILD-LIVE] rule=%s hand-off: risk config: %v", cfg.ID, err)
	}
	tr.Status = "ACTIVE"
	tr.LastUpdateTime = now
	s.Store.UpdateTrade(tr)
	s.startRuntime(tr)

	r.phase = sbPhaseHandedOff
	r.event("HANDOFF", "position handed to the STANDARD monitor as trade %s: CE %d @%.2f + PE %d @%.2f at %.0f (%d lots%s), SL %.0f bps, TP %.0f bps, exit %s, straddle/hedge div %.0f/%.0f -- it now shows as a normal Portfolio trade (Hedge / Partial Exit / Full Exit there); this build stops monitoring",
		tr.TradeUID, tr.CEQty, tr.CELtp, tr.PEQty, tr.PELtp, tr.Strike, tr.Lots, map[bool]string{true: ", PARTIAL of " + fmt.Sprint(target), false: ""}[tr.Config.PartialFill],
		cfg.SLBps, cfg.TPBps, cfg.ExitTime, tr.Config.StraddleDiv, tr.Config.HedgeDiv)
	log.Printf("[SBUILD-LIVE] rule=%s trade=%s HANDED OFF to the standard monitor: CE %d / PE %d, net delta %.2f, sold straddle %.2f",
		cfg.ID, tr.TradeUID, tr.CEQty, tr.PEQty, v.NetDelta, math.Round(v.BuildStraddle*100)/100)
	r.persistLocked()
	return true
}

// sbFollowHandedOffLocked: once the standard trade a build was handed to has
// closed (SL / TP / time / manual exit), the build is finished too -- phase
// EXITED with the trade's realized PnL, so the tab stops showing the old
// position. Checked every few seconds. Caller holds r.mu.
func (s *Service) sbFollowHandedOffLocked(r *sbRunner) {
	if s.Store == nil || r.tradeUID == "" || time.Since(r.followAt) < 3*time.Second {
		return
	}
	r.followAt = time.Now()
	tr, ok := s.Store.LoadTrade(r.tradeUID)
	if !ok || !isTerminalTradeStatus(tr.Status) {
		return
	}
	r.phase = "EXITED"
	r.event("EXIT", "trade %s closed by the standard monitor (%s, realized ₹%.2f) -- build finished", tr.TradeUID, tr.Status, tr.RealizedPnL)
	r.persistLocked()
}
