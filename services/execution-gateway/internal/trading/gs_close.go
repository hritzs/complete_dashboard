package trading

// GreekSoft minute closes. GreekSoft builds its 1-minute candles from every
// trade; our NSE broadcast closes are the last LTP the snapshot feed showed
// before hh:mm:00, which missed the final trade often enough to be off by
// 0.40 (CE) / 0.52 (PE) on average at the ATM (max 2-3 pts, 109 minutes,
// 2026-10-09). The completed candle appears ~0.3-0.5 s after the minute
// (40-160 ms per request) and never changed afterwards (11:06 test).
//
// So every minute-end decision prices at GreekSoft's close: gsCloseAt asks
// GreekSoft for a token's candle ending at boundary b (hh:mm:00) and waits
// for it until a deadline; one request per token per minute is shared by
// every caller (LUT, each trade's monitor). If it has not arrived by the
// deadline the caller keeps our feed close and logs that.

import (
	"context"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

// MinuteCloseProvider: the day's 1-minute candle closes of one instrument
// keyed by bar timestamp (unix s; a completed candle is stamped at its end).
type MinuteCloseProvider interface {
	MinuteCloses(ctx context.Context, token int64, date string) (map[int64]float64, error)
}

const (
	gsClosePoll     = 150 * time.Millisecond
	gsCloseDeadline = 2 * time.Second // after the boundary: then the feed close is used
)

type gsCloseKey struct {
	token int64
	b     int64
}

var gsc struct {
	mu       sync.Mutex
	got      map[gsCloseKey]float64
	inflight map[gsCloseKey]chan struct{}
	day      string
	recent   map[int64]time.Time // tokens asked for lately (kept warm)
	warmOnce sync.Once
}

// gsWarmLoop pre-loads GreekSoft candles so the boundary request is fast
// (the first request for a token after a pause took ~0.9 s; 11:43
// 2026-10-09 right after a restart timed out). Once a minute (:50, ~10 s
// before the boundary; the boundary fetch itself keeps the ATM / held legs
// hot) and once at start it requests:
//   - CE + PE of the ATM +/- gsWarmStrikes strikes of the nearest expiry
//     (an ATM that jumps at the boundary is already warm),
//   - every leg the open trades hold (portfolio view),
//   - every token asked for in the last 10 minutes.
const gsWarmStrikes = 3

func (s *Service) gsWarmLoop() {
	s.gsWarmNow()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for now := range t.C {
		if now.Second() == 50 {
			s.gsWarmNow()
		}
	}
}

func (s *Service) gsWarmNow() {
	p := s.gsCloseProvider()
	if p == nil || !sbLiveWindow(time.Now().In(lutIST())) {
		return
	}
	toks := map[int64]bool{}
	gsc.mu.Lock()
	for tok, at := range gsc.recent {
		if time.Since(at) < 10*time.Minute {
			toks[tok] = true
		} else {
			delete(gsc.recent, tok)
		}
	}
	gsc.mu.Unlock()
	if s.Snapshot != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if c, err := s.Snapshot.GetOptionChain(ctx, lutSymbol, ""); err == nil && c != nil {
			atm := c.ATM
			if S := lutUnderlying(c); S > 0 {
				atm = math.Round(S/lutStrikeStep) * lutStrikeStep
			}
			for _, r := range c.Chain {
				if math.Abs(r.Strike-atm) <= gsWarmStrikes*lutStrikeStep+1e-6 {
					toks[r.CEToken], toks[r.PEToken] = r.CEToken > 0, r.PEToken > 0
				}
			}
		}
		cancel()
	}
	pm.mu.Lock()
	for _, pos := range pm.view.Positions {
		if pos.NetQty != 0 && pos.Token > 0 {
			toks[pos.Token] = true
		}
	}
	pm.mu.Unlock()
	day := time.Now().In(lutIST()).Format("20060102")
	for tok, ok := range toks {
		if !ok || tok <= 0 {
			continue
		}
		go func(tok int64) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if bars, err := p.MinuteCloses(ctx, tok, day); err == nil {
				gsc.mu.Lock()
				if gsc.got != nil && gsc.day == day {
					for ts, c := range bars {
						if ts%60 == 0 {
							gsc.got[gsCloseKey{tok, ts}] = c
						}
					}
				}
				gsc.mu.Unlock()
			}
		}(tok)
	}
}

// gsClosesOn: GreekSoft closes are the default for every minute-end
// decision (verified against the terminal 2026-10-09); GS_MINUTE_CLOSE=0
// switches back to our feed closes.
func gsClosesOn() bool { return strings.TrimSpace(os.Getenv("GS_MINUTE_CLOSE")) != "0" }

func (s *Service) gsCloseProvider() MinuteCloseProvider {
	if s.BrokerFactory == nil || !gsClosesOn() {
		return nil
	}
	ex, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		return nil
	}
	p, _ := ex.(MinuteCloseProvider)
	return p
}

// gsCloseAt returns GreekSoft's close of token's candle ending at b, waiting
// until b + gsCloseDeadline at most. ok=false: not available (use the feed).
func (s *Service) gsCloseAt(token int64, b time.Time) (float64, bool) {
	if token <= 0 {
		return 0, false
	}
	k := gsCloseKey{token, b.Unix()}
	day := b.In(lutIST()).Format("20060102")
	gsc.warmOnce.Do(func() { go s.gsWarmLoop() })
	gsc.mu.Lock()
	if gsc.got == nil || gsc.day != day {
		gsc.got, gsc.inflight, gsc.day = map[gsCloseKey]float64{}, map[gsCloseKey]chan struct{}{}, day
	}
	if gsc.recent == nil {
		gsc.recent = map[int64]time.Time{}
	}
	gsc.recent[token] = time.Now()
	if v, ok := gsc.got[k]; ok {
		gsc.mu.Unlock()
		return v, true
	}
	if ch, busy := gsc.inflight[k]; busy { // another caller is fetching it
		gsc.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(time.Until(b.Add(gsCloseDeadline + 500*time.Millisecond))):
		}
		gsc.mu.Lock()
		v, ok := gsc.got[k]
		gsc.mu.Unlock()
		return v, ok
	}
	ch := make(chan struct{})
	gsc.inflight[k] = ch
	gsc.mu.Unlock()
	defer func() {
		gsc.mu.Lock()
		delete(gsc.inflight, k)
		gsc.mu.Unlock()
		close(ch)
	}()

	p := s.gsCloseProvider()
	if p == nil {
		return 0, false
	}
	deadline := b.Add(gsCloseDeadline)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
		bars, err := p.MinuteCloses(ctx, token, day)
		cancel()
		if err == nil {
			gsc.mu.Lock()
			for ts, c := range bars { // keep every completed candle seen (stamped hh:mm:00)
				if ts%60 == 0 {
					gsc.got[gsCloseKey{token, ts}] = c
				}
			}
			v, ok := gsc.got[k]
			gsc.mu.Unlock()
			if ok {
				return v, true
			}
		}
		if time.Now().Add(gsClosePoll).After(deadline) {
			if err != nil {
				log.Printf("[GS-CLOSE] token %d %s: %v -- feed close used", token, b.Format("15:04"), err)
			}
			return 0, false
		}
		time.Sleep(gsClosePoll)
	}
}

// gsApplyCloses replaces the closes of the given tokens in cc (a chain
// already priced at our feed closes for boundary b) with GreekSoft's, all
// fetched in parallel; the synthetic future / ATM are re-derived when the
// ATM pair changed. Returns how many legs came from GreekSoft and the feed
// closes they replaced (token -> feed price), for the log.
func (s *Service) gsApplyCloses(cc *OptionChainSnapshot, b time.Time, tokens map[int64]bool) (int, map[int64]float64) {
	feed := map[int64]float64{}
	if cc == nil || len(tokens) == 0 {
		return 0, feed
	}
	type res struct {
		tok int64
		px  float64
		ok  bool
	}
	ch := make(chan res, len(tokens))
	for t := range tokens {
		go func(t int64) {
			px, ok := s.gsCloseAt(t, b)
			ch <- res{t, px, ok}
		}(t)
	}
	got := map[int64]float64{}
	for range tokens {
		r := <-ch
		if r.ok {
			got[r.tok] = r.px
		}
	}
	n := 0
	for i := range cc.Chain {
		row := &cc.Chain[i]
		if px, ok := got[row.CEToken]; ok && row.CEToken > 0 {
			feed[row.CEToken], row.CELtp = row.CELtp, px
			n++
		}
		if px, ok := got[row.PEToken]; ok && row.PEToken > 0 {
			feed[row.PEToken], row.PELtp = row.PELtp, px
			n++
		}
	}
	if n > 0 {
		for i := range cc.Chain {
			r := &cc.Chain[i]
			if r.Strike == cc.ATM && r.CELtp > 0 && r.PELtp > 0 {
				cc.SyntheticFuture = r.Strike + r.CELtp - r.PELtp
				cc.SyntheticSpot = cc.SyntheticFuture
				cc.ATM = math.Round(cc.SyntheticFuture/lutStrikeStep) * lutStrikeStep
			}
		}
	}
	return n, feed
}

// gsHaveAll: every token's GreekSoft close for b is already in (no wait).
func gsHaveAll(tokens map[int64]bool, b time.Time) bool {
	if len(tokens) == 0 {
		return false
	}
	gsc.mu.Lock()
	defer gsc.mu.Unlock()
	for t := range tokens {
		if _, ok := gsc.got[gsCloseKey{t, b.Unix()}]; !ok {
			return false
		}
	}
	return true
}

// atmTokens: the ATM CE / PE tokens of a chain.
func atmTokens(cc *OptionChainSnapshot) map[int64]bool {
	out := map[int64]bool{}
	if cc == nil {
		return out
	}
	for _, r := range cc.Chain {
		if r.Strike == cc.ATM {
			out[r.CEToken], out[r.PEToken] = true, true
		}
	}
	return out
}
