package overnight

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Service struct {
	live  Broker
	paper *paperBroker
	feed  *feedClient
	dir   string

	computing sync.Mutex // one scenario computation at a time

	mu        sync.Mutex
	chain     *Chain
	chainErr  string
	st        *State
	rows      []*Scenario // the latest day's minute ends
	feedErr   string
	lot, maxQ int64
	limitsDay string
	jobCancel context.CancelFunc
	brokerPos map[int64]int64
	brokerAt  time.Time
	brokerErr string
	liveAt    time.Time
	liveView  *LiveView
}

// New builds the module over the gateway's broker session.
func New(live Broker) *Service {
	dir := dataDir()
	s := &Service{live: live, paper: newPaperBroker(), feed: newFeedClient(), dir: dir, st: loadState(dir)}
	// The latest stored day, so the screen is not empty after a restart.
	if files, _ := filepath.Glob(filepath.Join(dir, "*_scenarios.jsonl")); len(files) > 0 {
		sort.Strings(files)
		s.rows = loadScenarios(files[len(files)-1])
	}
	return s
}

func (s *Service) delta() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Delta
}

func (s *Service) setFeedErr(e string) {
	s.mu.Lock()
	s.feedErr = e
	s.mu.Unlock()
}

// HeldQty is what the module holds at the broker right now, per exchange
// token (LIVE positions only) -- for anything that compares the account's
// net positions against other strategies.
func (s *Service) HeldQty() map[int64]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int64]int64{}
	if p := s.st.Position; p != nil && p.Mode == ModeLive {
		for _, l := range p.Legs {
			if l.Qty != 0 {
				out[l.Token] += l.Qty
			}
		}
	}
	return out
}

// loadChain (re)reads the nearest monthly expiry. While a position is open
// its own expiry stays loaded.
func (s *Service) loadChain(now time.Time) {
	symbol := strings.TrimSpace(os.Getenv("OVERNIGHT_SYMBOL"))
	if symbol == "" {
		symbol = "NIFTY"
	}
	c, err := LoadNearestMonthly(symbol, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.chainErr = err.Error()
		log.Printf("[OVERNIGHT] %v", err)
		return
	}
	if s.chain != nil && s.st.Position != nil && s.st.Position.Expiry != c.Expiry {
		return
	}
	if s.chain == nil || s.chain.Expiry != c.Expiry {
		log.Printf("[OVERNIGHT] monthly expiry %s %s: %d strikes, step %.0f, future token %d", c.Symbol, c.Expiry, len(c.Strikes), c.Step, c.Fut.Token)
		if len(s.rows) > 0 && s.chain != nil {
			s.rows = nil
		}
	}
	s.chain, s.chainErr = c, ""
}

// limits: lot size and the largest single order, read live from the broker
// once a day. PAPER falls back to OVERNIGHT_LOT_SIZE (65) when the broker
// cannot be asked.
func (s *Service) limits(ctx context.Context, mode string) (lot, maxQ int64, err error) {
	day := time.Now().In(ist()).Format("2006-01-02")
	s.mu.Lock()
	c := s.chain
	if s.limitsDay == day && s.lot > 0 {
		lot, maxQ = s.lot, s.maxQ
		s.mu.Unlock()
		return lot, maxQ, nil
	}
	s.mu.Unlock()
	if c == nil {
		return 0, 0, fmt.Errorf("monthly chain not loaded")
	}
	lot, maxQ, err = s.live.Limits(ctx, c.CE[c.Strikes[len(c.Strikes)/2]].Token)
	if err != nil || lot <= 0 || maxQ < lot {
		if mode != ModePaper {
			return 0, 0, fmt.Errorf("lot size / max order quantity not available from the broker: %v", err)
		}
		lot, _ = strconv.ParseInt(strings.TrimSpace(os.Getenv("OVERNIGHT_LOT_SIZE")), 10, 64)
		if lot <= 0 {
			lot = 65
		}
		return lot, lot * 27, nil
	}
	s.mu.Lock()
	s.lot, s.maxQ, s.limitsDay = lot, maxQ, day
	s.mu.Unlock()
	return lot, maxQ, nil
}

// Start runs the minute-end loop until ctx ends. Each minute's scenario is
// computed at second :05 -- after the intraday minute-end capture (:00-:03)
// has had GreekSoft's REST to itself.
func (s *Service) Start(ctx context.Context) {
	go func() {
		s.loadChain(time.Now().In(ist()))
		s.resubscribe(s.lastRow())
		{ // lot size / max order quantity, so the screen shows them before any job
			c2, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, _, _ = s.limits(c2, ModeLive)
			cancel()
		}
		var done int64
		chainDay := ""
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			now := time.Now().In(ist())
			if d := now.Format("2006-01-02"); d != chainDay && now.Hour() >= 9 {
				chainDay = d
				s.loadChain(now)
			}
			if !inMarket(now) || now.Second() < 5 || now.Hour()*100+now.Minute() < 916 {
				continue
			}
			end := now.Truncate(time.Minute)
			if end.Unix() == done {
				continue
			}
			done = end.Unix()
			go func() { _, _ = s.minuteEnd(end) }()
		}
	}()
}

func (s *Service) lastRow() *Scenario {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.rows); n > 0 {
		return s.rows[n-1]
	}
	return nil
}

// LiveLeg is one strike on the live screen, from GreekSoft ticks.
type LiveLeg struct {
	Label  string  `json:"label"`
	Token  int64   `json:"token"`
	Strike float64 `json:"strike"`
	Opt    string  `json:"opt"`
	LTP    float64 `json:"ltp"`
	Bid    float64 `json:"bid"`
	Ask    float64 `json:"ask"`
	Delta  float64 `json:"delta"`
	AgeMs  int64   `json:"age_ms"`
	Qty    int64   `json:"qty,omitempty"`
	Avg    float64 `json:"avg,omitempty"`
	PnL    float64 `json:"pnl,omitempty"`
}

type LiveView struct {
	At         string    `json:"at"`
	Spot       float64   `json:"spot"`
	Fut        float64   `json:"fut"`
	ATM        float64   `json:"atm"`
	Straddle   float64   `json:"straddle"`
	Legs       []LiveLeg `json:"legs"`
	Unrealized float64   `json:"unrealized"`
	Realized   float64   `json:"realized"`
	PnL        float64   `json:"pnl"`
	DayPnL     float64   `json:"day_pnl"`
	Priced     bool      `json:"priced"` // every held leg has a live price
}

// liveNow builds the live screen from the bridge's ticks, at most once a
// second however often the page asks.
func (s *Service) liveNow(ctx context.Context) *LiveView {
	s.mu.Lock()
	if s.liveView != nil && time.Since(s.liveAt) < time.Second {
		v := s.liveView
		s.mu.Unlock()
		return v
	}
	c := s.chain
	var sc *Scenario
	if n := len(s.rows); n > 0 {
		sc = s.rows[n-1]
	}
	d := s.st.Delta
	var legs []Leg
	realized, prevCum := 0.0, 0.0
	today := time.Now().In(ist()).Format("2006-01-02")
	if p := s.st.Position; p != nil {
		for _, l := range p.Legs {
			legs = append(legs, *l)
		}
		realized = p.realized()
		for _, m := range p.Days {
			if m.Day < today {
				prevCum = m.Cum
			}
		}
	}
	s.mu.Unlock()
	if c == nil {
		return nil
	}

	_ = sc
	q, spot, err := s.feed.quotes(ctx, s.bandTokens())
	if err != nil {
		s.setFeedErr(err.Error())
		return nil
	}
	nowMs := time.Now().UnixMilli()
	fut := q[c.Fut.GToken].LTP
	v := &LiveView{At: time.Now().In(ist()).Format("15:04:05"), Spot: spot, Fut: fut, Realized: round2(realized), Priced: true}
	t := c.ExpiryAt.Sub(time.Now()).Hours() / (365 * 24)
	mk := func(label string, in Instrument) LiveLeg {
		t0 := q[in.GToken]
		l := LiveLeg{Label: label, Token: in.Token, Strike: in.Strike, Opt: in.Opt, LTP: t0.LTP, Bid: t0.Bid, Ask: t0.Ask}
		if t0.AtMs > 0 {
			l.AgeMs = nowMs - t0.AtMs
		}
		if px := tickPrice(t0); fut > 0 && px > 0 && t > 0 {
			if iv, ok := impliedVol(px, fut, in.Strike, t, in.Opt == "CE"); ok {
				l.Delta = round4(delta76(fut, in.Strike, t, iv, in.Opt == "CE"))
			}
		}
		return l
	}
	if fut > 0 {
		// Live ATM and live delta strikes: off the future's live price,
		// re-picked every time -- the same rule as the minute end (closest
		// |delta|, 4 decimals, a tie takes the lower strike).
		atm := c.Strikes[c.idx(fut)]
		v.ATM = atm
		ce, pe := mk("ATM", c.CE[atm]), mk("ATM", c.PE[atm])
		if ce.LTP > 0 && pe.LTP > 0 {
			v.Straddle = round2(ce.LTP + pe.LTP)
		}
		v.Legs = append(v.Legs, ce, pe)
		for _, opt := range []string{"CE", "PE"} {
			var best *LiveLeg
			bestGap := 9.0
			for _, k := range c.Strikes { // ascending: the lower strike wins a tie
				if (opt == "CE" && k < atm) || (opt == "PE" && k > atm) || math.Abs(k-fut) > 1500 {
					continue
				}
				in, _ := c.leg(k, opt)
				l := mk("DELTA "+deltaKey(d), in)
				if l.Delta == 0 {
					continue
				}
				if gap := round4(math.Abs(math.Abs(l.Delta) - d)); gap < bestGap {
					bestGap, best = gap, &l
				}
			}
			if best != nil {
				v.Legs = append(v.Legs, *best)
			}
		}
	}
	for i := range legs {
		if legs[i].Qty <= 0 {
			continue
		}
		lg := legs[i]
		l := mk("HELD", Instrument{GToken: lg.GToken, Token: lg.Token, Strike: lg.Strike, Opt: lg.Opt})
		l.Qty, l.Avg = lg.Qty, round2(lg.Avg)
		if l.LTP > 0 {
			l.PnL = round2((l.LTP - lg.Avg) * float64(lg.Qty))
			v.Unrealized += l.PnL
		} else {
			v.Priced = false
		}
		v.Legs = append(v.Legs, l)
	}
	v.Unrealized = round2(v.Unrealized)
	v.PnL = round2(v.Realized + v.Unrealized)
	v.DayPnL = round2(v.PnL - prevCum)
	s.mu.Lock()
	s.liveView, s.liveAt, s.feedErr = v, time.Now(), ""
	s.mu.Unlock()
	return v
}

// refreshBrokerPositions reads the account's net positions (LIVE position
// only, at most every 15 s) so the screen can show broker quantity beside
// the module's own.
func (s *Service) refreshBrokerPositions() {
	s.mu.Lock()
	p := s.st.Position
	if p == nil || p.Mode != ModeLive || time.Since(s.brokerAt) < 15*time.Second {
		s.mu.Unlock()
		return
	}
	s.brokerAt = time.Now()
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m, err := s.live.Positions(ctx)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.brokerErr = err.Error()
			return
		}
		s.brokerPos, s.brokerErr = m, ""
	}()
}
