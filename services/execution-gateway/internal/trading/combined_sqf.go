package trading

// Combined square-off rules. Every exit closes only what is OPEN (sized
// from the verified fills, never the stored size), so together they can
// never close more than 100% or turn a short into a long:
//
//   - MTM exit (mtm_exit.go)  : MTMExitPct of the ORIGINAL position, once
//     the trade's executable MTM >= its level, lot by lot at IOC limits.
//   - ATM-straddle exit (here): StraddleExitPct of the ORIGINAL position,
//     only when the live ATM CE LTP + PE LTP is BELOW its level.
//   - PSQF (manual)           : a % of what is open now.
//   - Full exit (SL/TP/TIME/manual square-off): whatever is still open.
//
// Each rule remembers how much it has closed (…ClosedQty, straddle
// contracts CE+PE): its remaining share = its % of the original - what it
// closed, capped by what is open. E.g. MTM 50% fires -> half closed; the
// straddle rule at 100% then closes the remaining 50%, not another 100%.

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// straddleOpenOrig: the straddle legs' (the trade's own CE/PE tokens)
// open short quantity and original (built) quantity, from the fills.
func straddleOpenOrig(legs []mtmLeg, tr StoredTrade) (open, orig int64) {
	for _, l := range legs {
		if l.Token != tr.CEToken && l.Token != tr.PEToken {
			continue
		}
		if l.NetShort > 0 {
			open += l.NetShort
		}
		orig += l.BuildSold
	}
	if orig <= 0 { // no build fills tagged (older rows): the trade's size
		orig = int64(tr.CEQty + tr.PEQty)
		if s := int64(tr.Lots) * int64(tr.LotSize) * 2; s > orig {
			orig = s
		}
	}
	return open, orig
}

// ruleRemaining is how many straddle contracts a rule may still close:
// pct of the original (rounded to lots), less what it already closed,
// never more than is open. pct <= 0 or >= 100 = complete (all open).
func ruleRemaining(pct float64, orig, closed, open, lot int64) int64 {
	if open <= 0 {
		return 0
	}
	if pct <= 0 || pct >= 100 {
		return open
	}
	if lot <= 0 {
		lot = 1
	}
	target := int64(math.Round(pct/100*float64(orig)/float64(lot))) * lot
	rem := target - closed
	if rem < 0 {
		rem = 0
	}
	return min(rem, open)
}

// legCloseTargets splits closing `rem` straddle contracts across every
// open non-wing leg pro rata (straddle and hedges alike), whole lots, each
// capped by that leg's open quantity. Complete (rem >= open) = everything.
func legCloseTargets(legs []mtmLeg, rem, open, lot int64) map[int64]int64 {
	out := map[int64]int64{}
	if rem <= 0 || open <= 0 {
		return out
	}
	f := math.Min(1, float64(rem)/float64(open))
	for _, l := range legs {
		q := abs64(l.NetShort)
		if q == 0 {
			continue
		}
		if f >= 1 {
			out[l.Token] = q
			continue
		}
		t := int64(math.Round(f*float64(q)/float64(lot))) * lot
		out[l.Token] = min(t, q)
	}
	return out
}

// straddleExitCheck runs every monitor tick for a trade with the ATM
// straddle rule set: below the level it closes the rule's remaining share
// (complete via SquareOff "STRADDLE", partial via PSQF of the open
// position), and records what it closed. Returns true if it acted.
func (s *Service) straddleExitCheck(trade StoredTrade, chain *OptionChainSnapshot) bool {
	rules := straddleRules(trade.Config) // highest level first (exit_tiers.go)
	if len(rules) == 0 || trade.Status != "ACTIVE" || !sbLiveWindow(time.Now().In(lutIST())) {
		return false
	}
	atm, err := FindATMRow(*chain)
	if err != nil || atm.CELtp <= 0 || atm.PELtp <= 0 {
		return false
	}
	straddle := atm.CELtp + atm.PELtp
	if straddle >= rules[0].level {
		return false // above every level
	}
	src, ok := s.Store.(interface {
		TradeLegCash(ctx context.Context, tradeUID string) ([]mtmLeg, error)
	})
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	legs, lerr := src.TradeLegCash(ctx, trade.TradeUID)
	cancel()
	if lerr != nil {
		return false
	}
	open, orig := straddleOpenOrig(legs, trade)
	// The first step below whose level the straddle is and with a share
	// left fires; lower steps wait for it.
	for _, r := range rules {
		if straddle >= r.level {
			return false
		}
		rem := ruleRemaining(r.pct, orig, r.closed, open, int64(trade.LotSize))
		if rem <= 0 {
			continue // this step's share is done (or nothing open)
		}
		if s.exitRunning(trade.TradeUID) {
			return false
		}
		log.Printf("[RISK] STRADDLE_TRIGGER trade=%s %s: ATM %.0f straddle %.2f (CE %.2f + PE %.2f) < %.2f -- closing %d of %d open straddle contracts (%.0f%% of %d, %d closed before by this step)",
			trade.TradeUID, r.name(), atm.Strike, straddle, atm.CELtp, atm.PELtp, r.level, rem, open, ruleShare(r.pct), orig, r.closed)
		idx := r.idx
		return s.runExitAsync(trade.TradeUID, "STRADDLE", func() { s.straddleExitRun(trade, src, rem, open, idx) })
	}
	return false
}

// straddleExitRun closes the straddle rule's share and records it.
func (s *Service) straddleExitRun(trade StoredTrade, src interface {
	TradeLegCash(ctx context.Context, tradeUID string) ([]mtmLeg, error)
}, rem, open int64, rule int) {
	var runErr error
	if rem >= open {
		runErr = s.SquareOff(trade.TradeUID, "STRADDLE")
	} else {
		runErr = s.PartialSquareOff(trade.TradeUID, float64(rem)/float64(open)*100)
	}
	// Record what actually closed (verified fills), success or not.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	after, aerr := src.TradeLegCash(ctx2, trade.TradeUID)
	cancel2()
	if aerr == nil {
		open2, _ := straddleOpenOrig(after, trade)
		if tr, ok := s.Store.LoadTrade(trade.TradeUID); ok && open2 < open {
			addStraddleClosed(&tr.Config, rule, open-open2)
			tr.LastUpdateTime = time.Now()
			s.Store.UpdateTrade(tr)
			if isTerminalTradeStatus(tr.Status) {
				if rt, ok := s.Store.LoadRuntime(tr.TradeUID); ok {
					close(rt.StopCh)
					s.Store.DeleteRuntime(tr.TradeUID)
				}
			}
		}
	}
	if runErr != nil {
		log.Printf("[RISK] STRADDLE exit trade=%s: %v -- retried next tick for what is left of its share", trade.TradeUID, runErr)
	}
}

func ruleShare(pct float64) float64 {
	if pct <= 0 || pct >= 100 {
		return 100
	}
	return pct
}

// ruleLabel shows a rule's level and share ("∞" when off).
func ruleLabel(level *float64, pct float64, unit string) string {
	if level == nil {
		return "∞"
	}
	return fmt.Sprintf("%g %s · %.0f%%", *level, unit, ruleShare(pct))
}

// exitsInFlight: one background exit per trade at a time.
var exitsInFlight sync.Map

// runExitAsync runs an exit in the background unless one is already
// running for the trade. Reports whether it started.
func (s *Service) runExitAsync(tradeUID, what string, fn func()) bool {
	if _, busy := exitsInFlight.LoadOrStore(tradeUID, what); busy {
		return false
	}
	go func() {
		defer exitsInFlight.Delete(tradeUID)
		fn()
	}()
	return true
}

func (s *Service) exitRunning(tradeUID string) bool {
	_, busy := exitsInFlight.Load(tradeUID)
	return busy
}
