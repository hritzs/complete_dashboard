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
	"strings"
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
	// The current expiry and the next weekly expiry, each from its own
	// records and tokens.
	for _, set := range []string{"", "next"} {
		s.gsBackfillSet(p, now, set)
	}
}

func (s *Service) gsBackfillSet(p MinuteCloseProvider, now time.Time, set string) {
	day := now.Format("2006-01-02")
	gsBackfillPurge(day, set) // a wrong fill from before is dropped and filled again
	have := lutLoadChainSet(day, set)
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
	// A minute GreekSoft could not price in 5 runs is given up for the day
	// (each run asks for every strike's candles again).
	gsBackfillTries.Lock()
	tryable := missing[:0:0]
	for _, t := range missing {
		if gsBackfillTries.n[day+"|"+set+"|"+t] < 5 {
			tryable = append(tryable, t)
		}
	}
	gsBackfillTries.Unlock()
	missing = tryable
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
			if d := absInt(lutMinutesOfDay(r) - lutMinutesOfDay(t)); d < bd {
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
	retried := map[int64]bool{}
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
	// A reply whose newest bar is older than the minute being filled is cut
	// off (GreekSoft does that now and then) or the strike is dead: asked
	// once more, and if still short the strike is left out of that minute
	// rather than priced from hours earlier.
	closeAt := func(tok int64, b int64) float64 {
		px, fresh := gsBarCloseAt(closes[tok], b)
		if fresh {
			return px
		}
		mu.Lock()
		again := !retried[tok]
		retried[tok] = true
		mu.Unlock()
		if again {
			gsAfterBoundary()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			bars, err := p.MinuteCloses(ctx, tok, ymd)
			cancel()
			if err == nil {
				mu.Lock()
				closes[tok] = bars
				mu.Unlock()
				if px, fresh = gsBarCloseAt(bars, b); fresh {
					return px
				}
			}
		}
		return 0
	}

	ks := make([]float64, 0, len(strikes))
	for k := range strikes {
		ks = append(ks, k)
	}
	sort.Float64s(ks)
	filled := 0
	done := map[string]bool{}
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
		if ref := gsBackfillRef(day, set, t); ref > 0 && math.Abs(m.Future-ref)/ref > gsBackfillMaxJump {
			log.Printf("[BACKFILL] %s %s NOT written: future %.2f from the %.0f pair is %.2f%% away from the recorded minutes around it (%.2f) -- left missing, tried again next minute",
				gsBackfillName(set), t, m.Future, atm, (m.Future-ref)/ref*100, ref)
			continue
		}
		if err := lutAppendJSON(lutChainImportFile(day, set), m); err != nil {
			log.Printf("[BACKFILL] ⚠ %s not saved: %v", t, err)
			continue
		}
		filled++
		done[t] = true
	}
	gsBackfillTries.Lock()
	for _, t := range missing {
		if !done[t] {
			gsBackfillTries.n[day+"|"+set+"|"+t]++
		}
	}
	gsBackfillTries.Unlock()
	if filled > 0 {
		log.Printf("[BACKFILL] %s expiry: %d missing minute(s) %s..%s rebuilt from GreekSoft candles (%d strikes) -- Paper Sim / history now include them (marked imported)",
			map[bool]string{true: "current", false: set}[set == ""], filled, missing[0], missing[len(missing)-1], len(ks))
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

// lutMinutesOfDay: "HH:MM" -> minutes since midnight.
func lutMinutesOfDay(t string) int {
	var h, m int
	fmt.Sscanf(t, "%d:%d", &h, &m)
	return h*60 + m
}

// gsBackfillTries counts the runs a missing minute could not be filled in.
var gsBackfillTries = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

// gsBackfillMaxJump: a filled minute's future may not sit further than this
// from the RECORDED minute nearest to it (a gap is a minute or two wide;
// 0.6% is ~135 NIFTY points).
const gsBackfillMaxJump = 0.006

func gsBackfillName(set string) string {
	if set == "" {
		return "current expiry"
	}
	return set + " expiry"
}

// gsBarCloseAt is "the last trade before boundary b" out of one get_ohlc
// reply. A finished bar is stamped when it was closed -- exactly b on a
// busy strike, some seconds (or minutes) after b on a quiet one -- and the
// newest bar is still forming. So: the first finished bar stamped at / after
// b, else the newest bar. fresh is false when the newest bar is more than
// 3 minutes older than b: the reply is cut off or the strike did not trade,
// and the price would come from long before.
func gsBarCloseAt(bars map[int64]float64, b int64) (px float64, fresh bool) {
	keys := make([]int64, 0, len(bars))
	for ts, c := range bars {
		if c > 0 {
			keys = append(keys, ts)
		}
	}
	if len(keys) == 0 {
		return 0, false
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	newest := keys[len(keys)-1]
	if v, ok := bars[b]; ok && v > 0 { // the candle ending exactly there
		return v, true
	}
	px = bars[newest]
	for _, ts := range keys[:len(keys)-1] {
		if ts >= b {
			px = bars[ts]
			break
		}
	}
	return px, newest >= b-180
}

// gsBackfillRef is the future of the minute RECORDED live nearest to t
// (0 when none within 5 minutes).
func gsBackfillRef(day, set, t string) float64 {
	rec := lutLoadChainFile(lutChainFile(day, set))
	ref, bd := 0.0, 6
	for rt, m := range rec {
		if d := absInt(lutMinutesOfDay(rt) - lutMinutesOfDay(t)); d < bd && m.Future > 0 {
			ref, bd = m.Future, d
		}
	}
	return ref
}

// gsBackfillPurge removes backfilled minutes whose future is off the
// recorded minutes around them (seen 2026-10-13: next-week 09:37 filled
// with 09:15 prices on two strikes, future 22375 between recorded 22549 and
// 22575, which then drove a false hedge and stop in Paper Sim). The dropped
// lines are kept in <import file>.rejected; the minute is filled again.
func gsBackfillPurge(day, set string) {
	path := lutChainImportFile(day, set)
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return
	}
	var keep, bad []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var m lutChainMinute
		if json.Unmarshal([]byte(line), &m) == nil && m.Src == gsBackfillSrc && m.Future > 0 {
			if ref := gsBackfillRef(day, set, m.Time); ref > 0 && math.Abs(m.Future-ref)/ref > gsBackfillMaxJump {
				bad = append(bad, line)
				log.Printf("[BACKFILL] %s %s dropped: stored future %.2f is %.2f%% away from the recorded minutes around it (%.2f) -- will be filled again",
					gsBackfillName(set), m.Time, m.Future, (m.Future-ref)/ref*100, ref)
				continue
			}
		}
		keep = append(keep, line)
	}
	if len(bad) == 0 {
		return
	}
	if f, err := os.OpenFile(path+".rejected", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString(strings.Join(bad, "\n") + "\n")
		f.Close()
	}
	out := ""
	if len(keep) > 0 {
		out = strings.Join(keep, "\n") + "\n"
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(out), 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}
