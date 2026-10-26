package trading

import (
	"log"
	"time"
)

// 30-second average entry for the trades a LUT build actually fires (the
// real build and its test fires), as the backtest fills an entry
// (GammaShort_Backtest_Final.py N_OPT_SECONDS: options at the mean of the
// next 30 seconds of bid): from the fire on, the first quote of each second
// of the trade's strike, 30 seconds. Only for those trades -- nothing is
// captured for the Paper Sim's other cards. The simulation driving the trade
// and its card enter at these prices once captured (until then, and if the
// gateway restarts inside the 30 s, at the minute's close).
const (
	lutEntryAvgSecs = 30
	lutEntryAvgWait = 40 * time.Second // give up a capture this long after the fire
)

// lutEntryAvgCapture: one capture in progress (unexported: not saved).
type lutEntryAvgCapture struct {
	lastSec        string
	sumCE, sumPE   float64
	n              int
	first, lastKey string
}

// noteEntryAvgLocked samples the strike's CE / PE bid once a second for every
// fired run still capturing (caller holds e.mu, on the LUT tick).
func (e *lutEngine) noteEntryAvgLocked(now time.Time, day string, chain *OptionChainSnapshot) {
	if chain == nil {
		return
	}
	runs := append([]*LUTBuildRun{e.build}, e.tests...)
	for _, run := range runs {
		if run == nil || run.EntryAvgN > 0 || run.FiredAt == "" || run.Strike <= 0 || run.Status == "REFUSED" || run.Status == "WAITING" {
			continue
		}
		fired, err := time.ParseInLocation("2006-01-02 15:04:05.000", day+" "+run.FiredAt, lutIST())
		if err != nil || now.Before(fired) {
			continue
		}
		if e.entryAvg == nil {
			e.entryAvg = map[*LUTBuildRun]*lutEntryAvgCapture{}
		}
		c := e.entryAvg[run]
		if c == nil {
			c = &lutEntryAvgCapture{}
			e.entryAvg[run] = c
		}
		done := false
		if now.Sub(fired) > lutEntryAvgWait {
			done = true // whatever seconds were seen (the backtest's min_periods=1)
		} else if sec := now.Format("15:04:05"); sec != c.lastSec {
			if ceBid, peBid := lutStrikeBids(chain, run.Strike); ceBid > 0 && peBid > 0 {
				c.lastSec = sec
				c.sumCE, c.sumPE, c.n = c.sumCE+ceBid, c.sumPE+peBid, c.n+1
				if c.first == "" {
					c.first = sec
				}
				c.lastKey = sec
				done = c.n >= lutEntryAvgSecs
			}
		}
		if !done {
			continue
		}
		delete(e.entryAvg, run)
		if c.n == 0 {
			log.Printf("[LUT-BUILD] 30 s entry average for %s %.0f: no bids seen -- the simulation keeps the minute's close", run.Minute, run.Strike)
			run.EntryAvgN = -1 // tried
			e.saveDayLocked(day)
			continue
		}
		run.EntryAvgCE, run.EntryAvgPE, run.EntryAvgN = c.sumCE/float64(c.n), c.sumPE/float64(c.n), c.n
		run.EntryAvgFrom, run.EntryAvgTo = c.first, c.lastKey
		log.Printf("[LUT-BUILD] 30 s entry average for %s %.0f (%s-%s, %d s of bid): CE %.2f PE %.2f = %.2f -- the simulation enters at these",
			run.Minute, run.Strike, c.first, c.lastKey, c.n, run.EntryAvgCE, run.EntryAvgPE, run.EntryAvgCE+run.EntryAvgPE)
		e.saveDayLocked(day)
	}
}

// lutStrikeBids: the CE / PE bid of a strike on the chain.
func lutStrikeBids(chain *OptionChainSnapshot, strike float64) (ce, pe float64) {
	for _, r := range chain.Chain {
		if r.Strike == strike {
			return r.CEBid, r.PEBid
		}
	}
	return 0, 0
}

// lutNearestExpiry: the earliest listed expiry (the current week).
func lutNearestExpiry(chain *OptionChainSnapshot) string {
	best, bestT := chain.Expiry, time.Time{}
	if t, err := lutParseExpiry(chain.Expiry); err == nil {
		bestT = t
	}
	for _, x := range chain.AvailableExpiries {
		if t, err := lutParseExpiry(x); err == nil && (bestT.IsZero() || t.Before(bestT)) {
			best, bestT = x, t
		}
	}
	return best
}
