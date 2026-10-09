package trading

// Minute backfill from GreekSoft candles. While the gateway is down (a
// restart) no minute is recorded, and the Paper Sim / day history showed a
// hole (14:19 -> 14:22, 2026-10-09) -- its hedge checks simply skipped those
// minutes. GreekSoft keeps every 1-minute candle of the day, so once a
// minute (at :30, away from the minute-end work) the missing minutes of
// today's current-expiry chain are rebuilt from GreekSoft's closes:
//
//   - only minutes with NO record at all (a recorded minute is never
//     touched), 09:16 up to the last completed minute;
//   - strikes: ATM +/- gsBackfillStrikes of the neighbouring recorded
//     minutes, plus every strike recorded at the day's first minute (the
//     paper entry strike), tokens taken from those records;
//   - a strike without a trade in a minute keeps its last earlier close;
//   - synthetic future / ATM from the ATM pair (as at the live minute end);
//   - written to the day's import file, marked "GreekSoft backfill"
//     (shown as imported), so the Paper Sim re-runs its SL / TP / hedge
//     checks over those minutes too.
//
// Requests go out two at a time (GreekSoft serves about two at once).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	gsBackfillStrikes = 8
	gsBackfillSrc     = "GreekSoft backfill"
)

var gsBackfillBusy atomic.Bool

// gsBackfillNow fills today's missing minutes (no-op when none).
func (s *Service) gsBackfillNow() {
	p := s.gsCloseProvider()
	now := time.Now().In(lutIST())
	if p == nil || !sbLiveWindow(now) || !gsBackfillBusy.CompareAndSwap(false, true) {
		return
	}
	defer gsBackfillBusy.Store(false)
	day := now.Format("2006-01-02")
	have := lutLoadChainSet(day, "")
	if len(have) == 0 {
		return
	}
	last := now.Truncate(time.Minute)
	if hm := last.Hour()*100 + last.Minute(); hm > lutRecordUntil {
		last = time.Date(now.Year(), now.Month(), now.Day(), lutRecordUntil/100, lutRecordUntil%100, 0, 0, lutIST())
	}
	var times []string
	for t := time.Date(now.Year(), now.Month(), now.Day(), 9, 16, 0, 0, lutIST()); !t.After(last); t = t.Add(time.Minute) {
		times = append(times, t.Format("15:04"))
	}
	var missing []string
	for _, t := range times {
		if _, ok := have[t]; !ok {
			missing = append(missing, t)
		}
	}
	// The minute just ended may still be on its way in: never fill it.
	if n := len(missing); n > 0 && missing[n-1] == last.Format("15:04") {
		missing = missing[:n-1]
	}
	if len(missing) == 0 {
		return
	}

	// Strikes and tokens from the records around the gaps.
	recorded := make([]string, 0, len(have))
	for t := range have {
		recorded = append(recorded, t)
	}
	sort.Strings(recorded)
	nearest := func(t string) lutChainMinute {
		best, bd := lutChainMinute{}, math.MaxInt
		for _, r := range recorded {
			if d := absInt(lutHHMMMinutes(r) - lutHHMMMinutes(t)); d < bd {
				best, bd = have[r], d
			}
		}
		return best
	}
	type tk struct{ ce, pe int64 }
	strikes := map[float64]tk{}
	expiry := ""
	addRows := func(m lutChainMinute, band float64) {
		if expiry == "" {
			expiry = m.Expiry
		}
		for _, r := range m.Rows {
			if r.CEToken <= 0 || r.PEToken <= 0 {
				continue
			}
			if band < 0 || math.Abs(r.K-m.ATM) <= band*lutStrikeStep+1e-6 {
				strikes[r.K] = tk{r.CEToken, r.PEToken}
			}
		}
	}
	for _, t := range missing {
		addRows(nearest(t), gsBackfillStrikes)
	}
	if first, ok := have[recorded[0]]; ok { // the paper entry strike (09:16) stays priced
		addRows(first, 0)
	}
	if len(strikes) == 0 {
		return
	}

	// Every token's candles of the day, two requests at a time.
	ymd := now.Format("20060102")
	closes := map[int64]map[int64]float64{}
	var mu sync.Mutex
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, v := range strikes {
		for _, tok := range []int64{v.ce, v.pe} {
			wg.Add(1)
			go func(tok int64) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				gsAfterBoundary() // never inside the minute-end window
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				bars, err := p.MinuteCloses(ctx, tok, ymd)
				cancel()
				if err == nil {
					mu.Lock()
					closes[tok] = bars
					mu.Unlock()
				}
			}(tok)
		}
	}
	wg.Wait()
	closeAt := func(tok int64, b int64) float64 {
		bars := closes[tok]
		if v, ok := bars[b]; ok {
			return v
		}
		bestTs, best := int64(0), 0.0 // last close before b
		for ts, v := range bars {
			if ts%60 == 0 && ts < b && ts > bestTs {
				bestTs, best = ts, v
			}
		}
		return best
	}

	ks := make([]float64, 0, len(strikes))
	for k := range strikes {
		ks = append(ks, k)
	}
	sort.Float64s(ks)
	filled := 0
	for _, t := range missing {
		var hh, mm int
		if _, err := fmt.Sscanf(t, "%d:%d", &hh, &mm); err != nil {
			continue
		}
		b := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, lutIST()).Unix()
		m := lutChainMinute{Time: t, Ts: now.Format("15:04:05.000"), Expiry: expiry, Src: gsBackfillSrc, CloseSrc: "GreekSoft"}
		for _, k := range ks {
			v := strikes[k]
			ce, pe := closeAt(v.ce, b), closeAt(v.pe, b)
			if ce <= 0 && pe <= 0 {
				continue
			}
			m.Rows = append(m.Rows, lutChainRow{K: k, CEToken: v.ce, PEToken: v.pe, CE: ce, PE: pe, Src: gsBackfillSrc})
		}
		// Synthetic future / ATM from the ATM pair, starting at the
		// neighbouring record's ATM, re-derived until it settles.
		atm := nearest(t).ATM
		for i := 0; i < 3; i++ {
			var row *lutChainRow
			for j := range m.Rows {
				if math.Abs(m.Rows[j].K-atm) < 1e-6 {
					row = &m.Rows[j]
				}
			}
			if row == nil || row.CE <= 0 || row.PE <= 0 {
				break
			}
			m.Future = row.K + row.CE - row.PE
			next := math.Round(m.Future/lutStrikeStep) * lutStrikeStep
			if next == atm {
				break
			}
			atm = next
		}
		m.ATM = atm
		if m.Future <= 0 || len(m.Rows) == 0 {
			continue
		}
		if err := lutAppendJSON(lutChainImportFile(day, ""), m); err != nil {
			log.Printf("[BACKFILL] ⚠ %s not saved: %v", t, err)
			continue
		}
		filled++
	}
	if filled > 0 {
		log.Printf("[BACKFILL] %d missing minute(s) %s..%s rebuilt from GreekSoft candles (%d strikes) -- Paper Sim / history now include them (marked imported)",
			filled, missing[0], missing[len(missing)-1], len(ks))
	}
}

// lutAppendJSON appends one JSON line to a LUT data file.
func lutAppendJSON(path string, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(lutDataDir(), 0o755); err != nil {
		return err
	}
	return lutAppend(path, append(b, '\n'))
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// lutHHMMMinutes: "HH:MM" -> minutes since midnight.
func lutHHMMMinutes(t string) int {
	var h, m int
	fmt.Sscanf(t, "%d:%d", &h, &m)
	return h*60 + m
}
