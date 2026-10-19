package trading

// Next week's sell levels for the LUT tab (display only): the same 09:16 ..
// 09:20 rows as today's, for the NEXT expiry -- each minute at that expiry's
// own time to expiry and business days, on that expiry's ATM straddle and
// future. The daily inputs (previous straddle, IDVs, opening gap) are
// today's: the sheet only has the current expiry's, so this says where the
// tables would sell next week's straddle on today's inputs.
//
// "Now" for next week: before 09:15 the previous trading day's last recorded
// next-expiry minute (<= 15:29, as recorded); from 09:15 today's latest
// recorded next-expiry minute.

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// lutLastNextMinute: the latest recorded next-expiry minute of day (<= 15:29
// when close is set), skipping the current expiry.
func lutLastNextMinute(day string, close bool, curExpiry string) (lutChainMinute, bool) {
	mins := lutLoadChainSet(day, "next")
	var times []string
	for t, m := range mins {
		if (!close || t <= "15:29") && m.Expiry != "" && m.Expiry != curExpiry && m.Future > 0 {
			times = append(times, t)
		}
	}
	if len(times) == 0 {
		return lutChainMinute{}, false
	}
	sort.Strings(times)
	return mins[times[len(times)-1]], true
}

func (e *lutEngine) lutNextWeekLevels(now time.Time, day string, hhmm int, shown lutCtx, curExpiry string) ([]LUTSellLevel, string, string) {
	if shown.Ev == nil {
		return nil, "", ""
	}
	var m lutChainMinute
	ok, basis := false, ""
	if hhmm >= 915 {
		if m, ok = lutLastNextMinute(day, false, curExpiry); ok {
			basis = "today " + m.Time + " (recorded next-expiry minute)"
		}
	}
	if !ok {
		for back := 1; back <= 10 && !ok; back++ {
			d := now.AddDate(0, 0, -back).Format("2006-01-02")
			if m, ok = lutLastNextMinute(d, true, curExpiry); ok {
				basis = d + " " + m.Time + " close (as recorded)"
			}
		}
	}
	if !ok {
		return nil, "", ""
	}
	exp, err := lutParseExpiry(m.Expiry)
	if err != nil || !exp.After(now) {
		return nil, "", ""
	}
	atm := math.Round(m.Future/lutStrikeStep) * lutStrikeStep
	straddle := 0.0
	for _, r := range m.Rows {
		if r.K == atm && r.CE > 0 && r.PE > 0 {
			straddle = r.CE + r.PE
		}
	}
	c := shown
	ev := *shown.Ev
	ev.Straddle = straddle
	c.Ev, c.K = &ev, atm
	c.M.Underlying = m.Future
	open := time.Date(now.Year(), now.Month(), now.Day(), 9, 16, 0, 0, now.Location())
	c.M.BusDays = lutBusDays(now, int(math.Ceil(lutRawDTE(open, exp))))
	levels := lutSellLevelsFor(e.set, nil, now, c, m.Expiry, false, "next week: "+basis, false)
	return levels, m.Expiry, fmt.Sprintf("%s · future %.2f · ATM %.0f straddle %.2f · %d business day(s) to expiry · today's daily inputs",
		basis, m.Future, atm, straddle, c.M.BusDays)
}
