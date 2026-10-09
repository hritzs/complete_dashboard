package trading

// Pre-open estimate for the LUT tab. Before 09:15 (or with no live chain)
// there are no live prices, so the tab would show nothing. Instead it shows
// an ESTIMATE of today's 09:16 check built from the previous trading day's
// last recorded minute (<= 15:29):
//
//   - future = yesterday's closing synthetic future (opening gap 0 unless
//     a manual future / 09:16 override is set);
//   - every strike's CE / PE re-priced from its own implied vol at that
//     close to TODAY's 09:16 time to expiry (the overnight decay included).
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
	key := now.Format("2006-01-02") + "|" + expiry
	if e.preopen.key == key && time.Since(e.preopenAt) < 5*time.Minute {
		return e.preopen.chain, e.preopen.note
	}
	e.preopenAt = time.Now()
	e.preopen = lutBuildPreopen(now, expiry)
	e.preopen.key = key
	return e.preopen.chain, e.preopen.note
}

func lutBuildPreopen(now time.Time, expiry string) lutPreopen {
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
			if c, note, ok := lutRepriceForToday(m, day, now); ok {
				return lutPreopen{chain: c, note: note}
			}
		}
	}
	return lutPreopen{note: "no recorded chain from a previous trading day -- enter manual values to check"}
}

// lutRepriceForToday turns a recorded closing minute into today's 09:16
// estimate: same future, each option at its own closing IV, today's T.
func lutRepriceForToday(m lutChainMinute, day string, now time.Time) (*OptionChainSnapshot, string, bool) {
	exp, err := lutParseExpiry(m.Expiry)
	if err != nil || m.Future <= 0 {
		return nil, "", false
	}
	loc := now.Location()
	yd, _ := time.ParseInLocation("2006-01-02", day, loc)
	hh, mm := 15, 29
	fmt.Sscanf(m.Time, "%d:%d", &hh, &mm)
	closeAt := time.Date(yd.Year(), yd.Month(), yd.Day(), hh, mm, 0, 0, loc)
	at0916 := time.Date(now.Year(), now.Month(), now.Day(), 9, 16, 0, 0, loc)
	Ty := math.Max(lutRawDTE(closeAt, exp)/365.0, 1e-5)
	Tt := math.Max(lutRawDTE(at0916, exp)/365.0, 1e-5)
	if !exp.After(at0916.Add(-24 * time.Hour)) {
		return nil, "", false // that expiry is gone
	}
	S := m.Future
	c := &OptionChainSnapshot{Symbol: lutSymbol, Expiry: m.Expiry, SyntheticFuture: S, SyntheticSpot: S,
		ATM: math.Round(S/lutStrikeStep) * lutStrikeStep}
	var atmY, atmT float64
	for _, r := range m.Rows {
		row := OptionChainRow{Strike: r.K, CEToken: r.CEToken, PEToken: r.PEToken}
		if iv, ok := lutImpliedVol(true, r.CE, S, r.K, Ty); ok && iv > 0 {
			row.CELtp = math.Round(lutBSPrice(true, S, r.K, Tt, iv)*20) / 20
		}
		if iv, ok := lutImpliedVol(false, r.PE, S, r.K, Ty); ok && iv > 0 {
			row.PELtp = math.Round(lutBSPrice(false, S, r.K, Tt, iv)*20) / 20
		}
		if row.CELtp <= 0 && row.PELtp <= 0 {
			continue
		}
		row.IsATM = r.K == c.ATM
		if row.IsATM {
			atmY, atmT = r.CE+r.PE, row.CELtp+row.PELtp
		}
		c.Chain = append(c.Chain, row)
	}
	if len(c.Chain) == 0 {
		return nil, "", false
	}
	note := fmt.Sprintf("PRE-OPEN ESTIMATE from %s %s close: future %.2f, ATM %.0f straddle %.2f at the close -> %.2f re-priced for today's 09:16 (same IVs, today's time to expiry); opening gap 0 unless a manual future is set",
		day, m.Time, S, c.ATM, atmY, atmT)
	return c, note, true
}
