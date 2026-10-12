package overnight

// The minute-end scenario: every number is a GreekSoft 1-minute candle
// CLOSE of the monthly contracts at that minute end.
//
//	future     close of the monthly future -- the underlying for everything
//	ATM        listed strike nearest the future
//	delta      Black-76 on the future, each strike's own implied vol
//	CE pick    strike at / above ATM whose delta is closest to +target
//	PE pick    strike at / below ATM whose delta is closest to -target
//	           (distance compared to 4 decimals; a tie takes the LOWER strike)
//
// The search starts at the previous minute's pick and walks strike by
// strike, so a normal minute costs about ten candle requests in all.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Pick struct {
	Strike float64 `json:"strike"`
	Token  int64   `json:"token"`
	GToken int64   `json:"gtoken"`
	Price  float64 `json:"price"`
	IV     float64 `json:"iv"`
	Delta  float64 `json:"delta"`
	Src    string  `json:"src,omitempty"` // gs = candle close, ws = websocket tick
}

type Picks struct {
	Delta float64 `json:"delta"`
	CE    *Pick   `json:"ce,omitempty"`
	PE    *Pick   `json:"pe,omitempty"`
}

type HeldMark struct {
	Token  int64   `json:"token"`
	Strike float64 `json:"strike"`
	Opt    string  `json:"opt"`
	Qty    int64   `json:"qty"`
	Price  float64 `json:"price"`
	Delta  float64 `json:"delta"`
	PnL    float64 `json:"pnl"`
}

type Scenario struct {
	Day      string            `json:"day"`
	Minute   string            `json:"minute"` // the minute END, HH:MM
	Ts       int64             `json:"ts"`
	Spot     float64           `json:"spot"`
	Fut      float64           `json:"fut"`
	ATM      float64           `json:"atm"`
	ATMCE    float64           `json:"atm_ce"`
	ATMPE    float64           `json:"atm_pe"`
	Straddle float64           `json:"straddle"`
	ATMIV    float64           `json:"atm_iv"`
	Picks    map[string]*Picks `json:"picks"` // by delta, "0.2500"
	Held     []HeldMark        `json:"held,omitempty"`
	PnL      float64           `json:"pnl"`
	DayPnL   float64           `json:"day_pnl"`
	TookMs   int64             `json:"took_ms"`
	Requests int               `json:"requests"`
}

func deltaKey(d float64) string { return fmt.Sprintf("%.4f", d) }

func (sc *Scenario) picks(d float64) *Picks {
	if sc == nil {
		return nil
	}
	return sc.Picks[deltaKey(d)]
}

// closeFromBars reads "the last traded price at the minute end" out of one
// get_ohlc reply (verified live 2026-10-12 on the monthly contracts):
//
//   - a finished bar is stamped when it was closed, i.e. at the first trade
//     AFTER its minute -- 12:40:05 for the 12:39-12:40 bar of a busy strike,
//     12:41:38 for a quiet one;
//   - the newest bar is still forming, stamped with its latest trade.
//
// So the close at end is the first finished bar stamped at / after end,
// and when there is none (nothing traded since) the newest bar. fresh is
// false when the newest bar is over 3 minutes older than end: GreekSoft
// sometimes returns a cut-off list (seen: 376 bars ending 09:15 instead of
// 584), and a quiet strike looks the same -- either way the candle is not
// trusted on its own.
func closeFromBars(bars map[int64]float64, end int64) (px float64, fresh bool) {
	if len(bars) == 0 {
		return 0, false
	}
	keys := make([]int64, 0, len(bars))
	for ts, c := range bars {
		if c > 0 {
			keys = append(keys, ts)
		}
	}
	if len(keys) == 0 {
		return 0, false
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
	newest := keys[len(keys)-1]
	px = bars[newest]
	for _, ts := range keys[:len(keys)-1] {
		if ts >= end {
			px = bars[ts]
			break
		}
	}
	return px, newest >= end-180
}

// tickPrice is a live tick as a price: the middle of bid and ask when the
// book is two-sided and tight, else the last trade.
func tickPrice(q Quote) float64 {
	if q.Bid > 0 && q.Ask >= q.Bid {
		mid := (q.Bid + q.Ask) / 2
		if q.Ask-q.Bid <= math.Max(0.1*mid, 2) {
			return mid
		}
	}
	return q.LTP
}

// closeBook prices tokens at one minute end, once each, two candle
// requests at a time (what GreekSoft's REST serves in parallel). A candle
// that is not fresh is asked for once more and then replaced by the
// GreekSoft websocket tick taken at the start of the computation.
type closeBook struct {
	ctx   context.Context
	b     Broker
	end   time.Time
	sem   chan struct{}
	ticks map[int64]Quote // by exchange token
	mu    sync.Mutex
	got   map[int64]float64
	src   map[int64]string // "gs" candle | "ws" tick
	miss  map[int64]bool
	n     int
}

func newCloseBook(ctx context.Context, b Broker, end time.Time, ticks map[int64]Quote) *closeBook {
	return &closeBook{ctx: ctx, b: b, end: end, sem: make(chan struct{}, 2), ticks: ticks,
		got: map[int64]float64{}, src: map[int64]string{}, miss: map[int64]bool{}}
}

func (cb *closeBook) at(token int64) (float64, bool) {
	cb.mu.Lock()
	if v, ok := cb.got[token]; ok {
		cb.mu.Unlock()
		return v, true
	}
	if cb.miss[token] {
		cb.mu.Unlock()
		return 0, false
	}
	cb.mu.Unlock()

	var px float64
	src := ""
	for try := 0; try < 2 && src == ""; try++ {
		cb.sem <- struct{}{}
		bars, err := cb.b.MinuteCloses(cb.ctx, token, cb.end.Format("20060102"))
		<-cb.sem
		cb.mu.Lock()
		cb.n++
		cb.mu.Unlock()
		if err != nil {
			continue
		}
		v, fresh := closeFromBars(bars, cb.end.Unix())
		if v > 0 {
			px = v
		}
		if fresh && v > 0 {
			src = "gs"
		}
	}
	if src == "" {
		// Not fresh twice: only the live tick is trusted. A stale candle
		// alone is NOT used -- it put a morning price into the 13:12 ATM
		// straddle (642.90 instead of 524) on a start before ticks flowed.
		px = tickPrice(cb.ticks[token])
		src = "ws"
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if px <= 0 {
		cb.miss[token] = true
		return 0, false
	}
	cb.got[token], cb.src[token] = px, src
	return px, true
}

// pickSide finds the strike of one option type whose |delta| is closest to
// target. o counts strikes outward from ATM (0 = ATM); |delta| falls as o
// grows, so the answer is one of the two neighbours around the crossing.
func pickSide(c *Chain, cb *closeBook, f, t, target float64, call bool, atmIdx, startIdx int) *Pick {
	dir, max := 1, len(c.Strikes)-1-atmIdx
	if !call {
		dir, max = -1, atmIdx
	}
	memo := map[int]*Pick{}
	eval := func(o int) *Pick {
		if p, ok := memo[o]; ok {
			return p
		}
		k := c.Strikes[atmIdx+dir*o]
		opt := "PE"
		if call {
			opt = "CE"
		}
		in, _ := c.leg(k, opt)
		p := &Pick{Strike: k, Token: in.Token, GToken: in.GToken}
		if px, ok := cb.at(in.Token); ok {
			p.Price = px
			cb.mu.Lock()
			p.Src = cb.src[in.Token]
			cb.mu.Unlock()
			if iv, ok := impliedVol(px, f, k, t, call); ok {
				p.IV = round4(iv)
				p.Delta = round4(delta76(f, k, t, iv, call))
			}
		}
		memo[o] = p
		return p
	}
	g := func(o int) float64 { return math.Abs(eval(o).Delta) }

	o := (startIdx - atmIdx) * dir
	if o < 0 {
		o = 0
	}
	if o > max {
		o = max
	}
	steps := 0
	if g(o) > target {
		for o < max && g(o) > target && steps < 80 {
			o++
			steps++
		}
	} else {
		for o > 0 && g(o-1) <= target && steps < 80 {
			o--
			steps++
		}
	}
	// g(o) <= target (or the listing ended); the inner neighbour is above it.
	best := eval(o)
	if o > 0 {
		inner := eval(o - 1)
		di, do := round4(math.Abs(math.Abs(inner.Delta)-target)), round4(math.Abs(math.Abs(best.Delta)-target))
		switch {
		case di < do:
			best = inner
		case di == do && inner.Strike < best.Strike: // tie: the lower strike
			best = inner
		}
	}
	if best.Price <= 0 || best.Delta == 0 {
		return nil
	}
	return best
}

// compute builds the scenario of the minute ending at end.
func (s *Service) compute(ctx context.Context, end time.Time) (*Scenario, error) {
	t0 := time.Now()
	s.mu.Lock()
	c := s.chain
	deltas := map[string]float64{deltaKey(s.st.Delta): s.st.Delta}
	var held []Leg
	if p := s.st.Position; p != nil {
		deltas[deltaKey(p.Delta)] = p.Delta
		for _, l := range p.Legs {
			if l.Qty > 0 {
				held = append(held, *l)
			}
		}
	}
	for _, j := range s.st.Jobs {
		if (j.Status == "SCHEDULED" || j.Status == "RUNNING") && j.Delta > 0 {
			deltas[deltaKey(j.Delta)] = j.Delta
		}
	}
	var prev *Scenario
	if n := len(s.rows); n > 0 {
		prev = s.rows[n-1]
	}
	s.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("monthly chain not loaded")
	}

	// Websocket ticks of the band, taken now: the stand-in for any strike
	// whose candle is not fresh.
	// The monthly future's own price is the underlying for everything
	// (ATM and delta) -- no synthetic.
	ticks := map[int64]Quote{}
	cb := newCloseBook(ctx, s.live, end, ticks)
	band := s.bandTokens()
	load := func() {
		if q, _, err := s.feed.quotes(ctx, band); err == nil {
			for g, v := range q {
				ticks[g%1000000] = v // gtoken 102051352 -> exchange token 51352
			}
		}
	}
	load() // at least the future's tick, should its candle not be fresh
	fut, ok := cb.at(c.Fut.Token)
	if !ok {
		return nil, fmt.Errorf("no GreekSoft price for the %s future yet", c.Expiry)
	}
	// Websocket ticks of the strikes around the future: the stand-in for
	// any strike whose candle is not fresh. On a fresh start (or after the
	// tick source restarted) they are asked for now and given a moment.
	band = s.bandTokensAt(fut)
	_ = s.feed.subscribe(ctx, band, false)
	load()
	if len(ticks) < 10 && inMarket(time.Now()) {
		sleepCtx(ctx, 2*time.Second)
		load()
	}
	sc := &Scenario{Day: end.Format("2006-01-02"), Minute: end.Format("15:04"), Ts: end.Unix(), Fut: fut, Picks: map[string]*Picks{}}
	atmIdx := c.idx(fut)
	{
		k := c.Strikes[atmIdx]
		var ce, pe float64
		var okC, okP bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); ce, okC = cb.at(c.CE[k].Token) }()
		go func() { defer wg.Done(); pe, okP = cb.at(c.PE[k].Token) }()
		wg.Wait()
		if !okC || !okP {
			return nil, fmt.Errorf("no GreekSoft price for the %.0f pair yet", k)
		}
		sc.ATM, sc.ATMCE, sc.ATMPE, sc.Straddle = k, ce, pe, round2(ce+pe)
	}
	f := fut
	t := c.ExpiryAt.Sub(end).Hours() / (365 * 24)
	if t <= 0 {
		return nil, fmt.Errorf("%s has expired", c.Expiry)
	}
	ivC, okC := impliedVol(sc.ATMCE, f, sc.ATM, t, true)
	ivP, okP := impliedVol(sc.ATMPE, f, sc.ATM, t, false)
	switch {
	case okC && okP:
		sc.ATMIV = round4((ivC + ivP) / 2)
	case okC:
		sc.ATMIV = round4(ivC)
	case okP:
		sc.ATMIV = round4(ivP)
	}

	var wg sync.WaitGroup
	var pmu sync.Mutex
	for key, d := range deltas {
		pk := &Picks{Delta: d}
		sc.Picks[key] = pk
		for _, call := range []bool{true, false} {
			start := c.idx(guessStrike(f, t, sc.ATMIV, d, call))
			if pp := prev.picks(d); pp != nil {
				if call && pp.CE != nil {
					start = c.idx(pp.CE.Strike)
				} else if !call && pp.PE != nil {
					start = c.idx(pp.PE.Strike)
				}
			}
			wg.Add(1)
			go func(call bool, start int, d float64) {
				defer wg.Done()
				p := pickSide(c, cb, f, t, d, call, atmIdx, start)
				pmu.Lock()
				if call {
					pk.CE = p
				} else {
					pk.PE = p
				}
				pmu.Unlock()
			}(call, start, d)
		}
	}
	// The held strikes at the same minute end.
	{
		for _, l := range held {
			wg.Add(1)
			go func(l Leg) {
				defer wg.Done()
				px, ok := cb.at(l.Token)
				if !ok {
					return
				}
				m := HeldMark{Token: l.Token, Strike: l.Strike, Opt: l.Opt, Qty: l.Qty, Price: px, PnL: round2((px - l.Avg) * float64(l.Qty))}
				if iv, ok := impliedVol(px, f, l.Strike, t, l.Opt == "CE"); ok {
					m.Delta = round4(delta76(f, l.Strike, t, iv, l.Opt == "CE"))
				}
				pmu.Lock()
				sc.Held = append(sc.Held, m)
				pmu.Unlock()
			}(l)
		}
	}
	wg.Wait()
	sc.TookMs, sc.Requests = time.Since(t0).Milliseconds(), cb.n
	return sc, nil
}

// minuteEnd computes and stores the scenario of the minute ending at end,
// marks the position's day PnL from it and keeps the live subscription on
// the strikes that matter now. Runs after the intraday minute-end work.
func (s *Service) minuteEnd(end time.Time) (*Scenario, error) {
	s.computing.Lock()
	defer s.computing.Unlock()
	s.mu.Lock()
	if n := len(s.rows); n > 0 && s.rows[n-1].Ts == end.Unix() {
		sc := s.rows[n-1]
		s.mu.Unlock()
		return sc, nil
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	sc, err := s.compute(ctx, end)
	if err != nil {
		log.Printf("[OVERNIGHT] minute %s not computed: %v", end.Format("15:04"), err)
		return nil, err
	}
	// Spot is the index tick the bridge holds (GreekSoft websocket).
	if _, spot, ferr := s.feed.quotes(ctx, nil); ferr == nil {
		sc.Spot = spot
	}

	s.mu.Lock()
	if p := s.st.Position; p != nil && p.ClosedAt == "" {
		s.markDay(p, sc)
	}
	if len(s.rows) > 0 && s.rows[0].Day != sc.Day {
		s.rows = nil
	}
	s.rows = append(s.rows, sc)
	_ = s.st.save(s.dir)
	s.mu.Unlock()

	appendJSONL(filepath.Join(s.dir, sc.Day+"_scenarios.jsonl"), sc)
	pk := sc.picks(s.delta())
	ce, pe := "-", "-"
	if pk != nil && pk.CE != nil {
		ce = fmt.Sprintf("%.0f CE d=%.4f @%.2f %s", pk.CE.Strike, pk.CE.Delta, pk.CE.Price, pk.CE.Src)
	}
	if pk != nil && pk.PE != nil {
		pe = fmt.Sprintf("%.0f PE d=%.4f @%.2f %s", pk.PE.Strike, pk.PE.Delta, pk.PE.Price, pk.PE.Src)
	}
	log.Printf("[OVERNIGHT] %s %s fut=%.2f atm=%.0f straddle=%.2f | %s | %s | pnl=%.2f (%d req, %d ms)",
		s.chain.Expiry, sc.Minute, sc.Fut, sc.ATM, sc.Straddle, ce, pe, sc.PnL, sc.Requests, sc.TookMs)
	s.resubscribe(sc)
	return sc, nil
}

// markDay writes today's mark of the position from a minute-end scenario.
// Caller holds s.mu.
func (s *Service) markDay(p *Position, sc *Scenario) {
	m := &DayMark{Day: sc.Day, Minute: sc.Minute, Realized: round2(p.realized()), Spot: sc.Spot, Fut: sc.Fut, Qty: p.qty(), Closes: map[string]float64{}}
	for _, h := range sc.Held {
		l := p.leg(h.Token)
		if l == nil || l.Qty <= 0 {
			continue
		}
		m.Unrealized += (h.Price - l.Avg) * float64(l.Qty)
		m.Closes[fmt.Sprintf("%.0f%s", l.Strike, l.Opt)] = h.Price
	}
	m.Unrealized = round2(m.Unrealized)
	m.Cum = round2(m.Realized + m.Unrealized)
	prevCum := 0.0
	if n := len(p.Days); n > 0 && p.Days[n-1].Day == sc.Day {
		p.Days[n-1] = m
		if n > 1 {
			prevCum = p.Days[n-2].Cum
		}
	} else {
		if n > 0 {
			prevCum = p.Days[n-1].Cum
		}
		p.Days = append(p.Days, m)
	}
	sc.PnL, sc.DayPnL = m.Cum, round2(m.Cum-prevCum)
}

// bandTokens: gtokens of the future and of every strike within 1500
// points of the last known future price (all strikes before any price is
// known is too many; then just the future) plus the held legs.
func (s *Service) bandTokens() []int64 {
	ref := 0.0
	if sc := s.lastRow(); sc != nil {
		ref = sc.Fut
	}
	return s.bandTokensAt(ref)
}

// bandTokensAt is the band around a given future price (0 = future only).
func (s *Service) bandTokensAt(ref float64) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chain
	if c == nil {
		return nil
	}
	out := []int64{c.Fut.GToken}
	if ref > 0 {
		for _, k := range c.Strikes {
			if math.Abs(k-ref) <= 1500 {
				out = append(out, c.CE[k].GToken, c.PE[k].GToken)
			}
		}
	}
	if p := s.st.Position; p != nil {
		for _, l := range p.Legs {
			if l.Qty > 0 {
				out = append(out, l.GToken)
			}
		}
	}
	return out
}

// resubscribe keeps the bridge sending ticks for the band (re-sent every
// minute: the bridge forgets its list when it restarts).
func (s *Service) resubscribe(_ *Scenario) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.feed.subscribe(ctx, s.bandTokens(), true); err != nil {
		s.setFeedErr(err.Error())
		return
	}
	s.setFeedErr("")
}

func appendJSONL(path string, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func loadScenarios(path string) []*Scenario {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []*Scenario
	r := bufio.NewScanner(f)
	r.Buffer(make([]byte, 1<<20), 1<<22)
	for r.Scan() {
		var sc Scenario
		if json.Unmarshal(r.Bytes(), &sc) == nil && sc.Ts > 0 {
			out = append(out, &sc)
		}
	}
	return out
}
