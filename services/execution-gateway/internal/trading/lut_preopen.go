package trading

// Pre-open chain for the LUT tab. Before 09:15 (or with no live chain)
// there are no live prices, so the tab shows the previous trading day's
// last recorded minute (<= 15:29) exactly as recorded -- future and every
// strike's CE / PE at that close, not re-priced (opening gap 0 unless a
// manual future / 09:16 override is set).
//
// It only feeds the display (the "next check" evaluation, its grid and the
// sell zone). It is never recorded and never an entry. Manual what-if
// values override it field by field (e.g. a future from GIFT Nifty).

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type lutPreopen struct {
	key   string // day|expiry it was built for
	chain *OptionChainSnapshot
	note  string
}

// lutPreopenLocked returns (cached per day + expiry) the estimate chain for
// today's 09:16 from the last trading day recorded before today. expiry is
// today's expiry (blank = whatever was current then). Caller holds e.mu.
func (e *lutEngine) lutPreopenLocked(now time.Time, expiry string) (*OptionChainSnapshot, string) {
	prevFut := 0.0
	if e.inputsOK {
		prevFut = e.inputs.PrevFuture
	}
	key := fmt.Sprintf("%s|%s|%.2f", now.Format("2006-01-02"), expiry, prevFut)
	if e.preopen.key == key && time.Since(e.preopenAt) < 5*time.Minute {
		return e.preopen.chain, e.preopen.note
	}
	e.preopenAt = time.Now()
	e.preopen = lutBuildPreopen(now, expiry, prevFut)
	e.preopen.key = key
	return e.preopen.chain, e.preopen.note
}

// lutBuildPreopen takes the latest recorded day before today. When the
// day's inputs are loaded (prevFuture > 0), a day whose closing future is
// more than 0.5% away from them is passed over first -- a recorded
// Saturday mock session is not "yesterday's close" -- and used only if no
// earlier day fits either.
func lutBuildPreopen(now time.Time, expiry string, prevFuture float64) lutPreopen {
	var fallback *lutPreopen
	for back := 1; back <= 10; back++ {
		day := now.AddDate(0, 0, -back).Format("2006-01-02")
		for _, set := range []string{"", "next"} { // yesterday's "next" = today's current after an expiry day
			mins := lutLoadChainSet(day, set)
			if len(mins) == 0 {
				continue
			}
			var times []string
			for t := range mins {
				if t <= "15:29" {
					times = append(times, t)
				}
			}
			if len(times) == 0 {
				continue
			}
			sort.Strings(times)
			m := mins[times[len(times)-1]]
			if expiry != "" && m.Expiry != "" && m.Expiry != expiry {
				continue
			}
			c, note, ok := lutRepriceForToday(m, day, now)
			if !ok {
				continue
			}
			if prevFuture > 0 && math.Abs(m.Future-prevFuture)/prevFuture > 0.005 {
				if fallback == nil {
					fallback = &lutPreopen{chain: c, note: note + fmt.Sprintf(" -- NOTE: that close is %.2f%% away from the daily inputs' previous future %.2f", (m.Future-prevFuture)/prevFuture*100, prevFuture)}
				}
				continue
			}
			return lutPreopen{chain: c, note: note}
		}
	}
	if fallback != nil {
		return *fallback
	}
	return lutPreopen{note: "no recorded chain from a previous trading day -- enter manual values to check"}
}

// lutRepriceForToday turns a recorded closing minute into the pre-open
// chain: the previous close exactly as recorded (owner, 19 Oct: show the
// previous closing straddle, not a re-priced estimate).
func lutRepriceForToday(m lutChainMinute, day string, now time.Time) (*OptionChainSnapshot, string, bool) {
	exp, err := lutParseExpiry(m.Expiry)
	if err != nil || m.Future <= 0 {
		return nil, "", false
	}
	loc := now.Location()
	at0916 := time.Date(now.Year(), now.Month(), now.Day(), 9, 16, 0, 0, loc)
	if !exp.After(at0916.Add(-24 * time.Hour)) {
		return nil, "", false // that expiry is gone
	}
	// As recorded: the previous close is shown, not re-priced.
	S := m.Future
	c := &OptionChainSnapshot{Symbol: lutSymbol, Expiry: m.Expiry, SyntheticFuture: S, SyntheticSpot: S,
		ATM: math.Round(S/lutStrikeStep) * lutStrikeStep}
	var atmY float64
	for _, r := range m.Rows {
		row := OptionChainRow{Strike: r.K, CEToken: r.CEToken, PEToken: r.PEToken, CELtp: r.CE, PELtp: r.PE}
		if row.CELtp <= 0 && row.PELtp <= 0 {
			continue
		}
		row.IsATM = r.K == c.ATM
		if row.IsATM {
			atmY = r.CE + r.PE
		}
		c.Chain = append(c.Chain, row)
	}
	if len(c.Chain) == 0 {
		return nil, "", false
	}
	note := fmt.Sprintf("PRE-OPEN: previous close %s %s as recorded (not re-priced): future %.2f, ATM %.0f straddle %.2f. The sell levels below are where today's tables sell; live prices from 09:15",
		day, m.Time, S, c.ATM, atmY)
	return c, note, true
}
