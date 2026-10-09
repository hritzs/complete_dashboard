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
	// While a candle is missing a new request goes out every gsClosePoll
	// with up to gsCloseInflight overlapping per token, so the close is seen
	// ~one request time after GreekSoft publishes it (sequential request +
	// 150 ms sleep missed it by up to ~250 ms).
	gsClosePoll     = 60 * time.Millisecond
	gsCloseInflight = 2
	gsCloseDeadline = 2 * time.Second // after the boundary: then the feed close is used
	// gsCaptureStart: when the boundary capture starts asking (GreekSoft
	// never had the candle earlier than ~+0.25 s).
	gsCaptureStart = 150 * time.Millisecond
)

// gsArrived wakes the LUT loop as soon as a close is in (instead of its
// next 100 ms tick).
var gsArrived = make(chan struct{}, 1)

type gsCloseKey struct {
	token int64
	b     int64
}

var gsc struct {
	mu       sync.Mutex
	got      map[gsCloseKey]float64
	inflight map[gsCloseKey]chan struct{}
	day      string
	warmOnce sync.Once
}

// gsWarmLoop pre-loads GreekSoft candles so the boundary request is fast
// (the first request for a token after a pause took ~0.9 s; 11:43
// 2026-10-09 right after a restart timed out). Once a minute (:50, ~10 s
// before the boundary; the boundary fetch itself keeps the ATM / held legs
// hot) and once at start it requests:
//   - CE + PE of the ATM +/- 1 strike of the nearest expiry (everything at
//     the minute end is derived from the ATM; +/-1 covers an ATM shift),
//   - every leg the open trades hold (their SL / TP use those closes).
//
// Nothing is warmed for display only: execution comes first.
const gsWarmStrikes = 1

func (s *Service) gsWarmLoop() {
	go s.gsCaptureLoop()
	s.gsWarmNow()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for now := range t.C {
		if now.Second() == 50 {
			s.gsWarmNow()
		}
	}
}

// gsCaptureLoop is the minute-end snapshot capture, run on its own and
// ahead of everything else: at every boundary + gsCaptureStart it starts
// fetching the closed candle of the ATM CE / PE (plus the neighbouring
// strike when the synthetic is near the middle) and every held leg. The
// LUT record and each trade's hedge / SL / TP check then only pick the
// results up (same singleflight in gsCloseAt) instead of starting the
// requests themselves ~0.1-0.2 s later, one after another.
func (s *Service) gsCaptureLoop() {
	for {
		b := time.Now().Truncate(time.Minute).Add(time.Minute)
		time.Sleep(time.Until(b.Add(gsCaptureStart)))
		if s.gsCloseProvider() == nil || !sbLiveWindow(b.Add(-time.Minute).In(lutIST())) {
			continue
		}
		for tok := range s.gsTokens(0) {
			go s.gsCloseAt(tok, b)
		}
	}
}

// gsBoundary: within the first seconds after a minute boundary, while the
// minute-end capture and checks run. Display-only / housekeeping work
// waits it out (gsAfterBoundary) so it never competes with them.
func gsBoundary(now time.Time) bool {
	return now.Sub(now.Truncate(time.Minute)) < 2500*time.Millisecond
}

// gsAfterBoundary sleeps until the minute-end window is over.
func gsAfterBoundary() {
	now := time.Now()
	if gsBoundary(now) {
		time.Sleep(time.Until(now.Truncate(time.Minute).Add(2500 * time.Millisecond)))
	}
}

// gsTokens: CE + PE within band strikes of the ATM (band 0: the ATM, plus
// the neighbouring strike when the synthetic is within 15 pts of the middle
// between two strikes) and every leg the open positions hold.
func (s *Service) gsTokens(band int) map[int64]bool {
	toks := map[int64]bool{}
	if s.Snapshot != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if c, err := s.Snapshot.GetOptionChain(ctx, lutSymbol, ""); err == nil && c != nil {
			S := lutUnderlying(c)
			atm := c.ATM
			if S > 0 {
				atm = math.Round(S/lutStrikeStep) * lutStrikeStep
			}
			for _, r := range c.Chain {
				d := math.Abs(r.Strike - atm)
				near := d <= float64(band)*lutStrikeStep+1e-6
				if band == 0 && S > 0 && d > 0 && d <= lutStrikeStep+1e-6 && math.Abs(r.Strike-S) <= lutStrikeStep/2+15 {
					near = true // the ATM may flip to this strike
				}
				if near {
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
	return toks
}

func (s *Service) gsWarmNow() {
	p := s.gsCloseProvider()
	if p == nil || !sbLiveWindow(time.Now().In(lutIST())) {
		return
	}
	toks := s.gsTokens(gsWarmStrikes)
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
	return gsFetch(p, k, day, b.Add(gsCloseDeadline))
}

// gsFetch asks p for candle k until it is in or the deadline passes: a new
// request every gsClosePoll, at most gsCloseInflight at a time.
func gsFetch(p MinuteCloseProvider, k gsCloseKey, day string, deadline time.Time) (float64, bool) {
	token, b := k.token, time.Unix(k.b, 0)
	type result struct {
		v   float64
		ok  bool
		err error
	}
	res := make(chan result, 64) // buffered: late answers never block
	inflight := 0
	launch := func() {
		inflight++
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
			bars, err := p.MinuteCloses(ctx, token, day)
			cancel()
			if err != nil {
				res <- result{err: err}
				return
			}
			gsc.mu.Lock()
			for ts, c := range bars { // keep every completed candle seen (stamped hh:mm:00)
				if ts%60 == 0 {
					gsc.got[gsCloseKey{token, ts}] = c
				}
			}
			v, ok := gsc.got[k]
			gsc.mu.Unlock()
			res <- result{v: v, ok: ok}
		}()
	}
	tick := time.NewTicker(gsClosePoll)
	defer tick.Stop()
	var lastErr error
	launch()
	for {
		select {
		case r := <-res:
			inflight--
			if r.ok {
				select {
				case gsArrived <- struct{}{}:
				default:
				}
				return r.v, true
			}
			if r.err != nil {
				lastErr = r.err
			}
		case <-tick.C:
			if inflight < gsCloseInflight && time.Now().Before(deadline) {
				launch()
			}
		}
		if !time.Now().Before(deadline) && (inflight == 0 || time.Now().After(deadline.Add(300*time.Millisecond))) {
			if lastErr != nil {
				log.Printf("[GS-CLOSE] token %d %s: %v -- feed close used", token, b.Format("15:04"), lastErr)
			}
			return 0, false
		}
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
