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
	"net/http"
	"os"
	"sort"
	"strconv"
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
// once, in rank order (gsRankStep apart): the ATM pair, each trade's own
// straddle strike, the hedge legs largest first, the rest of the band.
var gsGate struct {
	mu   sync.Mutex
	b    int64
	lead int64
	rank map[int64]int
	open chan struct{}
}

// gsRankStep: spacing between ranks once the candle is out, so GreekSoft
// (~2 answers at a time) serves the legs that matter most first.
const gsRankStep = 6 * time.Millisecond

// gsGateFor (caller holds gsGate.mu) resets the gate for boundary b.
func gsGateFor(b int64) {
	if gsGate.b != b {
		gsGate.b, gsGate.lead, gsGate.rank, gsGate.open = b, 0, nil, make(chan struct{})
	}
}

// gsGateSet names the lead and the ask order (rank, 0 first) of boundary b.
// The capture's lead (the front ATM CE) replaces a leg that asked first
// and named itself (19 Oct 14:39: a next-week trade's leg at +0 s held the
// lead, the ATM CE waited for its candle and fell back to the feed).
func gsGateSet(b, lead int64, rank map[int64]int) {
	gsGate.mu.Lock()
	gsGateFor(b)
	if lead > 0 {
		gsGate.lead = lead
	}
	gsGate.rank = rank
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
	// A leg asking before the capture has named the lead (a trade's check at
	// +0 s) waits for it instead of becoming a second poller.
	if gsGate.lead == 0 && time.Since(b) >= gsCaptureStart+gsLeadGrace {
		gsGate.lead = token
	}
	lead, open := gsGate.lead == token, gsGate.open
	gsGate.mu.Unlock()
	if lead {
		return
	}
	maxWait := time.After(time.Until(b.Add(gsGateMaxWait)))
	select {
	case <-open:
	case <-maxWait:
		return
	}
	gsGate.mu.Lock() // the rank is set by now (the capture names it before asking)
	r, ranked := gsGate.rank[token]
	if !ranked {
		r = len(gsGate.rank) + 1 // unranked: after every ranked leg
	}
	pair := gsGatePair()
	gsGate.mu.Unlock()
	if r == 0 {
		return // the ATM pair asks as soon as the candle is out
	}
	// Everything else only once the ATM pair is in (19 Oct 12:30-14:50:
	// released together, ~15 next-week legs pushed the ATM PE past the
	// deadline in 8-15 of every 30 minutes), then in rank order.
	for len(pair) > 0 && !gsHaveAll(pair, b) && time.Now().Before(b.Add(gsGateMaxWait)) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(time.Duration(r) * gsRankStep)
}

// gsLeadGrace: how long after gsCaptureStart a leg still waits for the
// capture to name the lead before naming itself (no capture running).
const gsLeadGrace = 300 * time.Millisecond

// gsGatePair (caller holds gsGate.mu): the rank-0 tokens -- the front ATM pair.
func gsGatePair() map[int64]bool {
	pair := map[int64]bool{}
	for t, r := range gsGate.rank {
		if r == 0 && t > 0 {
			pair[t] = true
		}
	}
	return pair
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
	s.gsPlanNow()
	s.gsWarmNow()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for now := range t.C {
		if now.Second() == 50 {
			s.gsPlanNow()
			s.gsWarmNow()
		}
		if now.Second() == 3 {
			// Right after the minute-end work (hedge / SL / LUT record): the
			// position strikes' GreekSoft closes, so the minute is complete
			// before any screen shows it.
			go s.gsOverlayNow()
		}
		if now.Second() == 30 { // missing minutes (restart gap) from GreekSoft candles
			go func() {
				s.gsBackfillNow()
				if gsOverlayRetry.Load() { // only when the :03 run missed something
					s.gsOverlayNow()
				}
			}()
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
		toks, atmCE, atmPE := s.gsTokens(gsCaptureBand())
		lead, first := gsFirstGet()
		if lead == 0 { // no trade open: the front ATM pair (the LUT record) first
			lead, first = atmCE, map[int64]bool{atmCE: atmCE > 0, atmPE: atmPE > 0}
		}
		for t, ok := range first {
			toks[t] = toks[t] || ok
		}
		gsGateSet(b.Unix(), lead, gsRankFirst(first, atmCE, atmPE, toks))
		if lead > 0 {
			go s.gsCloseAt(lead, b) // the lead first
		}
		for tok, ok := range toks {
			if ok && tok != lead {
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
// gsDist: each front-expiry token's distance from the ATM in strikes, from
// the last gsTokens call (the ask order of the ATM band: nearest first).
var gsDist struct {
	sync.Mutex
	m map[int64]int
}

// gsFirst: what the live trades need at the minute end, asked before
// anything else -- each open trade's own CE / PE and its expiry's ATM pair;
// lead: the first trade expiry's ATM CE. Empty with no trade open. From the
// last gsTokens call.
var gsFirst struct {
	sync.Mutex
	lead int64
	m    map[int64]bool
}

func gsFirstGet() (int64, map[int64]bool) {
	gsFirst.Lock()
	defer gsFirst.Unlock()
	out := make(map[int64]bool, len(gsFirst.m))
	for t, ok := range gsFirst.m {
		if ok && t > 0 {
			out[t] = true
		}
	}
	return gsFirst.lead, out
}

// chainATMPair: the ATM CE / PE tokens of c (ATM from its synthetic).
func chainATMPair(c *OptionChainSnapshot) (ce, pe int64) {
	if c == nil {
		return 0, 0
	}
	atm := c.ATM
	if S := lutUnderlying(c); S > 0 {
		atm = math.Round(S/lutStrikeStep) * lutStrikeStep
	}
	for _, r := range c.Chain {
		if math.Abs(r.Strike-atm) < 1e-6 {
			return r.CEToken, r.PEToken
		}
	}
	return 0, 0
}

func (s *Service) gsTokens(band int) (toks map[int64]bool, atmCE, atmPE int64) {
	toks = map[int64]bool{}
	dist := map[int64]int{}
	frontExpiry := ""
	if s.Snapshot != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if c, err := s.Snapshot.GetOptionChain(ctx, lutSymbol, ""); err == nil && c != nil {
			frontExpiry = c.Expiry
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
					n := int(math.Round(d / lutStrikeStep))
					dist[r.CEToken], dist[r.PEToken] = n, n
				}
				if d < 1e-6 {
					atmCE, atmPE = r.CEToken, r.PEToken
				}
			}
		}
		cancel()
	}
	// Every trade's own strike. Held hedge legs are NOT fetched: the decision
	// rests on the ATM pair (synthetic future) and the own strike; a leg's
	// feed minute close moves its delta by thousandths (user, 19 Oct).
	first := map[int64]bool{}
	var lead int64
	own := gsPlanOwn()
	for t := range own {
		toks[t] = true
		first[t] = true
	}
	// Open trades, per the minute-end plan (gsPlanNow, refreshed at :50):
	// a sim-driven LUT trade needs only its expiry's ATM pair (the sim
	// decides its hedges / exits); a normal trade its expiry's ATM +/- 2
	// strikes, CE + PE (its hedges open at the ATM). Fewer tokens = the
	// candles in sooner: GreekSoft answers ~2 requests at a time.
	if s.Snapshot != nil {
		plan := gsPlanGet()
		exps := make([]gsSymExp, 0, len(plan))
		for se := range plan {
			exps = append(exps, se)
		}
		sort.Slice(exps, func(i, j int) bool { return exps[i].sym+exps[i].exp < exps[j].sym+exps[j].exp })
		for _, se := range exps {
			b := plan[se]
			if se.exp == "" {
				continue
			}
			if strings.EqualFold(se.exp, frontExpiry) && strings.EqualFold(se.sym, lutSymbol) {
				first[atmCE], first[atmPE] = atmCE > 0, atmPE > 0 // a trade on the front expiry
				if lead == 0 {
					lead = atmCE
				}
				if b <= band {
					continue // the front expiry's band is in already
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if c, err := s.Snapshot.GetOptionChain(ctx, se.sym, se.exp); err == nil && c != nil {
				for t, ok := range atmBandTokens(c, max(b, band)) {
					toks[t] = toks[t] || ok
				}
				if ce, pe := chainATMPair(c); ce > 0 && pe > 0 {
					first[ce], first[pe] = true, true
					if lead == 0 {
						lead = ce
					}
				}
			}
			cancel()
		}
	}
	if lead == 0 { // a trade whose chain did not answer: its own CE leads
		for t := range own {
			if lead == 0 || t < lead {
				lead = t
			}
		}
	}
	if len(own) == 0 {
		lead, first = 0, nil
	}
	gsFirst.Lock()
	gsFirst.lead, gsFirst.m = lead, first
	gsFirst.Unlock()
	gsDist.Lock()
	gsDist.m = dist
	gsDist.Unlock()
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
					for ts, c := range gsAligned(bars, time.Now(), tok) {
						gsc.got[gsCloseKey{tok, ts}] = c
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
			for ts, c := range gsAligned(bars, time.Now(), token) { // every completed candle seen
				gsc.got[gsCloseKey{token, ts}] = c
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
	return s.gsApplyClosesCore(cc, b, tokens, nil)
}

// gsApplyClosesCore is gsApplyCloses waiting fully only for core (the ATM
// pair and the trade's own strike: what the decision rests on); the other
// tokens get gsLegGrace after core is in, then their feed close stands. A
// held hedge strike that did not trade in the minute has no candle to wait
// for -- its last trade is its close (19 Oct: the monitor waited the full 2 s
// every minute for such legs). core nil: wait for every token.
func (s *Service) gsApplyClosesCore(cc *OptionChainSnapshot, b time.Time, tokens, core map[int64]bool) (int, map[int64]float64) {
	feed := map[int64]float64{}
	if cc == nil || len(tokens) == 0 {
		return 0, feed
	}
	done := map[int64]bool{}
	n := s.gsReplaceCore(cc, b, tokens, core, done, feed)
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
	gsRegreek(cc, b, feed)
	return n, feed
}

// gsRegreek recomputes delta / gamma / IV at every strike whose price the
// GreekSoft closes replaced (feed holds the replaced tokens): Black-76 at the
// GreekSoft synthetic future, one IV per strike from its OTM leg's close,
// both legs sharing it -- the backtest's method. The snapshot's greeks were
// taken at the feed's price at another moment (16 Oct 10:19).
func gsRegreek(cc *OptionChainSnapshot, b time.Time, feed map[int64]float64) {
	if cc == nil || len(feed) == 0 || cc.SyntheticFuture <= 0 {
		return
	}
	exp, err := lutParseExpiry(cc.Expiry)
	if err != nil {
		return
	}
	F := cc.SyntheticFuture
	T := math.Max(lutRawDTE(b, exp)/365.0, 1e-5)
	for i := range cc.Chain {
		r := &cc.Chain[i]
		_, ce := feed[r.CEToken]
		_, pe := feed[r.PEToken]
		if !(ce && r.CEToken > 0) && !(pe && r.PEToken > 0) {
			continue
		}
		iv := simStrikeIV(F, r.Strike, T, r.CELtp, r.PELtp)
		if iv <= 0 {
			continue
		}
		_, r.CEDelta, r.CEGamma = simBlack76(true, F, r.Strike, T, iv)
		_, r.PEDelta, r.PEGamma = simBlack76(false, F, r.Strike, T, iv)
		r.CEIV, r.PEIV = iv*100, iv*100
	}
}

// gsReplace fetches the GreekSoft closes of tokens (in parallel) and writes
// them into cc; done collects every token attempted, feed the replaced feed
// prices. Returns the number of legs replaced.
func (s *Service) gsReplace(cc *OptionChainSnapshot, b time.Time, tokens map[int64]bool, done map[int64]bool, feed map[int64]float64) int {
	return s.gsReplaceCore(cc, b, tokens, nil, done, feed)
}

// gsLegGrace: how long the non-core legs may still come in after the core
// tokens are in (gsApplyClosesCore).
const gsLegGrace = 200 * time.Millisecond

func (s *Service) gsReplaceCore(cc *OptionChainSnapshot, b time.Time, tokens, core map[int64]bool, done map[int64]bool, feed map[int64]float64) int {
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
	coreLeft := -1 // wait for every token
	if core != nil {
		coreLeft = 0
		for t := range tokens {
			if core[t] {
				coreLeft++
			}
		}
	}
	var grace <-chan time.Time
	if coreLeft == 0 {
		grace = time.After(gsLegGrace)
	}
collect:
	for range tokens {
		select {
		case r := <-ch:
			if r.ok {
				got[r.tok] = r.px
			}
			if coreLeft > 0 && core[r.tok] {
				if coreLeft--; coreLeft == 0 {
					grace = time.After(gsLegGrace)
				}
			}
		case <-grace:
			break collect // the rest: feed closes this minute
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

// gsSimBand: strikes either side of the front-expiry ATM fetched at every
// boundary -- what the LUT simulation needs besides its build strike: the
// ATM pair (synthetic future, allowed) and its neighbours, so an ATM that
// moved within the minute is already in. Asked nearest first (gsRank).
const gsSimBand = 2

// gsAligned: a token's candles of now's day, keyed by the minute they close
// (hh:mm:00). GreekSoft's answer for a day also carries the PREVIOUS trading
// day's candles (19 Oct token 51346: 324 of 610 were 16 Oct's) -- dropped.
// The day's candles are stamped at the minute end, or a few seconds after it
// (the time of the last trade it took in): 10:04:01 is the 10:04 close
// (19 Oct NSE ticks: = the LTP at the stamp; 6 of 62 match 10:05). Such a
// late stamp is taken once its minute is over (final); before that, and
// whenever an hh:mm:00 candle exists, the hh:mm:00 one is the close.
func gsAligned(bars map[int64]float64, now time.Time, tok int64) map[int64]float64 {
	y, m, d := now.In(lutIST()).Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, lutIST()).Unix()
	to := from + 86400
	out := make(map[int64]float64, len(bars))
	late := map[int64]int64{} // minute -> stamp used
	for ts, c := range bars {
		if ts < from || ts >= to || c <= 0 {
			continue
		}
		b := ts - ts%60
		if ts == b {
			out[b] = c
			delete(late, b)
			continue
		}
		if b+60 > now.Unix() { // still inside the minute it was stamped in
			continue
		}
		if _, end := bars[b]; end {
			continue
		}
		if prev, ok := late[b]; !ok || ts > prev {
			late[b] = ts
			out[b] = c
		}
	}
	if len(late) > 0 {
		if _, seen := gsLateLogged.LoadOrStore(tok, true); !seen {
			log.Printf("[GS-CLOSE] token %d: %d minute close(s) stamped a few seconds after the minute end -- used for that minute", tok, len(late))
		}
	}
	return out
}

var gsLateLogged sync.Map // token -> logged once

// gsOwnGrace: how long the minute record waits, after the ATM pair is in,
// for the open trades' position-strike closes (prefetched at rank 1, so
// normally ~0.1-0.3 s behind the ATM pair); after it, what is in is used.
const gsOwnGrace = 1500 * time.Millisecond

// gsRowFor: the chain has a row carrying this token.
func gsRowFor(cc *OptionChainSnapshot, tok int64) bool {
	if cc == nil || tok <= 0 {
		return false
	}
	for _, r := range cc.Chain {
		if r.CEToken == tok || r.PEToken == tok {
			return true
		}
	}
	return false
}

// atmTokens: the ATM CE / PE tokens of a chain.
// atmBandTokens: CE + PE tokens of the strikes within band steps of the ATM
// (from the synthetic future), plus the strike the ATM may flip to.
func atmBandTokens(c *OptionChainSnapshot, band int) map[int64]bool {
	out := map[int64]bool{}
	if c == nil {
		return out
	}
	S := lutUnderlying(c)
	atm := c.ATM
	if S > 0 {
		atm = math.Round(S/lutStrikeStep) * lutStrikeStep
	}
	for _, r := range c.Chain {
		d := math.Abs(r.Strike - atm)
		near := d <= float64(band)*lutStrikeStep+1e-6
		if !near && band == 0 && S > 0 && d <= lutStrikeStep+1e-6 && math.Abs(r.Strike-S) <= lutStrikeStep/2+10 {
			near = true // the ATM may flip to this strike
		}
		if near {
			if r.CEToken > 0 {
				out[r.CEToken] = true
			}
			if r.PEToken > 0 {
				out[r.PEToken] = true
			}
		}
	}
	return out
}

// gsMinuteBand: strikes around the ATM a trade's minute-end GreekSoft closes
// cover -- 0 (the ATM pair) for a sim-driven LUT trade, 2 for a normal one.
func gsMinuteBand(tradeUID string) int {
	if owned, _ := simDriveOwns(tradeUID); owned {
		return 0
	}
	return 2
}

type gsSymExp struct{ sym, exp string }

var gsPlan struct {
	sync.Mutex
	m    map[gsSymExp]int // expiry -> widest band an open trade there needs
	own  map[int64]bool   // each open trade's own straddle CE / PE tokens
	legs map[int64]int64  // normal trades' held non-wing legs -> |net qty|
	sim  bool             // a sim-driven (LUT) trade is open
}

// gsCaptureBand: the front ATM band fetched at every boundary. +/- gsSimBand
// only while a sim-driven LUT trade is open (its sim needs the neighbours);
// otherwise the ATM pair alone (plus the strike it may flip to) -- fewer
// requests, so the ATM candle is in sooner. Normal trades add their own
// expiry's ATM +/- 2 through gsPlan either way.
func gsCaptureBand() int {
	gsPlan.Lock()
	defer gsPlan.Unlock()
	if gsPlan.sim {
		return gsSimBand
	}
	return 0
}

// gsPlanNow refreshes which expiries need which band (open trades), off the
// boundary (warm loop :50) so the minute-end capture only reads it.
func (s *Service) gsPlanNow() {
	m := map[gsSymExp]int{}
	own := map[int64]bool{}
	legs := map[int64]int64{} // held hedge legs are not fetched (feed closes)
	sim := false
	for _, t := range s.Store.AllTrades() {
		if isTerminalTradeStatus(t.Status) {
			continue
		}
		own[t.CEToken], own[t.PEToken] = t.CEToken > 0, t.PEToken > 0
		band := gsMinuteBand(t.TradeUID)
		sim = sim || band == 0
		if strings.TrimSpace(t.Expiry) == "" {
			continue
		}
		k := gsSymExp{NormalizeSymbol(t.Symbol), strings.TrimSpace(t.Expiry)}
		if b, ok := m[k]; !ok || band > b {
			m[k] = band
		}
	}
	gsPlan.Lock()
	gsPlan.m, gsPlan.own, gsPlan.legs, gsPlan.sim = m, own, legs, sim
	gsPlan.Unlock()
}

// gsPlanOwn: every open trade's own straddle CE / PE tokens.
func gsPlanOwn() map[int64]bool {
	gsPlan.Lock()
	defer gsPlan.Unlock()
	out := make(map[int64]bool, len(gsPlan.own))
	for k, v := range gsPlan.own {
		if v {
			out[k] = true
		}
	}
	return out
}

// gsPlanLegs: the normal trades' held non-wing legs (|net qty|).
func gsPlanLegs() map[int64]int64 {
	gsPlan.Lock()
	defer gsPlan.Unlock()
	out := make(map[int64]int64, len(gsPlan.legs))
	for k, v := range gsPlan.legs {
		out[k] = v
	}
	return out
}

// gsRankTokens orders a boundary's asks: 0 the ATM pair, 1 each trade's own
// straddle strike, then the held hedge legs largest first, then the rest.
func gsRankTokens(atmCE, atmPE int64, toks map[int64]bool) map[int64]int {
	gsPlan.Lock()
	own := gsPlan.own
	legs := gsPlan.legs
	gsPlan.Unlock()
	return gsRank(atmCE, atmPE, toks, own, legs)
}

func gsRank(atmCE, atmPE int64, toks, own map[int64]bool, legs map[int64]int64) map[int64]int {
	gsDist.Lock()
	dist := gsDist.m
	gsDist.Unlock()
	return gsRankWith(atmCE, atmPE, toks, own, legs, dist)
}

// gsRankWith orders a boundary's asks: 0 the ATM pair, 1 the build (own)
// strikes, then the front ATM band nearest first (+/-1, +/-2: an ATM that
// moved within the minute is in right behind), then the normal trades' held
// legs largest first, then anything else.
func gsRankWith(atmCE, atmPE int64, toks, own map[int64]bool, legs map[int64]int64, dist map[int64]int) map[int64]int {
	return gsRankFirstWith(map[int64]bool{atmCE: atmCE > 0, atmPE: atmPE > 0}, atmCE, atmPE, toks, own, legs, dist)
}

// gsRankFirst: the boundary's ask order with first (the live trades' own
// strikes and their expiry's ATM pair) at rank 0, the front ATM pair next.
func gsRankFirst(first map[int64]bool, atmCE, atmPE int64, toks map[int64]bool) map[int64]int {
	gsPlan.Lock()
	own, legs := gsPlan.own, gsPlan.legs
	gsPlan.Unlock()
	gsDist.Lock()
	dist := gsDist.m
	gsDist.Unlock()
	return gsRankFirstWith(first, atmCE, atmPE, toks, own, legs, dist)
}

func gsRankFirstWith(first map[int64]bool, atmCE, atmPE int64, toks, own map[int64]bool, legs map[int64]int64, dist map[int64]int) map[int64]int {
	rank := map[int64]int{}
	for t, ok := range first {
		if ok && t > 0 {
			rank[t] = 0
		}
	}
	next := 1
	pairNext := false
	for _, t := range []int64{atmCE, atmPE} {
		if _, done := rank[t]; t > 0 && !done {
			rank[t], pairNext = next, true
		}
	}
	if pairNext {
		next++
	}
	ownTok := []int64{}
	for t, ok := range own {
		if _, done := rank[t]; ok && toks[t] && !done {
			ownTok = append(ownTok, t)
		}
	}
	if len(ownTok) > 0 {
		for _, t := range ownTok {
			rank[t] = next
		}
		next++
	}
	for d := 1; d <= gsSimBand; d++ {
		any := false
		for t, ok := range toks {
			if _, done := rank[t]; ok && !done && dist[t] == d {
				rank[t], any = next, true
			}
		}
		if any {
			next++
		}
	}
	held := []int64{}
	for t := range legs {
		if _, done := rank[t]; !done && toks[t] {
			held = append(held, t)
		}
	}
	sort.Slice(held, func(i, j int) bool {
		if legs[held[i]] != legs[held[j]] {
			return legs[held[i]] > legs[held[j]]
		}
		return held[i] < held[j]
	})
	for _, t := range held {
		rank[t] = next
		next++
	}
	for t, ok := range toks {
		if _, done := rank[t]; ok && !done {
			rank[t] = next
		}
	}
	return rank
}

func gsPlanGet() map[gsSymExp]int {
	gsPlan.Lock()
	defer gsPlan.Unlock()
	out := make(map[gsSymExp]int, len(gsPlan.m))
	for k, v := range gsPlan.m {
		out[k] = v
	}
	return out
}

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

// GSCandlesHandler: GET /api/gs/candles?token=47159[&date=YYYYMMDD] -- a
// token's raw GreekSoft 1-minute candles (timestamp, close, whether stamped at
// a minute end) through the gateway's existing session. Read-only diagnostic.
func (h *Handlers) GSCandlesHandler(w http.ResponseWriter, r *http.Request) {
	tok, _ := strconv.ParseInt(r.URL.Query().Get("token"), 10, 64)
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().In(lutIST()).Format("20060102")
	}
	p := h.Service.gsCloseProvider()
	if tok <= 0 || p == nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "token required / GreekSoft closes unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	bars, err := p.MinuteCloses(ctx, tok, date)
	cancel()
	if err != nil {
		lutJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	type bar struct {
		TS       int64   `json:"ts"`
		Time     string  `json:"time"`
		Close    float64 `json:"close"`
		EndStamp bool    `json:"end_stamped"`
	}
	out := make([]bar, 0, len(bars))
	for ts, c := range bars {
		out = append(out, bar{ts, time.Unix(ts, 0).In(lutIST()).Format("15:04:05"), c, ts%60 == 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "token": tok, "date": date, "bars": out})
}
