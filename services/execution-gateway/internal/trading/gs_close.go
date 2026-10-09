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
	// While a candle is missing the next request goes out at most
	// gsClosePoll after the previous answer, ONE request per token at a time:
	// GreekSoft's REST slows down under load at the boundary (2 overlapping
	// per token over 6-8 tokens took 350-750 ms per answer vs 50-100 ms one
	// at a time, 13:16-13:17 2026-10-09), so fewer, back-to-back requests
	// get the close sooner.
	gsClosePoll     = 40 * time.Millisecond
	gsCloseInflight = 1
	gsCloseDeadline = 2 * time.Second // after the boundary: then the feed close is used
	// gsCaptureStart: when the boundary capture starts asking (GreekSoft
	// never had the candle earlier than ~+0.25-0.3 s; earlier asks only add
	// load).
	gsCaptureStart = 250 * time.Millisecond
	// gsGateMaxWait: legs waiting for the lead's "candle is out" give up
	// waiting at this point after the boundary and ask themselves.
	gsGateMaxWait = 1300 * time.Millisecond
)

// gsGate: GreekSoft answers its REST requests about two at a time (6 sent
// together finished at 100/170/245/245/380 ms on a quiet second, ~3x slower
// at the boundary, 14:17 2026-10-09), so every extra request -- above all
// the "candle not out yet" misses of 6-8 legs polling together -- delays
// the real answers. So per boundary ONE leg, the lead (the ATM CE), asks
// until the candle is out; every other leg waits for that and then asks
// once, the ATM PE first, the held legs right behind it.
var gsGate struct {
	mu   sync.Mutex
	b    int64
	lead int64
	prio map[int64]bool
	open chan struct{}
}

// gsGateFor (caller holds gsGate.mu) resets the gate for boundary b.
func gsGateFor(b int64) {
	if gsGate.b != b {
		gsGate.b, gsGate.lead, gsGate.prio, gsGate.open = b, 0, nil, make(chan struct{})
	}
}

// gsGateSet names the lead and the priority legs of boundary b.
func gsGateSet(b, lead int64, prio map[int64]bool) {
	gsGate.mu.Lock()
	gsGateFor(b)
	if gsGate.lead == 0 {
		gsGate.lead = lead
	}
	gsGate.prio = prio
	gsGate.mu.Unlock()
}

// gsGateOpen: the candle of boundary b is out.
func gsGateOpen(b int64) {
	gsGate.mu.Lock()
	gsGateFor(b)
	select {
	case <-gsGate.open:
	default:
		close(gsGate.open)
	}
	gsGate.mu.Unlock()
}

// gsGateWait returns when token may ask: it is the lead (the first leg to
// ask becomes the lead when the capture named none), the candle is out
// (non-priority legs a moment after the ATM PE), or gsGateMaxWait passed.
func gsGateWait(token int64, b time.Time) {
	gsGate.mu.Lock()
	gsGateFor(b.Unix())
	if gsGate.lead == 0 {
		gsGate.lead = token
	}
	lead, prio, open := gsGate.lead == token, gsGate.prio[token], gsGate.open
	gsGate.mu.Unlock()
	if lead {
		return
	}
	select {
	case <-open:
		if !prio {
			time.Sleep(10 * time.Millisecond) // the ATM pair goes out first
		}
	case <-time.After(time.Until(b.Add(gsGateMaxWait))):
	}
}

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
		if now.Second() == 30 { // missing minutes (restart gap) from GreekSoft candles
			go s.gsBackfillNow()
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
		toks, atmCE, atmPE := s.gsTokens(0)
		gsGateSet(b.Unix(), atmCE, map[int64]bool{atmCE: true, atmPE: true})
		if atmCE > 0 {
			go s.gsCloseAt(atmCE, b) // the lead first
		}
		for tok, ok := range toks {
			if ok && tok != atmCE {
				go s.gsCloseAt(tok, b)
			}
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
// the neighbouring strike when the synthetic is within 10 pts of the middle
// between two strikes) and every leg the open positions hold.
func (s *Service) gsTokens(band int) (toks map[int64]bool, atmCE, atmPE int64) {
	toks = map[int64]bool{}
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
				if band == 0 && S > 0 && d > 0 && d <= lutStrikeStep+1e-6 && math.Abs(r.Strike-S) <= lutStrikeStep/2+10 {
					near = true // the ATM may flip to this strike
				}
				if near {
					toks[r.CEToken], toks[r.PEToken] = r.CEToken > 0, r.PEToken > 0
				}
				if d < 1e-6 {
					atmCE, atmPE = r.CEToken, r.PEToken
				}
			}
		}
		cancel()
	}
	pm.mu.Lock()
	for _, pos := range pm.view.Positions {
		// Wings are margin-only (outside PnL / delta): no candle needed.
		if pos.NetQty != 0 && pos.Token > 0 && !pos.Wing {
			toks[pos.Token] = true
		}
	}
	pm.mu.Unlock()
	return toks, atmCE, atmPE
}

func (s *Service) gsWarmNow() {
	p := s.gsCloseProvider()
	if p == nil || !sbLiveWindow(time.Now().In(lutIST())) {
		return
	}
	toks, _, _ := s.gsTokens(gsWarmStrikes)
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
	inflight, asks := 0, 0
	launch := func() {
		inflight++
		asks++
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
			if ok {
				gsGateOpen(k.b) // the candle is out: the other legs ask now
			}
			res <- result{v: v, ok: ok}
		}()
	}
	tick := time.NewTicker(gsClosePoll)
	defer tick.Stop()
	var lastErr error
	gsGateWait(token, b)
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
				gsGate.mu.Lock()
				lead := gsGate.b == k.b && gsGate.lead == token
				gsGate.mu.Unlock()
				if lead { // one line a minute: when GreekSoft had the candle
					log.Printf("[GS-CLOSE] %s candle out: lead token %d answered at +%dms after %d ask(s)",
						b.In(lutIST()).Format("15:04"), token, time.Since(b).Milliseconds(), asks)
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
// fetched in parallel, then re-derives the synthetic future / ATM from the
// ATM pair. When that moves the ATM to another strike (synthetic near the
// middle between two strikes), the new ATM pair is priced at GreekSoft too
// and the synthetic is taken from IT -- so the ATM, its closes and the
// synthetic always belong together (13:17 2026-10-09: the ATM flipped
// 22550 -> 22500 and the LUT kept feed closes for 22500 while the
// synthetic came from the 22550 pair). Returns how many legs came from
// GreekSoft and the feed closes they replaced (token -> feed price).
func (s *Service) gsApplyCloses(cc *OptionChainSnapshot, b time.Time, tokens map[int64]bool) (int, map[int64]float64) {
	feed := map[int64]float64{}
	if cc == nil || len(tokens) == 0 {
		return 0, feed
	}
	done := map[int64]bool{}
	n := s.gsReplace(cc, b, tokens, done, feed)
	if n == 0 {
		return 0, feed
	}
	for i := 0; i < 3; i++ {
		row := gsRowAt(cc, cc.ATM)
		if row == nil || row.CELtp <= 0 || row.PELtp <= 0 {
			break
		}
		syn := row.Strike + row.CELtp - row.PELtp
		cc.SyntheticFuture, cc.SyntheticSpot = syn, syn
		atm := math.Round(syn/lutStrikeStep) * lutStrikeStep
		next := gsRowAt(cc, atm)
		if atm == cc.ATM || next == nil {
			break
		}
		cc.ATM = atm // the ATM moved: price the new pair at GreekSoft, then re-derive from it
		extra := map[int64]bool{}
		for _, t := range []int64{next.CEToken, next.PEToken} {
			if t > 0 && !done[t] {
				extra[t] = true
			}
		}
		n += s.gsReplace(cc, b, extra, done, feed)
	}
	return n, feed
}

// gsReplace fetches the GreekSoft closes of tokens (in parallel) and writes
// them into cc; done collects every token attempted, feed the replaced feed
// prices. Returns the number of legs replaced.
func (s *Service) gsReplace(cc *OptionChainSnapshot, b time.Time, tokens map[int64]bool, done map[int64]bool, feed map[int64]float64) int {
	if len(tokens) == 0 {
		return 0
	}
	type res struct {
		tok int64
		px  float64
		ok  bool
	}
	ch := make(chan res, len(tokens))
	for t := range tokens {
		done[t] = true
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
	return n
}

func gsRowAt(cc *OptionChainSnapshot, strike float64) *OptionChainRow {
	for i := range cc.Chain {
		if math.Abs(cc.Chain[i].Strike-strike) < 1e-6 {
			return &cc.Chain[i]
		}
	}
	return nil
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
