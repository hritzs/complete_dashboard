package trading

// GreekSoft closes for POSITION strikes in the stored minutes. At the minute
// end only the ATM pair (and a live trade's legs) is fetched from GreekSoft
// -- execution first; every other strike of the stored chain keeps our feed
// close. The Paper Sim prices its position strike from those rows, so a
// build sitting away from the ATM ran on feed closes (2026-10-12 09:24: the
// sim had 22650 at 54.50 / 161.35, GreekSoft's candles 53.50 / 164.70).
//
// Once a minute (at :30, away from the minute-end work) this fetches the
// day's candles of every strike that has been the ATM today (a sim's or a
// trade's position strike was the ATM at its entry) and writes their closes
// per minute to a small overlay file; lutLoadChainSet lays it over the
// stored rows. No request is added at the boundary, a stored minute is
// never rewritten, and a minute without a GreekSoft candle keeps its feed
// close.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// gsOverlay: minute "HH:MM" -> strike ("22650") -> [CE close, PE close].
type gsOverlay map[string]map[string][2]float64

var gsOverlayBusy atomic.Bool

// gsOverlayRetry: the last run missed something (a failed request, or it
// has not run yet today): the :30 pass runs it again. A clean :03 run is
// the only one that minute -- no repeated load on GreekSoft.
var gsOverlayRetry atomic.Bool

func init() { gsOverlayRetry.Store(true) }

// gsOverlayFile: the overlay of a set ("" current expiry / "next").
func gsOverlayFile(day, set string) string {
	if set == "" || set == "current" {
		return filepath.Join(lutDataDir(), day+"_chain_gs.json")
	}
	return filepath.Join(lutDataDir(), day+"_chain_"+set+"_gs.json")
}

func gsOverlayKey(k float64) string { return fmt.Sprintf("%.0f", k) }

// gsOverlayLoad reads the day's overlay (nil when there is none).
func gsOverlayLoad(day, set string) gsOverlay {
	b, err := os.ReadFile(gsOverlayFile(day, set))
	if err != nil {
		return nil
	}
	var ov gsOverlay
	if json.Unmarshal(b, &ov) != nil {
		return nil
	}
	return ov
}

// gsOverlayApply lays GreekSoft's closes over the stored rows of a day.
//
// The current expiry's stored future / ATM already come from GreekSoft's ATM
// closes (taken at the boundary). Another set ("next") is stored entirely on
// feed closes, so there the synthetic future and the ATM are re-derived from
// the overlaid ATM pair too -- each expiry on its own closes.
func gsOverlayApply(day, set string, mins map[string]lutChainMinute) {
	gsOverlayApplyWith(gsOverlayLoad(day, set), set, mins)
}

// gsOverlayApplyWith: gsOverlayApply on a loaded overlay. A strike it
// re-prices drops the feed's greeks / IVs (taken at the feed's price at
// another moment): the simulation recomputes them at the close, one IV per
// strike from the OTM leg as the backtest does (simStrikeIV). Mixing close
// prices with feed greeks flipped the 16 Oct 10:19 minute-end hedge.
func gsOverlayApplyWith(ov gsOverlay, set string, mins map[string]lutChainMinute) {
	if len(ov) == 0 {
		return
	}
	other := !(set == "" || set == "current")
	for t, m := range mins {
		by, ok := ov[t]
		if !ok {
			continue
		}
		for i := range m.Rows {
			if px, ok := by[gsOverlayKey(m.Rows[i].K)]; ok {
				if px[0] > 0 {
					m.Rows[i].CE = px[0]
				}
				if px[1] > 0 {
					m.Rows[i].PE = px[1]
				}
				r := &m.Rows[i]
				r.CEDelta, r.CEGamma, r.PEDelta, r.PEGamma, r.CEIV, r.PEIV = 0, 0, 0, 0, 0, 0
			}
		}
		if other {
			atm := m.ATM
			for i := 0; i < 3; i++ {
				px, ok := by[gsOverlayKey(atm)]
				if !ok || px[0] <= 0 || px[1] <= 0 {
					break
				}
				m.Future = atm + px[0] - px[1]
				next := math.Round(m.Future/lutStrikeStep) * lutStrikeStep
				if next == atm {
					break
				}
				atm = next
			}
			if _, ok := by[gsOverlayKey(atm)]; ok {
				m.ATM = atm
			}
		}
		mins[t] = m
	}
}

// gsOverlayHold: how long a just-ended minute is held back from the screens
// while its position-strike closes are still on their way.
const gsOverlayHold = 45 * time.Second

// gsOverlayPending: minute t ("HH:MM") of day ended less than gsOverlayHold
// ago and its GreekSoft closes are not in the overlay yet. Screens show a
// minute only once its data is final (the UI comes after the backend work),
// so they skip it until then.
func gsOverlayPending(day, t string, ov gsOverlay) bool {
	if !gsClosesOn() {
		return false
	}
	b, err := time.ParseInLocation("2006-01-02 15:04", day+" "+t, lutIST())
	if err != nil {
		return false
	}
	age := time.Since(b)
	if age < 0 || age > gsOverlayHold {
		return false
	}
	_, ok := ov[t]
	return !ok
}

// gsOverlayNow rebuilds today's overlays: the current expiry and the next
// weekly expiry, each from its own stored chain and its own tokens.
func (s *Service) gsOverlayNow() {
	p := s.gsCloseProvider()
	now := time.Now().In(lutIST())
	if p == nil || !sbLiveWindow(now) || !gsOverlayBusy.CompareAndSwap(false, true) {
		return
	}
	defer gsOverlayBusy.Store(false)
	failed := 0
	for _, set := range []string{"", "next"} {
		failed += s.gsOverlayBuild(p, now, set)
	}
	gsOverlayRetry.Store(failed > 0)
}

// gsOverlayBuild rebuilds one set's overlay; returns how many candle
// requests failed.
func (s *Service) gsOverlayBuild(p MinuteCloseProvider, now time.Time, set string) int {
	day := now.Format("2006-01-02")
	mins := lutLoadChainFile(lutChainFile(day, set)) // the stored rows as they are
	if len(mins) == 0 {
		return 0
	}
	// Every strike that was the ATM today, with its tokens.
	type tk struct{ ce, pe int64 }
	strikes := map[float64]tk{}
	for _, m := range mins {
		if _, ok := strikes[m.ATM]; ok || m.ATM <= 0 {
			continue
		}
		for _, r := range m.Rows {
			if math.Abs(r.K-m.ATM) < 1e-6 && r.CEToken > 0 && r.PEToken > 0 {
				strikes[m.ATM] = tk{r.CEToken, r.PEToken}
			}
		}
	}
	if set != "" {
		// Stored on feed closes, this set's ATM can sit one strike off the
		// GreekSoft one: the neighbours are fetched too so the re-derived
		// ATM always has its pair.
		for _, m := range mins {
			for _, r := range m.Rows {
				if d := math.Abs(r.K - m.ATM); d > 1e-6 && d <= lutStrikeStep+1e-6 && r.CEToken > 0 && r.PEToken > 0 {
					if _, ok := strikes[r.K]; !ok {
						strikes[r.K] = tk{r.CEToken, r.PEToken}
					}
				}
			}
		}
	}
	if len(strikes) == 0 {
		return 0
	}
	ymd := now.Format("20060102")
	closes := map[int64]map[int64]float64{}
	var mu sync.Mutex
	sem := make(chan struct{}, 2) // GreekSoft serves about two requests at a time
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
	// A request that failed is tried once more, one at a time; what still
	// fails is logged (its earlier values stay, see below).
	failed := 0
	for k, v := range strikes {
		for _, leg := range []struct {
			name string
			tok  int64
		}{{"CE", v.ce}, {"PE", v.pe}} {
			if _, ok := closes[leg.tok]; ok {
				continue
			}
			gsAfterBoundary()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			bars, err := p.MinuteCloses(ctx, leg.tok, ymd)
			cancel()
			if err != nil {
				failed++
				log.Printf("[GS-OVERLAY] ⚠ %s%.0f %s (token %d): candles not fetched (%v) -- earlier values kept", map[bool]string{true: "", false: set + " "}[set == ""], k, leg.name, leg.tok, err)
				continue
			}
			closes[leg.tok] = bars
		}
	}

	// Start from what is already known: a candle request that failed this
	// run (cold connections right after a restart) must not blank a side
	// that an earlier run had.
	ov := gsOverlayLoad(day, set)
	if ov == nil {
		ov = gsOverlay{}
	}
	times := make([]string, 0, len(mins))
	for t := range mins {
		times = append(times, t)
	}
	sort.Strings(times)
	for _, t := range times {
		var hh, mm int
		if _, err := fmt.Sscanf(t, "%d:%d", &hh, &mm); err != nil {
			continue
		}
		b := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, lutIST()).Unix()
		for k, v := range strikes {
			ce, pe := closes[v.ce][b], closes[v.pe][b] // the candle ending at this minute, exactly
			old := ov[t][gsOverlayKey(k)]
			if ce <= 0 {
				ce = old[0]
			}
			if pe <= 0 {
				pe = old[1]
			}
			if ce <= 0 && pe <= 0 {
				continue
			}
			if ov[t] == nil {
				ov[t] = map[string][2]float64{}
			}
			ov[t][gsOverlayKey(k)] = [2]float64{ce, pe}
		}
	}
	if len(ov) == 0 {
		return failed
	}
	bts, _ := json.Marshal(ov)
	if err := os.MkdirAll(lutDataDir(), 0o755); err == nil {
		if err = sbWriteAtomic(gsOverlayFile(day, set), bts); err != nil {
			log.Printf("[GS-OVERLAY] ⚠ %s not saved: %v", lutChainFile(day, set), err)
		}
	}
	return failed
}
