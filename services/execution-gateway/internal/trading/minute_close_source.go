package trading

// Minute-end close source (owner, 2026-11-02): the feed is the main source and
// GreekSoft's candle closes are the backup. On 30 Oct the feed's ATM closes were
// nearer the tick data than GreekSoft's (average gap 0.27 vs 0.59 a leg) and need no
// wait for a REST answer. GreekSoft takes over for a minute only when the feed's ATM
// pair has no recent trade, and still fills minutes missed in a restart gap.
//
// GreekSoft's same-day candle is not a true close: its Close matched the FIRST trade
// after the minute on 97.3% of 744 ATM legs (30 Oct) and 3.8% of the last trade before
// it. Its next-day history is stamped at the minute's start and does match the last
// trade. So the backup is an approximation, for minutes when the feed has nothing.
//
//	MINUTE_CLOSE_PRIMARY=gs  back to GreekSoft first, feed as its fallback
//	GS_MINUTE_CLOSE=0        feed only, no GreekSoft at all

import (
	"os"
	"strings"
	"time"
)

// feedFreshMax: the ATM pair must have traded this close to the minute end for the
// feed to count as alive. ATM options trade every second or so in the session.
const feedFreshMax = 30 * time.Second

func gsPrimary() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("MINUTE_CLOSE_PRIMARY")), "gs")
}

// feedCloseOK: every core token has a close and a trade within feedFreshMax of b.
func feedCloseOK(cc *OptionChainSnapshot, b time.Time, core map[int64]bool) bool {
	if cc == nil {
		return false
	}
	min, seen := b.Add(-feedFreshMax).Unix(), 0
	fresh := func(px float64, ltt, closeLTT int64) bool {
		return px > 0 && (ltt >= min || closeLTT >= min)
	}
	for i := range cc.Chain {
		r := &cc.Chain[i]
		if r.CEToken > 0 && core[r.CEToken] {
			if !fresh(r.CELtp, r.CELTT, r.CECloseLTT) {
				return false
			}
			seen++
		}
		if r.PEToken > 0 && core[r.PEToken] {
			if !fresh(r.PELtp, r.PELTT, r.PECloseLTT) {
				return false
			}
			seen++
		}
	}
	return seen > 0
}

// useFeedClose: this minute is priced at the feed's closes, no GreekSoft wait.
func useFeedClose(cc *OptionChainSnapshot, b time.Time, core map[int64]bool) bool {
	return !gsPrimary() && feedCloseOK(cc, b, core)
}

// feedRegreek: delta / gamma / IV at the feed's closes for tokens, the backtest's
// method (gsRegreek); the snapshot's greeks were taken at another moment.
func feedRegreek(cc *OptionChainSnapshot, b time.Time, tokens map[int64]bool) {
	on := map[int64]float64{}
	for t, ok := range tokens {
		if ok {
			on[t] = 0
		}
	}
	gsRegreek(cc, b, on)
}
