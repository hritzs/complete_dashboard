package trading

// Candle-close prices for the LUT minute: each option's LAST TRADE BEFORE
// the minute boundary by exchange trade time -- what a terminal's 1-minute
// candle shows (GreekSoft's 09:17 straddle = 09:16-09:17 candle close).
// The LTP at the minute's first tick instead depends on when the conflated
// broadcast last refreshed that option (~640ms apart, measured 2026-10-07)
// and read up to ~1.7pts off the candle close.

import (
	"math"
	"time"
)

// lutCloseGrace: how long after a boundary to wait for the ATM legs'
// closes to become final (a trade with time >= the boundary seen) before
// recording with what is known -- the last trade so far, which is the
// close unless a pre-boundary trade is still in flight.
const lutCloseGrace = 2 * time.Second

// lutCloseBefore is one leg's last trade before boundary B (unix seconds).
// final: a later trade confirms nothing else came before B. ok=false: no
// trade-time data for the leg (older decoder) -- callers keep the LTP.
func lutCloseBefore(ltp float64, ltt int64, close float64, closeLTT, closeNext int64, b int64) (px float64, final, ok bool) {
	switch {
	case closeNext > 0 && closeLTT < b && b <= closeNext && close > 0:
		return close, true, true
	case ltt > 0 && ltt < b && ltp > 0:
		return ltp, false, true // nothing traded since B yet
	}
	return ltp, false, false
}

// lutCloseChain returns a copy of chain whose CE/PE LTPs are the closes
// before boundary b, with the synthetic future re-derived from those same
// closes, and whether it is ready to record: the strike at the synthetic
// future has both closes final, the grace period is over, or the feed
// carries no trade times at all (then it is the chain as is).
func lutCloseChain(chain *OptionChainSnapshot, b time.Time, now time.Time) (*OptionChainSnapshot, bool) {
	if chain == nil {
		return nil, false
	}
	bs := b.Unix()
	cp := *chain
	cp.Chain = make([]OptionChainRow, len(chain.Chain))
	copy(cp.Chain, chain.Chain)
	hasTimes := false
	final := map[float64]bool{}
	for i := range cp.Chain {
		r := &cp.Chain[i]
		ce, ceFinal, ceOK := lutCloseBefore(r.CELtp, r.CELTT, r.CEClose, r.CECloseLTT, r.CECloseNext, bs)
		pe, peFinal, peOK := lutCloseBefore(r.PELtp, r.PELTT, r.PEClose, r.PECloseLTT, r.PECloseNext, bs)
		if ceOK || peOK {
			hasTimes = true
		}
		r.CELtp, r.PELtp = ce, pe
		final[r.Strike] = ceFinal && peFinal
	}
	if !hasTimes {
		return chain, true
	}
	// Synthetic future from the closes at the strike nearest the live one
	// (put-call parity, the decoder's own formula: K + CE - PE).
	if S := lutUnderlying(chain); S > 0 {
		var best *OptionChainRow
		for i := range cp.Chain {
			r := &cp.Chain[i]
			if r.CELtp > 0 && r.PELtp > 0 && (best == nil || math.Abs(r.Strike-S) < math.Abs(best.Strike-S)) {
				best = r
			}
		}
		if best != nil {
			cp.SyntheticFuture = best.Strike + best.CELtp - best.PELtp
			cp.ATM = math.Round(cp.SyntheticFuture/lutStrikeStep) * lutStrikeStep
		}
	}
	K := math.Round(lutUnderlying(&cp)/lutStrikeStep) * lutStrikeStep
	return &cp, final[K] || now.Sub(b) >= lutCloseGrace
}
