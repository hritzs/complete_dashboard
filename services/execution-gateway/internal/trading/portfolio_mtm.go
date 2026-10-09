package trading

// Portfolio MTM square-off: a BOOK level and a STOP-LOSS level on the DAY
// MTM of the live account -- every trade of today (closed: its realized cash;
// open: valued through the L1-L5 depth it would close at now, the trade
// MTM exit's own measure; wings excluded). Checked every second in the
// broker session.
//
//	day MTM >= book level (a profit, or a smaller loss: e.g. -10000 while
//	    at -15000 with the SL at -20000): every open trade is closed one at a time,
//	    lot by lot, at depth-checked IOC limits -- each lot priced so the
//	    DAY MTM stays >= the level (the floor is re-read before every lot,
//	    the other trades move too). If the book stops allowing it, the
//	    pass waits and resumes while the day MTM is >= the level again.
//	day MTM <= SL level: every open trade's normal Full Exit, at once.
//
// Square off ALL (POST /api/portfolio/square-off-all): the emergency
// button -- every trade holding a position gets its Full Exit now.
//
// Fires once per day (saving the levels re-arms it). It never blocks new
// entries; trades opened after it finished are not touched that day.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	pmOff           = "OFF"
	pmArmed         = "ARMED"
	pmProfitExiting = "PROFIT_EXITING"
	pmLossExiting   = "LOSS_EXITING"
	pmDoneProfit    = "DONE_PROFIT"
	pmDoneLoss      = "DONE_LOSS"
	pmRetryGap      = 10 * time.Second // a failed Full Exit is retried no sooner
)

// PortfolioMTMConfig is what the user sets (levels in rupees; nil = off).
type PortfolioMTMConfig struct {
	ProfitLevel *float64 `json:"profit_level"` // book at day MTM >= this (may be negative)
	LossLevel   *float64 `json:"loss_level"`   // portfolio SL: day MTM <= this
	BrokerName  string   `json:"broker_name"`
	AccountID   string   `json:"account_id"`
	SavedAt     string   `json:"saved_at,omitempty"`
}

// PortfolioMTMState is today's progress (saved with the config).
type PortfolioMTMState struct {
	Day      string  `json:"day"`
	Status   string  `json:"status"`
	FiredAt  string  `json:"fired_at,omitempty"`
	FiredMTM float64 `json:"fired_mtm,omitempty"`
	DoneAt   string  `json:"done_at,omitempty"`
	Note     string  `json:"note,omitempty"`
	// FiredOpenQty: open contracts (wings out) when it fired -- the
	// square-off progress is FiredOpenQty - the open quantity now.
	FiredOpenQty int64 `json:"fired_open_qty,omitempty"`
}

type pmTrade struct {
	TradeUID string  `json:"trade_uid"`
	Status   string  `json:"status"`
	MTM      float64 `json:"mtm"`
	Open     bool    `json:"open"`
	Priced   bool    `json:"priced"`

	legs  []mtmLeg             // non-wing legs from the fills
	chain *OptionChainSnapshot // its chain (this evaluation)
}

// pmPos is one instrument of the whole portfolio, netted across trades.
type pmPos struct {
	Symbol   string   `json:"symbol"`
	Expiry   string   `json:"expiry"`
	Leg      string   `json:"leg"` // CE / PE
	Strike   float64  `json:"strike"`
	Token    int64    `json:"token"`
	NetQty   int64    `json:"net_qty"`   // + long, - short
	AvgPrice float64  `json:"avg_price"` // avg sell (net short) / buy (net long) price
	LTP      float64  `json:"ltp"`
	Bid      float64  `json:"bid"`
	Ask      float64  `json:"ask"`
	ClosePx  float64  `json:"close_px"` // VWAP closing the whole qty through L1-L5 (short: asks, long: bids)
	CloseTo  float64  `json:"close_to"` // deepest level that close reaches
	MTM      float64  `json:"mtm"`      // cash of every fill + closing value now
	MTMAtLTP float64  `json:"mtm_ltp"`  // same, valued at LTP
	Delta    float64  `json:"delta"`    // position greeks (per-unit x signed qty)
	Gamma    float64  `json:"gamma"`
	Theta    float64  `json:"theta"`
	Vega     float64  `json:"vega"`
	IV       float64  `json:"iv"`
	Wing     bool     `json:"wing"` // RM only: outside the MTM and the greek totals
	Trades   []string `json:"trades"`
}

// pmGreeks: the portfolio's net greeks (wings excluded, as everywhere).
type pmGreeks struct {
	Delta     float64 `json:"delta"`
	Gamma     float64 `json:"gamma"`
	Theta     float64 `json:"theta"`
	Vega      float64 `json:"vega"`
	WingDelta float64 `json:"wing_delta"`
}

// PortfolioMTMView is the live day MTM and the whole position (not saved).
type PortfolioMTMView struct {
	At        string    `json:"at"`
	DayMTM    float64   `json:"day_mtm"`
	DayMTMLTP float64   `json:"day_mtm_ltp"` // the same day MTM with open legs at LTP
	Realized  float64   `json:"realized"`    // cash of every flat instrument (wings out)
	Priced    bool      `json:"priced"`      // every open trade could be valued
	Error     string    `json:"error,omitempty"`
	OpenQty   int64     `json:"open_qty"` // open contracts, wings excluded
	Greeks    pmGreeks  `json:"greeks"`
	Positions []pmPos   `json:"positions"`
	Trades    []pmTrade `json:"trades"`
}

type pmFile struct {
	Config PortfolioMTMConfig `json:"config"`
	State  PortfolioMTMState  `json:"state"`
}

var pm struct {
	mu       sync.Mutex
	loaded   bool
	cfg      PortfolioMTMConfig
	st       PortfolioMTMState
	view     PortfolioMTMView
	running  bool                 // a profit pass is closing trades
	lastTry  map[string]time.Time // loss side: last Full Exit attempt per trade
	evalLock sync.Mutex           // one day-MTM evaluation at a time
	lastSnap string               // HH:MM of the last minute snapshot saved
	lastEval time.Time            // last day-MTM evaluation (throttled while OFF)
}

func pmPath() string {
	if p := strings.TrimSpace(os.Getenv("PORTFOLIO_MTM_PATH")); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(lutDataDir()), "portfolio_mtm.json")
}

func pmToday() string { return time.Now().In(lutIST()).Format("2006-01-02") }

func pmDefaults(c PortfolioMTMConfig) PortfolioMTMConfig {
	if strings.TrimSpace(c.BrokerName) == "" {
		c.BrokerName = sbLiveBroker
	}
	if strings.TrimSpace(c.AccountID) == "" {
		c.AccountID = sbLiveAccount()
	}
	return c
}

func pmArmedStatus(c PortfolioMTMConfig) string {
	if c.ProfitLevel == nil && c.LossLevel == nil {
		return pmOff
	}
	return pmArmed
}

// pmLoadLocked reads the saved config/state once. Caller holds pm.mu.
func pmLoadLocked() {
	if pm.loaded {
		return
	}
	pm.loaded = true
	var f pmFile
	if b, err := stateRead(pmPath()); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	pm.cfg, pm.st = pmDefaults(f.Config), f.State
	pm.lastTry = map[string]time.Time{}
}

// pmSaveLocked persists config + state. Caller holds pm.mu.
func pmSaveLocked() {
	b, _ := json.MarshalIndent(pmFile{Config: pm.cfg, State: pm.st}, "", "  ")
	if err := os.MkdirAll(filepath.Dir(pmPath()), 0o755); err == nil {
		if err := sbWriteAtomic(pmPath(), b); err != nil {
			log.Printf("[PORTFOLIO-MTM] save: %v", err)
		}
	}
}

// pmOpenStatus: a trade the portfolio rule acts on (still holds a position).
func pmOpenStatus(status string) bool {
	switch status {
	case "", "FAILED", "RECONCILIATION_REQUIRED", sbStatusClosed:
		return false
	}
	return !isTerminalTradeStatus(status)
}

// pmDayMTM values every trade of today on the account.
func (s *Service) pmDayMTM(ctx context.Context, cfg PortfolioMTMConfig) (float64, map[string]pmTrade, bool, error) {
	total, out, priced, _, err := s.pmEvaluate(ctx, cfg, false)
	return total, out, priced, err
}

// pmEvaluate values every trade of today on the account; withView also
// nets the whole position by instrument with its greeks.
func (s *Service) pmEvaluate(ctx context.Context, cfg PortfolioMTMConfig, withView bool) (float64, map[string]pmTrade, bool, *PortfolioMTMView, error) {
	pm.evalLock.Lock()
	defer pm.evalLock.Unlock()
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok || s.Snapshot == nil {
		return 0, nil, false, nil, fmt.Errorf("postgres store / snapshot unavailable")
	}
	day := time.Now().In(lutIST())
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, lutIST())
	sums, err := pg.TradeSummaries(ctx, from, from.Add(24*time.Hour))
	if err != nil {
		return 0, nil, false, nil, err
	}
	chains := map[string]*OptionChainSnapshot{}
	chainOf := func(sym, exp string) *OptionChainSnapshot {
		key := sym + "|" + exp
		c, ok := chains[key]
		if !ok {
			if cc, cerr := s.Snapshot.GetOptionChain(ctx, sym, exp); cerr == nil {
				c = cc
			}
			chains[key] = c
		}
		return c
	}
	var agg *pmAgg
	if withView {
		agg = newPMAgg()
	}
	out := map[string]pmTrade{}
	total, priced := 0.0, true
	for _, t := range sums {
		if !strings.EqualFold(t.BrokerName, cfg.BrokerName) || strings.TrimSpace(t.AccountID) != strings.TrimSpace(cfg.AccountID) || t.Status == "FAILED" {
			continue
		}
		legs := mtmLegsFromExecutions(t.Executions)
		if agg != nil {
			agg.add(t, chainOf(t.Symbol, t.Expiry))
		}
		p := pmTrade{TradeUID: t.TradeUID, Status: t.Status, Priced: true, legs: legs}
		for _, l := range legs {
			p.Open = p.Open || l.NetShort != 0
		}
		if !p.Open {
			for _, l := range legs {
				p.MTM += l.Cash
			}
		} else {
			p.chain = chainOf(t.Symbol, t.Expiry)
			p.MTM, p.Priced = mtmExecMTM(legs, mtmRowLookup(p.chain))
			priced = priced && p.Priced
		}
		total += p.MTM
		out[t.TradeUID] = p
	}
	var view *PortfolioMTMView
	if agg != nil {
		view = agg.view()
		view.DayMTM, view.Priced = total, priced
	}
	return total, out, priced, view, nil
}

// pmAgg nets every fill of the day by instrument.
type pmAgg struct {
	pos   map[string]*pmPos
	acc   map[string]*pmFlows
	order []string
}

type pmFlows struct {
	soldQty, boughtQty int64
	soldVal, boughtVal float64
	row                *OptionChainRow
}

func newPMAgg() *pmAgg { return &pmAgg{pos: map[string]*pmPos{}, acc: map[string]*pmFlows{}} }

func (a *pmAgg) add(t TradeSummary, chain *OptionChainSnapshot) {
	rowOf := mtmRowLookup(chain)
	for _, e := range t.Executions {
		if e.FilledQty <= 0 || e.AvgPrice <= 0 || e.Leg == "" {
			continue
		}
		wing := isWingExecution(e)
		leg := strings.ToUpper(e.Leg)
		key := fmt.Sprintf("%s|%s|%d|%v", t.Symbol, leg, e.token, wing)
		p, ok := a.pos[key]
		if !ok {
			p = &pmPos{Symbol: t.Symbol, Expiry: t.Expiry, Leg: leg, Strike: e.Strike, Token: e.token, Wing: wing}
			a.pos[key], a.acc[key] = p, &pmFlows{row: rowOf(mtmLeg{Leg: leg, Token: e.token})}
			a.order = append(a.order, key)
		}
		f := a.acc[key]
		if len(p.Trades) == 0 || p.Trades[len(p.Trades)-1] != t.TradeUID {
			p.Trades = append(p.Trades, t.TradeUID)
		}
		v := float64(e.FilledQty) * e.AvgPrice
		if strings.EqualFold(e.Side, "SELL") {
			f.soldQty, f.soldVal = f.soldQty+e.FilledQty, f.soldVal+v
		} else {
			f.boughtQty, f.boughtVal = f.boughtQty+e.FilledQty, f.boughtVal+v
		}
	}
}

func (a *pmAgg) view() *PortfolioMTMView {
	v := &PortfolioMTMView{}
	for _, key := range a.order {
		p, f := a.pos[key], a.acc[key]
		cash := f.soldVal - f.boughtVal
		p.NetQty = f.boughtQty - f.soldQty
		if p.NetQty == 0 {
			if !p.Wing {
				v.Realized += cash
				v.DayMTMLTP += cash
			}
			continue // flat: only its realized cash counts
		}
		if p.NetQty < 0 && f.soldQty > 0 {
			p.AvgPrice = f.soldVal / float64(f.soldQty)
		} else if f.boughtQty > 0 {
			p.AvgPrice = f.boughtVal / float64(f.boughtQty)
		}
		qty := abs64(p.NetQty)
		if r := f.row; r != nil {
			p.LTP, p.Bid, p.Ask, p.IV = r.PELtp, r.PEBid, r.PEAsk, r.PEIV
			d, g, th, vg := r.PEDelta, r.PEGamma, r.PETheta, r.PEVega
			if p.Leg == "CE" {
				p.LTP, p.Bid, p.Ask, p.IV = r.CELtp, r.CEBid, r.CEAsk, r.CEIV
				d, g, th, vg = r.CEDelta, r.CEGamma, r.CETheta, r.CEVega
			}
			n := float64(p.NetQty)
			p.Delta, p.Gamma, p.Theta, p.Vega = d*n, g*n, th*n, vg*n
			buy := p.NetQty < 0 // a short closes by buying
			p.ClosePx, p.CloseTo, _ = mtmWalk(mtmBook(r, p.Leg, buy), qty)
		}
		closeVal := func(px float64) float64 {
			if p.NetQty < 0 {
				return -float64(qty) * px
			}
			return float64(qty) * px
		}
		p.MTM, p.MTMAtLTP = cash+closeVal(p.ClosePx), cash+closeVal(p.LTP)
		if p.Wing {
			v.Greeks.WingDelta += p.Delta
		} else {
			v.OpenQty += qty
			v.DayMTMLTP += p.MTMAtLTP
			v.Greeks.Delta += p.Delta
			v.Greeks.Gamma += p.Gamma
			v.Greeks.Theta += p.Theta
			v.Greeks.Vega += p.Vega
		}
		v.Positions = append(v.Positions, *p)
	}
	sort.Slice(v.Positions, func(i, j int) bool {
		a, b := v.Positions[i], v.Positions[j]
		if a.Wing != b.Wing {
			return !a.Wing
		}
		if a.Strike != b.Strike {
			return a.Strike < b.Strike
		}
		return a.Leg < b.Leg
	})
	return v
}

// StartPortfolioMTM runs the portfolio rule every second.
func (s *Service) StartPortfolioMTM() {
	pm.mu.Lock()
	pmLoadLocked()
	pm.mu.Unlock()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			s.pmTick()
		}
	}()
}

func (s *Service) pmTick() {
	pm.mu.Lock()
	pmLoadLocked()
	if pm.st.Day != pmToday() {
		// Every new day starts DISARMED (levels kept); "Save & arm" arms it.
		pm.st = PortfolioMTMState{Day: pmToday(), Status: pmOff, Note: "new day -- disarmed; Save & arm to use the levels"}
		pmSaveLocked()
	}
	cfg, st := pm.cfg, pm.st
	// Execution first: while the rule is OFF / done the evaluation only
	// feeds the screen -- every 3 s instead of every second.
	// And never inside the minute-end window, where the closes / hedges run.
	if (st.Status == pmOff || st.Status == pmDoneProfit || st.Status == pmDoneLoss) && (time.Since(pm.lastEval) < 3*time.Second || gsBoundary(time.Now())) {
		pm.mu.Unlock()
		return
	}
	pm.lastEval = time.Now()
	pm.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	total, trades, priced, vp, err := s.pmEvaluate(ctx, cfg, true)
	cancel()
	view := PortfolioMTMView{DayMTM: total, Priced: priced}
	if vp != nil {
		view = *vp
	}
	view.At = time.Now().In(lutIST()).Format("15:04:05")
	if err != nil {
		view.Error = err.Error()
	}
	for _, t := range trades {
		view.Trades = append(view.Trades, t)
	}
	sort.Slice(view.Trades, func(i, j int) bool { return view.Trades[i].TradeUID < view.Trades[j].TradeUID })
	pm.mu.Lock()
	pm.view = view
	minute := view.At[:5]
	snap := minute != pm.lastSnap
	pm.lastSnap = minute
	pm.mu.Unlock()
	// Postgres: the live view (latest, every second) and one snapshot a minute.
	if b, jerr := json.Marshal(map[string]interface{}{"config": cfg, "state": st, "view": view}); jerr == nil {
		stateMirrorKey("portfolio_live_view", b)
		if snap && sbLiveWindow(time.Now().In(lutIST())) { // one a minute, in the session only
			stateEvent("portfolio_snapshot", "portfolio_mtm", pmToday(), b)
		}
	}
	if err != nil || st.Status == pmOff || st.Status == pmDoneProfit || st.Status == pmDoneLoss || !sbLiveWindow(time.Now().In(lutIST())) {
		return
	}

	open := pmOpenTrades(trades)
	lossHit := cfg.LossLevel != nil && priced && total <= *cfg.LossLevel
	profitHit := cfg.ProfitLevel != nil && priced && total >= *cfg.ProfitLevel

	switch {
	case st.Status == pmLossExiting || lossHit:
		if st.Status != pmLossExiting {
			s.pmSetFiredQty(view.OpenQty)
			s.pmSetStatus(pmLossExiting, total, fmt.Sprintf("day MTM %.2f <= portfolio SL %.2f -- Full Exit on %d open trade(s)", total, *cfg.LossLevel, len(open)))
		}
		if len(open) == 0 {
			s.pmSetStatus(pmDoneLoss, total, "every trade closed")
			return
		}
		for _, uid := range open {
			s.pmFullExit(uid)
		}
	case st.Status == pmProfitExiting || profitHit:
		if st.Status != pmProfitExiting {
			s.pmSetFiredQty(view.OpenQty)
			s.pmSetStatus(pmProfitExiting, total, fmt.Sprintf("day MTM %.2f >= book level %.2f -- closing %d open trade(s) lot by lot (IOC), day MTM kept >= the level", total, *cfg.ProfitLevel, len(open)))
		}
		if len(open) == 0 {
			s.pmSetStatus(pmDoneProfit, total, "every trade closed")
			return
		}
		pm.mu.Lock()
		if pm.running || !profitHit {
			if !pm.running && !profitHit {
				pm.st.Note = fmt.Sprintf("waiting: day MTM %.2f below the book level %.2f -- resumes when it is back above", total, *cfg.ProfitLevel)
			}
			pm.mu.Unlock()
			return
		}
		pm.running = true
		pm.mu.Unlock()
		go s.pmProfitPass(cfg, open)
	}
}

// pmOpenTrades: the trades holding a position. A Straddle Build still
// waiting to sell holds nothing: left alone (new entries never blocked).
func pmOpenTrades(trades map[string]pmTrade) []string {
	var open []string
	for uid, t := range trades {
		if pmOpenStatus(t.Status) && (t.Open || t.Status != sbStatusLive) {
			open = append(open, uid)
		}
	}
	sort.Strings(open)
	return open
}

func (s *Service) pmSetFiredQty(q int64) {
	pm.mu.Lock()
	if pm.st.FiredOpenQty == 0 {
		pm.st.FiredOpenQty = q
	}
	pm.mu.Unlock()
}

func (s *Service) pmSetStatus(status string, mtm float64, note string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	now := time.Now().In(lutIST()).Format("15:04:05")
	if status == pmProfitExiting || status == pmLossExiting {
		if pm.st.FiredAt == "" || pm.st.Status == pmProfitExiting {
			pm.st.FiredAt, pm.st.FiredMTM = now, mtm
		}
	}
	if status == pmDoneProfit || status == pmDoneLoss {
		pm.st.DoneAt = now
	}
	pm.st.Status, pm.st.Note = status, note
	pmSaveLocked()
	if b, err := json.Marshal(map[string]interface{}{"state": pm.st, "config": pm.cfg, "day_mtm": mtm}); err == nil {
		stateEvent("portfolio_status", "portfolio_mtm", pm.st.Day, b)
	}
	log.Printf("[PORTFOLIO-MTM] %s: %s", status, note)
}

// pmSBRule returns the Straddle Build rule still running this live trade
// (its own monitor holds the position until hand-off).
func pmSBRule(tradeUID string) string {
	for _, r := range sbEng.list() {
		r.mu.Lock()
		uid, phase, id := r.tradeUID, r.phase, r.cfg.ID
		r.mu.Unlock()
		if uid == tradeUID && phase != sbPhaseHandedOff && phase != "EXITED" {
			return id
		}
	}
	return ""
}

// pmFullExit: the trade's normal Full Exit (a Straddle Build still holding
// its position: the build's own Exit), in the background, retried no more
// often than pmRetryGap.
func (s *Service) pmFullExit(uid string) {
	pm.mu.Lock()
	if time.Since(pm.lastTry[uid]) < pmRetryGap {
		pm.mu.Unlock()
		return
	}
	pm.lastTry[uid] = time.Now()
	pm.mu.Unlock()
	if id := pmSBRule(uid); id != "" {
		if err := s.ExitStraddleBuild(id, "EXIT"); err != nil {
			log.Printf("[PORTFOLIO-MTM] trade=%s straddle build %s exit: %v", uid, id, err)
		}
		return
	}
	s.runExitAsync(uid, "PORTFOLIO", func() {
		if err := s.SquareOff(uid, "PORTFOLIO"); err != nil {
			log.Printf("[PORTFOLIO-MTM] trade=%s Full Exit: %v -- retried", uid, err)
		}
	})
}

// pmCand is one closing lot the portfolio pass could send next.
type pmCand struct {
	uid    string
	leg    mtmLeg
	qty    int64
	frac   float64 // open now / open when the pass started (1 = untouched)
	dDelta float64 // change of the portfolio's net delta if it fills
	row    *OptionChainRow
}

// pmPickOrder orders the candidate lots for an even, delta-managed wind
// down: the leg that is most BEHIND (largest share still open) goes first,
// so every trade and every leg (straddle and hedges) shrinks at the same
// pace; but a lot that would push |net delta| beyond max(|delta now|,
// band) goes after every lot that doesn't (those by the resulting |delta|).
func pmPickOrder(c []pmCand, delta, band float64) []pmCand {
	limit := math.Max(math.Abs(delta), band)
	out := append([]pmCand(nil), c...)
	ok := func(x pmCand) bool { return math.Abs(delta+x.dDelta) <= limit+1e-9 }
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ok(a) != ok(b) {
			return ok(a)
		}
		if !ok(a) {
			return math.Abs(delta+a.dDelta) < math.Abs(delta+b.dDelta)
		}
		if math.Abs(a.frac-b.frac) > 1e-9 {
			return a.frac > b.frac
		}
		return a.qty > b.qty
	})
	return out
}

func pmUnitDelta(row *OptionChainRow, leg string) float64 {
	if row == nil {
		return 0
	}
	if leg == "CE" {
		return row.CEDelta
	}
	return row.PEDelta
}

// pmProfitPass closes every open trade together, lot by lot at
// depth-checked IOC limits, so the DAY MTM stays >= the book level:
//   - even: each lot comes from the leg most behind (share still open), so
//     all trades and legs shrink at the same pace;
//   - delta-managed: a lot is sent only if the portfolio's net delta stays
//     within max(|delta now|, ~one lot) -- else the lot that brings it
//     closest to zero;
//   - each lot's limit keeps the whole day MTM >= the level even if it
//     fills at the limit (the portfolio's slack, not one trade's).
//
// A trade whose legs are all flat is finished (wings closed, CLOSED_PORTFOLIO,
// monitor stopped). If the book stops allowing the level the pass pauses;
// unfinished trades go back to ACTIVE (their wings follow the short already
// removed) and the next tick resumes while the day MTM is >= the level.
func (s *Service) pmProfitPass(cfg PortfolioMTMConfig, open []string) {
	defer func() {
		pm.mu.Lock()
		pm.running = false
		pm.mu.Unlock()
	}()
	level := *cfg.ProfitLevel
	note := func(format string, a ...interface{}) {
		msg := fmt.Sprintf(format, a...)
		pm.mu.Lock()
		pm.st.Note = msg
		pm.mu.Unlock()
		log.Printf("[PORTFOLIO-MTM] %s", msg)
	}

	// Take every ACTIVE trade for this pass (one exit per trade at a time).
	type held struct {
		tr         StoredTrade
		start      map[int64]int64 // |net| per leg token at the pass start
		scCE, scPE int64           // short removed (for the wings on a pause)
		executor   Executor
	}
	pass := map[string]*held{}
	for _, uid := range open {
		tr, ok := s.Store.LoadTrade(uid)
		if !ok || !pmOpenStatus(tr.Status) {
			continue
		}
		if tr.Status != "ACTIVE" {
			if pmSBRule(uid) != "" {
				s.pmFullExit(uid) // a build still holding its own position: the build's Exit
			}
			continue
		}
		if _, busy := exitsInFlight.LoadOrStore(uid, "PORTFOLIO"); busy {
			continue // another exit is running on it
		}
		ex, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
		if err != nil || s.OrderEvents == nil {
			exitsInFlight.Delete(uid)
			note("trade %s: executor / order events unavailable (%v) -- skipped this pass", uid, err)
			continue
		}
		pass[uid] = &held{tr: tr, start: map[int64]int64{}, executor: ex}
	}
	release := func(uid string, h *held, finished bool) {
		if !finished {
			if tr, ok := s.Store.LoadTrade(uid); ok && tr.Status == "SQUARING_OFF" {
				if h.scCE != 0 || h.scPE != 0 {
					if err := s.adjustWings(context.Background(), h.executor, tr, h.scCE, h.scPE, tr.Strike, tr.Strike, "PSQF-PORTFOLIO"); err != nil {
						log.Printf("[WINGS] ⚠ portfolio partial wing reduction for %s: %v", uid, err)
					}
				}
				tr.Status, tr.LastUpdateTime = "ACTIVE", time.Now()
				s.Store.UpdateTrade(tr)
			}
		}
		exitsInFlight.Delete(uid)
	}
	defer func() {
		for uid, h := range pass {
			release(uid, h, false)
		}
	}()
	if len(pass) == 0 {
		return
	}

	evaluate := func() (float64, map[string]pmTrade, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		total, trades, priced, err := s.pmDayMTM(ctx, cfg)
		if err == nil && !priced {
			err = fmt.Errorf("an open leg has no price in the chain")
		}
		return total, trades, err
	}
	total, trades, err := evaluate()
	if err != nil {
		note("pass not started: %v -- retry next tick", err)
		return
	}
	for uid, h := range pass {
		for _, l := range trades[uid].legs {
			if l.NetShort != 0 {
				h.start[l.Token] = abs64(l.NetShort)
			}
		}
		h.tr.Status, h.tr.LastUpdateTime = "SQUARING_OFF", time.Now()
		s.Store.UpdateTrade(h.tr)
	}
	note("closing %d trade(s) together, lot by lot (IOC): evenly across trades and legs, net delta kept in a band, day MTM >= %.2f (now %.2f)", len(pass), level, total)

	misses := 0
	for step := 0; step < 20000 && len(pass) > 0; step++ {
		pm.mu.Lock()
		disarmed := pm.st.Status != pmProfitExiting
		pm.mu.Unlock()
		if disarmed {
			note("stopped: the portfolio rule was disarmed -- open trades stay monitored as before")
			return
		}
		if step > 0 {
			if total, trades, err = evaluate(); err != nil {
				note("paused: %v -- resumes next tick", err)
				return
			}
		}
		if total < level {
			note("paused: day MTM %.2f fell below the book level %.2f -- the rest stays monitored; resumes when it is back above", total, level)
			return
		}
		// Every open lot of the pass, and the portfolio's net delta.
		delta, band := 0.0, 0.0
		var cands []pmCand
		for uid, h := range pass {
			t := trades[uid]
			rowOf := mtmRowLookup(t.chain)
			lot := int64(h.tr.LotSize)
			anyOpen := false
			for _, l := range t.legs {
				if l.NetShort == 0 {
					continue
				}
				anyOpen = true
				row := rowOf(l)
				ud := pmUnitDelta(row, l.Leg)
				delta += ud * float64(-l.NetShort)
				band = math.Max(band, 0.6*float64(lot))
				q := min(lot, abs64(l.NetShort))
				st := h.start[l.Token]
				if st <= 0 {
					st = abs64(l.NetShort)
				}
				sign := 1.0
				if l.NetShort < 0 {
					sign = -1
				}
				cands = append(cands, pmCand{uid: uid, leg: l, qty: q, frac: float64(abs64(l.NetShort)) / float64(st), dDelta: ud * sign * float64(q), row: row})
			}
			if !anyOpen { // every non-wing leg flat: finish it
				func() {
					defer s.lockTrade(uid)()
					if tr, ok := s.Store.LoadTrade(uid); ok {
						s.mtmExitFinish(tr, "CLOSED_PORTFOLIO")
					}
				}()
				exitsInFlight.Delete(uid)
				delete(pass, uid)
			}
		}
		if len(cands) == 0 {
			continue
		}
		// First lot (in the even / delta order) whose IOC can keep the level.
		var pick *pmCand
		var limit float64
		for _, c := range pmPickOrder(cands, delta, band) {
			if c.row == nil {
				continue
			}
			buy := c.leg.NetShort > 0
			if lim, ok := mtmLotLimit(mtmBook(c.row, c.leg.Leg, buy), c.qty, buy, total, level); ok {
				cc := c
				pick, limit = &cc, lim
				break
			}
		}
		if pick == nil {
			misses++
			if misses >= mtmMaxMisses {
				note("paused: no lot can be closed at a price that keeps the day MTM >= %.2f right now -- resumes next tick", level)
				return
			}
			time.Sleep(mtmBookRefresh)
			continue
		}
		h := pass[pick.uid]
		buy := pick.leg.NetShort > 0
		side := map[bool]string{true: "BUY", false: "SELL"}[buy]
		var got int64
		var px float64
		var status string
		var unknown bool
		func() {
			defer s.lockTrade(pick.uid)()
			got, px, status, unknown = s.mtmSendIOC(h.executor, h.tr, pick.leg, side, pick.qty, limit)
			if unknown || got == 0 {
				return
			}
			tr, ok := s.Store.LoadTrade(pick.uid)
			if !ok {
				return
			}
			if pick.leg.Token == tr.CEToken && tr.CEQty > 0 {
				tr.CEQty = int(max(0, int64(tr.CEQty)-got))
			}
			if pick.leg.Token == tr.PEToken && tr.PEQty > 0 {
				tr.PEQty = int(max(0, int64(tr.PEQty)-got))
			}
			tr.LastUpdateTime = time.Now()
			s.Store.UpdateTrade(tr)
			h.tr = tr
		}()
		if unknown {
			if tr, ok := s.Store.LoadTrade(pick.uid); ok {
				tr.Status, tr.LastUpdateTime = "RECONCILIATION_REQUIRED", time.Now()
				s.Store.UpdateTrade(tr)
			}
			exitsInFlight.Delete(pick.uid)
			delete(pass, pick.uid)
			note("STOPPED: trade %s %s %s %d @%.2f IOC outcome unknown (%s) -- RECONCILIATION_REQUIRED, check the broker; pass stopped", pick.uid, side, pick.leg.Leg, pick.qty, limit, status)
			return
		}
		if got == 0 {
			misses++
			if misses >= mtmMaxMisses {
				note("paused: %d IOCs in a row not filled -- resumes next tick", misses)
				return
			}
			time.Sleep(mtmBookRefresh)
			continue
		}
		misses = 0
		if pick.leg.Leg == "CE" {
			h.scCE += shortChangeFromFill(side, got)
		} else {
			h.scPE += shortChangeFromFill(side, got)
		}
		log.Printf("[PORTFOLIO-MTM] trade=%s %s %s %.0f %d/%d @%.2f (IOC limit %.2f) -- day MTM before %.2f, level %.2f, net delta before %+.2f (lot %+.2f), leg %.0f%% still open",
			pick.uid, side, pick.leg.Leg, pick.leg.Strike, got, pick.qty, px, limit, total, level, delta, pick.dDelta, pick.frac*100)
		time.Sleep(mtmBookRefresh)
	}
}

// PortfolioMTMHandler: GET /api/portfolio/mtm-exit -> config, today's
// state and the live day MTM. POST {profit_level, loss_level} (null = off)
// saves and re-arms for today.
func (h *Handlers) PortfolioMTMHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var c struct {
			PortfolioMTMConfig
			Disarm bool `json:"disarm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		if c.Disarm {
			pm.mu.Lock()
			pmLoadLocked()
			// A running book-level pass sees OFF before its next lot and stops
			// (orders already sent finish); trades stay monitored as before.
			pm.st = PortfolioMTMState{Day: pmToday(), Status: pmOff, Note: "disarmed by user at " + time.Now().In(lutIST()).Format("15:04:05")}
			pmSaveLocked()
			if b, err := json.Marshal(map[string]interface{}{"state": pm.st, "config": pm.cfg, "saved_by": "user"}); err == nil {
				stateEvent("portfolio_status", "portfolio_mtm", pm.st.Day, b)
			}
			out := map[string]interface{}{"success": true, "config": pm.cfg, "state": pm.st, "view": pm.view, "running": pm.running}
			pm.mu.Unlock()
			log.Printf("[PORTFOLIO-MTM] disarmed by user (levels kept: book %s / SL %s)", pmLevelText(pm.cfg.ProfitLevel), pmLevelText(pm.cfg.LossLevel))
			lutJSON(w, http.StatusOK, out)
			return
		}
		cfg := c.PortfolioMTMConfig
		if cfg.ProfitLevel != nil && cfg.LossLevel != nil && *cfg.LossLevel >= *cfg.ProfitLevel {
			lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "the portfolio SL must be below the book level"})
			return
		}
		c2 := pmDefaults(cfg)
		c2.SavedAt = time.Now().In(lutIST()).Format("15:04:05")
		pm.mu.Lock()
		pmLoadLocked()
		if pm.running {
			pm.mu.Unlock()
			lutJSON(w, http.StatusConflict, map[string]interface{}{"success": false, "error": "a portfolio square-off pass is running -- try again in a moment"})
			return
		}
		pm.cfg = c2
		pm.st = PortfolioMTMState{Day: pmToday(), Status: pmArmedStatus(c2)}
		pmSaveLocked()
		if b, err := json.Marshal(map[string]interface{}{"state": pm.st, "config": pm.cfg, "saved_by": "user"}); err == nil {
			stateEvent("portfolio_status", "portfolio_mtm", pm.st.Day, b)
		}
		pm.mu.Unlock()
		log.Printf("[PORTFOLIO-MTM] saved: book at >= %s / SL <= %s on %s/%s -- %s", pmLevelText(c2.ProfitLevel), pmLevelText(c2.LossLevel), c2.BrokerName, c2.AccountID, pmArmedStatus(c2))
	}
	pm.mu.Lock()
	pmLoadLocked()
	out := map[string]interface{}{"success": true, "config": pm.cfg, "state": pm.st, "view": pm.view, "running": pm.running}
	pm.mu.Unlock()
	lutJSON(w, http.StatusOK, out)
}

func pmLevelText(v *float64) string {
	if v == nil {
		return "∞"
	}
	return fmt.Sprintf("%.2f", *v)
}

const pmSquareOffAllConfirm = "SQUARE OFF ALL"

// PortfolioSquareOffAll: POST /api/portfolio/square-off-all
// {"confirm": "SQUARE OFF ALL"} -- emergency: every trade on the live
// account holding a position gets its normal Full Exit NOW (a Straddle
// Build still holding its position: the build's Exit). Straddle Build
// rules still waiting to sell and the LUT build are not touched.
func (h *Handlers) PortfolioSquareOffAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b struct {
		Confirm string `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if strings.TrimSpace(b.Confirm) != pmSquareOffAllConfirm {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "needs the confirmation text " + pmSquareOffAllConfirm})
		return
	}
	s := h.Service
	pm.mu.Lock()
	pmLoadLocked()
	cfg := pm.cfg
	pm.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, trades, _, err := s.pmDayMTM(ctx, cfg)
	cancel()
	if err != nil {
		lutJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	open := pmOpenTrades(trades)
	result := map[string]string{}
	for _, uid := range open {
		if id := pmSBRule(uid); id != "" {
			if err := s.ExitStraddleBuild(id, "EXIT"); err != nil {
				result[uid] = "straddle build exit: " + err.Error()
			} else {
				result[uid] = "straddle build exit started"
			}
			continue
		}
		u := uid
		if s.runExitAsync(u, "SQUARE_OFF_ALL", func() {
			if err := s.SquareOff(u, "EMERGENCY"); err != nil {
				log.Printf("[SQUARE-OFF-ALL] trade=%s Full Exit: %v", u, err)
			}
		}) {
			result[uid] = "Full Exit started"
		} else {
			result[uid] = "an exit is already running on it"
		}
	}
	log.Printf("[SQUARE-OFF-ALL] by user on %s/%s: %d trade(s): %v", cfg.BrokerName, cfg.AccountID, len(open), result)
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "trades": result})
}
