package trading

// Straddle-target build -- SHADOW MODE (no broker orders).
//
// Several rules can build at the same time; each rule has its own run
// (config, PMS, risk, audit trail). Each cycle, per run: PMS position ->
// remaining = 2 x straddles - filled -> live ATM (re-read every tranche, so
// the build follows the ATM when it moves; the PMS holds every strike and
// all checks use the whole position) -> fresh L1-L5 books ->
// PlanDepthEntry -> OMS sends the tranche one lot per order, CE/PE
// interleaved -> every verified fill is pushed to PMS at once. Every minute
// (first tick) the normal risk checks run on the PMS position even while
// building: hedge, SL / TP (per FILLED straddle), exit time. Any exit stops
// that run for good, then flattens what its PMS holds.
//
// Rules building the same symbol + expiry split the participation (two
// rules at 50% -> 25% each) so together they never take more than 50% of
// the fillable book.
//
// Every run is saved to disk after each change; on a restart today's
// BUILDING / COMPLETE runs are restored (PMS rebuilt from the saved fills)
// and continue where they left off after the minute-end checks re-run.
// Here OMS "fills" are simulated against the live book.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SBConfig is one rule (what the user sets).
type SBConfig struct {
	ID             string  `json:"id"`
	Name           string  `json:"name,omitempty"`
	Symbol         string  `json:"symbol"`           // default NIFTY
	Expiry         string  `json:"expiry,omitempty"` // blank = nearest
	TargetStraddle float64 `json:"target_straddle"`  // sell only above this
	Straddles      int64   `json:"straddles"`        // straddle qty in CONTRACTS per leg (lot multiple); total = 2 x this
	Participation  float64 `json:"participation"`    // default 0.5 (split across rules on the same book)
	MaxLevels      int     `json:"max_levels"`       // default 5
	TrancheGapMs   int     `json:"tranche_gap_ms"`   // min gap between tranches, default 1000
	SLBps          float64 `json:"sl_bps"`           // 0 = off
	TPBps          float64 `json:"tp_bps"`           // 0 = off
	ExitTime       string  `json:"exit_time"`        // HH:MM[:SS], blank = none
	StraddleDiv    float64 `json:"straddle_div"`     // default 4
	HedgeDiv       float64 `json:"hedge_div"`        // default 57
	HedgeMinBps    float64 `json:"hedge_min_bps"`    // default 8
	MaxDepthAgeMs  int64   `json:"max_depth_age_ms"` // default 1500
}

// SBLive is the real-time straddle check for a rule (always computed, even
// when no build is running).
type SBLive struct {
	Symbol        string  `json:"symbol"`
	Expiry        string  `json:"expiry"`
	Spot          float64 `json:"spot"`
	ATM           float64 `json:"atm"`
	CELtp         float64 `json:"ce_ltp"`
	PELtp         float64 `json:"pe_ltp"`
	CEBid         float64 `json:"ce_bid"`
	CEAsk         float64 `json:"ce_ask"`
	PEBid         float64 `json:"pe_bid"`
	PEAsk         float64 `json:"pe_ask"`
	LTPStraddle   float64 `json:"ltp_straddle"`
	BidStraddle   float64 `json:"bid_straddle"` // what a SELL gets at L1
	Target        float64 `json:"target"`
	AboveTarget   bool    `json:"above_target"`
	Gap           float64 `json:"gap"` // bid straddle - target
	CEDepthLevels int     `json:"ce_depth_levels"`
	PEDepthLevels int     `json:"pe_depth_levels"`
	CEDepthAgeMs  int64   `json:"ce_depth_age_ms"`
	PEDepthAgeMs  int64   `json:"pe_depth_age_ms"`
	// Depth check (L1-L5) for the rule's quantity.
	CEBids        []DepthLevel    `json:"ce_bids,omitempty"`
	CEAsks        []DepthLevel    `json:"ce_asks,omitempty"`
	PEBids        []DepthLevel    `json:"pe_bids,omitempty"`
	PEAsks        []DepthLevel    `json:"pe_asks,omitempty"`
	LotSize       int64           `json:"lot_size"`
	VWAPOneLot    float64         `json:"vwap_one_lot"`      // sell 1 lot each leg
	FullQty       int64           `json:"full_qty"`          // straddle qty per leg
	VWAPFull      float64         `json:"vwap_full"`         // sell the full qty each leg (0 = book too thin)
	FullFillable  bool            `json:"full_fillable"`     // book holds the full qty on both legs
	Participation float64         `json:"participation"`     // effective share of the book for this rule
	SharedWith    int             `json:"shared_with"`       // rules building this book (incl. this one if started)
	Preview       *DepthEntryPlan `json:"preview,omitempty"` // next tranche decision right now
	Updated       string          `json:"updated"`
	Error         string          `json:"error,omitempty"`
}

// SBEvent is one line of a run's audit trail.
type SBEvent struct {
	Time string `json:"time"`
	Kind string `json:"kind"` // PLAN / TRANCHE / FILL / HEDGE / RISK / EXIT / RULE / ATM / RESUME / INFO
	Text string `json:"text"`
}

// SBState is what the tab shows for one rule.
type SBState struct {
	RuleID    string            `json:"rule_id"`
	Mode      string            `json:"mode"` // SHADOW / LIVE
	TradeUID  string            `json:"trade_uid,omitempty"`
	Busy      bool              `json:"busy"`
	Pending   []sbPendingOrder  `json:"pending,omitempty"`
	Halt      string            `json:"halt,omitempty"`
	LiveCap   int64             `json:"live_max_lots"`
	Phase     string            `json:"phase"`
	Config    SBConfig          `json:"config"`
	Date      string            `json:"date,omitempty"`
	StartedAt string            `json:"started_at,omitempty"`
	ResumedAt string            `json:"resumed_at,omitempty"`
	Now       string            `json:"now"`
	TargetQty int64             `json:"target_qty"`
	Remaining int64             `json:"remaining"`
	LotSize   int               `json:"lot_size"`
	Expiry    string            `json:"expiry"`
	ATM       float64           `json:"atm"`
	Spot      float64           `json:"spot"`
	Strikes   []float64         `json:"strikes"` // strikes the position was built at (ATM moves)
	Tranches  int               `json:"tranches"`
	LastPlan  *DepthEntryPlan   `json:"last_plan,omitempty"`
	LastAuth  *DepthEntryPlan   `json:"last_authorized,omitempty"`
	Position  PMSView           `json:"position"`
	Risk      map[string]string `json:"risk"`
	Events    []SBEvent         `json:"events"`
	Fills     []PMSFill         `json:"fills"`
	Live      SBLive            `json:"live"`
	Error     string            `json:"error,omitempty"`
}

type sbRunner struct {
	mu          sync.Mutex
	cfg         SBConfig
	phase       string // IDLE / BUILDING / COMPLETE / EXITED / STOPPED
	date        string // IST day the run started (YYYY-MM-DD)
	startedAt   time.Time
	resumedAt   time.Time
	pms         *PMS
	tranches    int
	lastTranche time.Time
	lastMinute  int
	lastReason  string
	lastPlan    *DepthEntryPlan
	lastAuth    *DepthEntryPlan
	risk        map[string]string
	events      []SBEvent
	lotSize     int
	expiry      string
	atm, spot   float64
	buildATM    float64 // strike of the last tranche (ATM-move detection)
	strikes     []float64
	errText     string
	live        SBLive
	chain       *OptionChainSnapshot // last chain (marks / greeks for the view)
	previewLot  int64
	previewKey  string
	reasonAt    time.Time
	dirty       bool
	// LIVE mode (real orders) -- see sb_live.go.
	isLive     bool
	tradeUID   string
	busy       bool             // orders in flight (background); the cycle waits
	pending    []sbPendingOrder // sent, outcome not yet confirmed (write-ahead)
	haltReason string
	// handoffNoted: the "not handed off" reason was logged once.
	handoffNoted bool
	followAt     time.Time // last check of the handed-off trade's status
}

func newSBRunner(c SBConfig) *sbRunner {
	return &sbRunner{cfg: c, phase: "IDLE", pms: NewPMS(), risk: map[string]string{}}
}

// PAUSED: the OMS (new entry orders) is paused; PMS keeps monitoring.
func (r *sbRunner) active() bool {
	return r.phase == "BUILDING" || r.phase == "COMPLETE" || r.phase == "PAUSED"
}

// holding: a run whose position may be open (blocks restart / delete).
func (r *sbRunner) holding() bool {
	switch r.phase {
	case "BUILDING", "COMPLETE", "PAUSED", "EXITING", "HALTED":
		return true
	}
	return r.isLive && !r.pms.View(nil).Flat && r.phase != "EXITED" && r.phase != sbPhaseHandedOff
}

// sbEngine holds every rule's runner, in display order.
type sbEngine struct {
	mu      sync.Mutex
	runners map[string]*sbRunner
	order   []string
}

var sbEng = &sbEngine{runners: map[string]*sbRunner{}}

func (e *sbEngine) get(id string) *sbRunner {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runners[id]
}

func (e *sbEngine) list() []*sbRunner {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*sbRunner, 0, len(e.order))
	for _, id := range e.order {
		out = append(out, e.runners[id])
	}
	return out
}

func (e *sbEngine) add(r *sbRunner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.runners[r.cfg.ID]; !ok {
		e.order = append(e.order, r.cfg.ID)
	}
	e.runners[r.cfg.ID] = r
}

func (e *sbEngine) remove(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.runners, id)
	for i, x := range e.order {
		if x == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
}

// configs returns every rule (for saving the rules file).
func (e *sbEngine) configs() []SBConfig {
	var out []SBConfig
	for _, r := range e.list() {
		r.mu.Lock()
		out = append(out, r.cfg)
		r.mu.Unlock()
	}
	return out
}

func (r *sbRunner) event(kind, format string, a ...interface{}) {
	e := SBEvent{Time: time.Now().In(lutIST()).Format("15:04:05.000"), Kind: kind, Text: fmt.Sprintf(format, a...)}
	r.events = append(r.events, e)
	if len(r.events) > 400 {
		r.events = r.events[len(r.events)-400:]
	}
	r.dirty = true
	tag := "SBUILD-SHADOW"
	if r.isLive {
		tag = "SBUILD-LIVE"
	}
	log.Printf("[%s] rule=%s %s %s", tag, r.cfg.ID, kind, e.Text)
}

func sbDefaults(c SBConfig) SBConfig {
	c.Symbol = NormalizeSymbol(c.Symbol)
	if c.Symbol == "" {
		c.Symbol = "NIFTY"
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Expiry = strings.TrimSpace(c.Expiry)
	c.ExitTime = strings.TrimSpace(c.ExitTime)
	if c.Participation <= 0 || c.Participation > 1 {
		c.Participation = 0.5
	}
	if c.MaxLevels <= 0 || c.MaxLevels > 5 {
		c.MaxLevels = 5
	}
	if c.TrancheGapMs <= 0 {
		c.TrancheGapMs = 1000
	}
	if c.StraddleDiv <= 0 {
		c.StraddleDiv = 4
	}
	if c.HedgeDiv <= 0 {
		c.HedgeDiv = 57
	}
	if c.HedgeMinBps <= 0 {
		c.HedgeMinBps = defaultHedgeMinThresholdBps
	}
	if c.MaxDepthAgeMs <= 0 {
		c.MaxDepthAgeMs = 1500
	}
	return c
}

func sbToday() string { return time.Now().In(lutIST()).Format("2006-01-02") }

// StartStraddleBuild starts a new run for one rule (replacing that rule's
// idle / finished run). live=true sends REAL orders and needs the typed
// confirmation sbLiveConfirm.
func (s *Service) StartStraddleBuild(id string, live bool, confirm string) error {
	r := sbEng.get(id)
	if r == nil {
		return fmt.Errorf("unknown rule %q", id)
	}
	r.mu.Lock()
	c := r.cfg
	r.mu.Unlock()
	if c.TargetStraddle <= 0 || c.Straddles <= 0 {
		return fmt.Errorf("target straddle and straddle quantity are required")
	}
	lot, err := s.sbLotSize(c.Symbol, c.Expiry)
	if err != nil {
		return err
	}
	if c.Straddles = sbRoundToLot(c.Straddles, lot); c.Straddles < lot {
		return fmt.Errorf("straddle quantity must be at least one lot (%d)", lot)
	}
	var liveTrade StoredTrade
	if live {
		if strings.TrimSpace(confirm) != sbLiveConfirm {
			return fmt.Errorf("LIVE needs the confirmation text %q", sbLiveConfirm)
		}
		if maxLots := sbLiveMaxLots(); c.Straddles/lot > maxLots {
			return fmt.Errorf("LIVE size %d lots per leg is above the cap %d (SBUILD_LIVE_MAX_LOTS)", c.Straddles/lot, maxLots)
		}
		if !sbLiveWindow(time.Now().In(lutIST())) {
			return fmt.Errorf("LIVE can only start in the broker session (09:15-15:40)")
		}
		if s.OrderEvents == nil || !s.OrderEvents.Healthy() {
			return fmt.Errorf("LIVE refused: order confirmations (reconciler -> order events) are not healthy")
		}
		r.mu.Lock()
		busy := r.holding()
		r.mu.Unlock()
		if busy {
			return fmt.Errorf("rule %s still holds a run/position; exit or stop it first", id)
		}
		exp := c.Expiry
		if exp == "" {
			if ch, err := s.Snapshot.GetOptionChain(context.Background(), c.Symbol, ""); err == nil {
				exp = ch.Expiry
			}
		}
		t, err := s.sbLiveCreateTrade(c, exp, lot)
		if err != nil {
			return fmt.Errorf("LIVE refused: %w", err)
		}
		liveTrade = t
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.holding() {
		return fmt.Errorf("rule %s is already running / holds a position; stop or exit it first", id)
	}
	// Reset field by field: the mutex itself must not be overwritten while held.
	r.cfg, r.phase, r.date, r.startedAt, r.resumedAt, r.pms = c, "BUILDING", sbToday(), time.Now(), time.Time{}, NewPMS()
	r.tranches, r.lastTranche, r.lastMinute, r.lastReason = 0, time.Time{}, 0, ""
	r.lastPlan, r.lastAuth, r.risk, r.events = nil, nil, map[string]string{}, nil
	r.lotSize, r.expiry, r.atm, r.spot, r.buildATM, r.strikes, r.errText = int(lot), "", 0, 0, 0, nil, ""
	r.isLive, r.tradeUID, r.busy, r.pending, r.haltReason = live, liveTrade.TradeUID, false, nil, ""
	r.handoffNoted = false
	if live {
		r.event("LIVE", "LIVE run started -- REAL ORDERS on account %s, trade %s: %s target straddle %.2f (sold straddle avg CE + avg PE always STRICTLY above it; IOC limit per lot), straddle qty %d (%d lots) = %d contracts, exit %s",
			liveTrade.AccountID, liveTrade.TradeUID, c.Symbol, c.TargetStraddle, c.Straddles, c.Straddles/lot, 2*c.Straddles, c.ExitTime)
		r.persistLocked()
		return nil
	}
	r.event("INFO", "SHADOW run started: %s target straddle %.2f, straddle qty %d (%d lots) = %d contracts total, %.0f%% participation, L1-L%d, SL %.0f bps, TP %.0f bps, exit %s -- NO ORDERS",
		c.Symbol, c.TargetStraddle, c.Straddles, c.Straddles/lot, 2*c.Straddles, c.Participation*100, c.MaxLevels, c.SLBps, c.TPBps, c.ExitTime)
	r.persistLocked()
	return nil
}

// StopStraddleBuild stops one rule's run. SHADOW: position kept as is,
// monitors stop. LIVE: building stops but the position stays monitored
// (hedge / SL / TP / exit time) -- use Exit now to flatten. On a HALTED
// live run, Stop acknowledges the halt (nothing is sent).
func (s *Service) StopStraddleBuild(id string) error {
	r := sbEng.get(id)
	if r == nil {
		return fmt.Errorf("unknown rule %q", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isLive {
		switch r.phase {
		case "BUILDING", "PAUSED":
			r.phase = "COMPLETE"
			r.event("INFO", "LIVE build stopped by user -- no more entry orders; the position stays monitored (hedge / SL / TP / exit time). Use Exit now to flatten.")
			r.persistLocked()
			return nil
		case "COMPLETE", "EXITING":
			return fmt.Errorf("LIVE position is open and monitored -- use Exit now to flatten it")
		case "HALTED":
			r.phase = "STOPPED"
			r.event("INFO", "HALT acknowledged by user (%s) -- run stopped; nothing was sent. Reconcile trade %s with the broker.", r.haltReason, r.tradeUID)
			s.sbLiveCloseTrade(r.tradeUID, "RECONCILIATION_REQUIRED_SB")
			r.persistLocked()
			return nil
		}
		return nil
	}
	if r.active() {
		r.phase = "STOPPED"
		r.event("INFO", "stopped by user")
		r.persistLocked()
	}
	return nil
}

// PauseStraddleBuild pauses (pause=true) or resumes one rule's OMS: while
// PAUSED no entry order is sent (no tranche, no completion lot); the PMS
// keeps monitoring what is built (hedge / SL / TP / exit time, every
// minute). Orders already in flight finish and are booked. Resume continues
// the build from the PMS position.
func (s *Service) PauseStraddleBuild(id string, pause bool) error {
	r := sbEng.get(id)
	if r == nil {
		return fmt.Errorf("unknown rule %q", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if pause {
		if r.phase != "BUILDING" {
			return fmt.Errorf("only a BUILDING run can be paused (phase %s)", r.phase)
		}
		r.phase = "PAUSED"
		v := r.pms.View(r.chain)
		r.event("INFO", "OMS PAUSED by user -- no new entry orders. Built so far CE %d + PE %d of %d (partial); PMS keeps monitoring it (hedge / SL / TP / exit time).%s",
			v.BuildCE, v.BuildPE, 2*r.cfg.Straddles, map[bool]string{true: " Orders in flight finish first.", false: ""}[r.busy])
		r.persistLocked()
		return nil
	}
	if r.phase != "PAUSED" {
		return fmt.Errorf("run is not paused (phase %s)", r.phase)
	}
	if r.isLive && !sbLiveWindow(time.Now().In(lutIST())) {
		return fmt.Errorf("outside the broker session (09:15-15:40)")
	}
	r.phase = "BUILDING"
	r.lastTranche = time.Now()
	r.event("INFO", "OMS RESUMED by user -- building continues from the PMS position")
	r.persistLocked()
	return nil
}

// ExitStraddleBuild flattens a run now (Exit now button). LIVE: MARKET
// orders, each confirmed. Refused while orders are in flight.
func (s *Service) ExitStraddleBuild(id string, confirm string) error {
	r := sbEng.get(id)
	if r == nil {
		return fmt.Errorf("unknown rule %q", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy {
		return fmt.Errorf("orders are in flight -- try again in a moment")
	}
	if r.phase == sbPhaseHandedOff || r.phase == "EXITED" {
		// The position belongs to the standard trade now: this run's PMS no
		// longer tracks it, so exiting from here could buy back twice.
		return fmt.Errorf("this build's position was handed to trade %s -- exit it from its Portfolio row (Full Exit)", r.tradeUID)
	}
	if r.isLive {
		if strings.TrimSpace(confirm) != "EXIT" {
			return fmt.Errorf("LIVE exit needs the confirmation text \"EXIT\"")
		}
		// Unconfirmed orders are settled from the broker's order book
		// first, so the exit flattens the REAL position (2026-10-06: an exit
		// left a lot open whose fill confirmation had been lost).
		if ok, why := s.sbResolvePendingLocked(r); !ok {
			return fmt.Errorf("unconfirmed order(s) could not be checked against the broker order book (%s) -- check the broker positions before exiting", why)
		}
		if !sbLiveWindow(time.Now().In(lutIST())) {
			return fmt.Errorf("outside the broker session (09:15-15:40)")
		}
	}
	if !r.holding() && r.pms.View(nil).Flat {
		return fmt.Errorf("nothing to exit (phase %s, position flat)", r.phase)
	}
	s.sbExitLocked(r, r.chain, "Exit now by user")
	return nil
}

func (s *Service) sbLoop() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	n := 0
	for range t.C {
		n++
		s.sbCycle(n%3 == 0)
	}
}

type sbChainRes struct {
	chain *OptionChainSnapshot
	err   error
}

// sbCycle runs one cycle for every rule. Chains are fetched once per
// symbol/expiry and shared by the rules on it.
func (s *Service) sbCycle(push bool) {
	if s.Snapshot == nil {
		return
	}
	runners := sbEng.list()
	cfgs := make([]SBConfig, len(runners))
	var keys []string
	seen := map[string]bool{}
	for i, r := range runners {
		r.mu.Lock()
		cfgs[i] = r.cfg
		r.mu.Unlock()
		k := cfgs[i].Symbol + "|" + cfgs[i].Expiry
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	results := make(map[string]sbChainRes, len(keys))
	var wg sync.WaitGroup
	var rmu sync.Mutex
	for _, k := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			sym, exp, _ := strings.Cut(k, "|")
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			c, err := s.Snapshot.GetOptionChain(ctx, sym, exp)
			cancel()
			if err == nil && c == nil {
				err = fmt.Errorf("empty chain")
			}
			rmu.Lock()
			results[k] = sbChainRes{c, err}
			rmu.Unlock()
		}(k)
	}
	wg.Wait()

	// Rules building the same book share its participation.
	book := func(i int) string {
		res := results[cfgs[i].Symbol+"|"+cfgs[i].Expiry]
		exp := cfgs[i].Expiry
		if res.chain != nil {
			exp = res.chain.Expiry
		}
		return cfgs[i].Symbol + "|" + exp
	}
	building := map[string]int{}
	for i, r := range runners {
		r.mu.Lock()
		if r.phase == "BUILDING" {
			building[book(i)]++
		}
		r.mu.Unlock()
	}

	states := make([]SBState, 0, len(runners))
	for i, r := range runners {
		res := results[cfgs[i].Symbol+"|"+cfgs[i].Expiry]
		r.mu.Lock()
		share := building[book(i)]
		if r.phase != "BUILDING" {
			share++ // what it would get if started now
		}
		s.sbCycleOneLocked(r, res.chain, res.err, share)
		if r.dirty {
			r.persistLocked()
		}
		if push {
			states = append(states, r.stateLocked(true))
		}
		r.mu.Unlock()
	}
	if push {
		if p, ok := s.Snapshot.(interface {
			PushEvent(ctx context.Context, eventType string, data interface{}) error
		}); ok {
			pctx, pcancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = p.PushEvent(pctx, "sbuild_update", map[string]interface{}{"runs": states})
			pcancel()
		}
	}
}

func (s *Service) sbCycleOneLocked(r *sbRunner, chain *OptionChainSnapshot, err error, share int) {
	cfg := r.cfg
	if share < 1 {
		share = 1
	}
	part := cfg.Participation / float64(share)
	if err != nil {
		r.live = SBLive{Symbol: cfg.Symbol, Error: "chain: " + err.Error(), Updated: time.Now().In(lutIST()).Format("15:04:05.0")}
	} else {
		r.chain = chain
		lot := int64(r.lotSize)
		key := cfg.Symbol + "|" + chain.Expiry
		if lot <= 0 {
			if r.previewKey != key || r.previewLot <= 0 {
				if l, lerr := s.sbLotSize(cfg.Symbol, chain.Expiry); lerr == nil {
					r.previewLot, r.previewKey = l, key
				}
			}
			lot = r.previewLot
		}
		remaining, posDelta := 2*cfg.Straddles, 0.0
		if r.active() {
			v := r.pms.View(chain)
			remaining, posDelta = 2*cfg.Straddles-(v.BuildCE+v.BuildPE), v.NetDelta
		}
		r.live = sbComputeLive(cfg, chain, lot, remaining, posDelta, part)
		r.live.SharedWith = share
	}
	if r.phase == sbPhaseHandedOff {
		s.sbFollowHandedOffLocked(r)
	}
	if !r.active() || r.busy {
		return // live orders in flight: risk and tranches wait for them
	}
	if err != nil {
		r.errText = "chain: " + err.Error()
		return
	}
	r.errText = ""
	r.expiry = chain.Expiry
	r.spot = lutUnderlying(chain)
	if r.lotSize <= 0 && s.LotSize != nil {
		lctx, lcancel := context.WithTimeout(context.Background(), 2*time.Second)
		if ls, lerr := s.LotSize.GetLotSize(lctx, cfg.Symbol, chain.Expiry); lerr == nil && ls > 0 {
			r.lotSize = ls
		}
		lcancel()
	}
	if r.lotSize <= 0 {
		r.errText = "lot size not available yet"
		return
	}
	now := time.Now().In(lutIST())
	hhmm := now.Hour()*100 + now.Minute()

	// LIVE: no order (tranche, completion, hedge, exit) until the order
	// confirmations are flowing and the gateway has been up a moment -- on
	// 2026-10-06 a resumed run sent a lot 1s before the reconciler's feed
	// reconnected and its fill was never confirmed.
	if r.isLive {
		if s.OrderEvents == nil || !s.OrderEvents.Healthy() {
			r.sbReason("LIVE: waiting for order confirmations (reconciler feed) before sending anything")
			return
		}
		if time.Since(sbGatewayUp) < sbLiveWarmup || (!r.resumedAt.IsZero() && time.Since(r.resumedAt) < sbLiveWarmup) {
			return
		}
	}

	// 1. Minute-end risk on the whole PMS position (runs while building
	// too, and on the first tick after a restart).
	if hhmm != r.lastMinute {
		r.lastMinute = hhmm
		if s.sbRiskLocked(r, chain, now) {
			return // exited
		}
	}

	// Built (complete, or stopped part-way): the position becomes a normal
	// trade run by the standard monitor.
	if r.phase == "COMPLETE" && s.sbHandOffLocked(r) {
		return
	}

	// 2. Next tranche, always at the CURRENT ATM.
	if r.phase != "BUILDING" || time.Since(r.lastTranche) < time.Duration(cfg.TrancheGapMs)*time.Millisecond {
		return
	}
	view := r.pms.View(chain)
	remaining := 2*cfg.Straddles - (view.BuildCE + view.BuildPE)
	lot := int64(r.lotSize)
	if remaining < lot {
		r.phase = "COMPLETE"
		r.event("INFO", "BUILD COMPLETE: CE %d + PE %d = %d of %d across strikes %s; net delta %+.2f",
			view.BuildCE, view.BuildPE, view.BuildCE+view.BuildPE, 2*cfg.Straddles, sbStrikesText(r.strikes), view.NetDelta)
		return
	}
	atm, aerr := FindATMRow(*chain)
	if aerr != nil || atm == nil {
		r.sbReason("no ATM row in chain")
		return
	}
	r.atm = atm.Strike
	if atm.CEDepth == nil || atm.PEDepth == nil {
		r.sbReason(fmt.Sprintf("no L1-L5 depth for ATM %.0f yet", atm.Strike))
		return
	}
	if atm.CEDepth.AgeMs > cfg.MaxDepthAgeMs || atm.PEDepth.AgeMs > cfg.MaxDepthAgeMs {
		r.sbReason(fmt.Sprintf("depth stale (CE %dms / PE %dms)", atm.CEDepth.AgeMs, atm.PEDepth.AgeMs))
		return
	}
	// A lopsided build (one leg ahead, carrying delta) is completed first,
	// at the best bid, whether or not the market is above target.
	if leg, ok := sbCompletionLeg(view, remaining, lot, atm.CEDelta, atm.PEDelta); ok {
		if r.isLive && !sbLiveWindow(now) {
			r.sbReason("LIVE: outside the broker session (09:15-15:40) -- no orders")
			return
		}
		r.buildATM = atm.Strike
		s.sbCompleteLocked(r, leg, *atm, view)
		return
	}
	plan := PlanDepthEntry(DepthEntryInput{
		CEBids: atm.CEDepth.Bids, PEBids: atm.PEDepth.Bids,
		CEDelta: atm.CEDelta, PEDelta: atm.PEDelta,
		TargetStraddle: cfg.TargetStraddle, LotSize: lot,
		Participation: part, MaxLevels: cfg.MaxLevels,
		RemainingQty: remaining, PositionDelta: view.NetDelta,
	})
	r.lastPlan = &plan
	if !plan.Authorized {
		r.sbReason(fmt.Sprintf("ATM %.0f: %s", atm.Strike, plan.Reason))
		return
	}
	if r.buildATM != 0 && r.buildATM != atm.Strike {
		r.event("ATM", "ATM moved %.0f -> %.0f: building now at %.0f; the whole position counts together (CE %d + PE %d sold across %s, net delta %+.2f)",
			r.buildATM, atm.Strike, atm.Strike, view.BuildCE, view.BuildPE, sbStrikesText(r.strikes), view.NetDelta)
	}
	r.buildATM = atm.Strike
	r.addStrike(atm.Strike)
	r.lastAuth = &plan
	r.lastReason = ""
	r.tranches++
	r.lastTranche = time.Now()
	if r.isLive && !sbLiveWindow(now) {
		r.sbReason("LIVE: outside the broker session (09:15-15:40) -- no orders")
		return
	}
	if share > 1 {
		r.event("PLAN", "tranche %d at ATM %.0f (remaining %d, participation %.0f%% shared by %d rules):\n%s", r.tranches, atm.Strike, remaining, part*100, share, plan.String())
	} else {
		r.event("PLAN", "tranche %d at ATM %.0f (remaining %d):\n%s", r.tranches, atm.Strike, remaining, plan.String())
	}
	if r.isLive {
		s.sbExecuteLiveAsync(r, plan, *atm)
		return
	}
	s.sbExecuteShadowLocked(r, plan, atm)
}

func (r *sbRunner) addStrike(k float64) {
	for _, x := range r.strikes {
		if x == k {
			return
		}
	}
	r.strikes = append(r.strikes, k)
}

func sbStrikesText(ks []float64) string {
	if len(ks) == 0 {
		return "-"
	}
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = fmt.Sprintf("%.0f", k)
	}
	return strings.Join(parts, ", ")
}

// sbReason logs a "waiting" reason only when its kind changes (numbers
// ignored) or at most every 30 s -- not on every price tick.
func (r *sbRunner) sbReason(reason string) {
	kind := strings.Map(func(c rune) rune {
		if (c >= '0' && c <= '9') || c == '.' {
			return -1
		}
		return c
	}, reason)
	if kind != r.lastReason || time.Since(r.reasonAt) >= 30*time.Second {
		r.lastReason, r.reasonAt = kind, time.Now()
		r.event("INFO", "waiting: %s", reason)
	}
}

// sbExecuteShadowLocked: OMS (simulated) -- one lot per order, CE/PE
// interleaved; each lot "fills" at the next level of the snapshot book it
// was planned on, never below the leg's limit floor; each verified fill is
// pushed to PMS immediately.
func (s *Service) sbExecuteShadowLocked(r *sbRunner, plan DepthEntryPlan, atm *OptionChainRow) {
	lot := int64(r.lotSize)
	seq := TrancheLotSequence(int(plan.CEQty/lot), int(plan.PEQty/lot))
	type cursor struct {
		levels []DepthLevel
		i      int
		used   int64
		floor  float64
	}
	cur := map[string]*cursor{
		"CE": {levels: atm.CEDepth.Bids, floor: plan.CEWorst},
		"PE": {levels: atm.PEDepth.Bids, floor: plan.PEWorst},
	}
	filled := map[string]int64{}
	bestBid := map[string]float64{"CE": atm.CEDepth.Bids[0].Price, "PE": atm.PEDepth.Bids[0].Price}
	for _, leg := range seq {
		c := cur[leg]
		other := "PE"
		if leg == "PE" {
			other = "CE"
		}
		// Same strict rule as LIVE: this lot may not pull avg CE + avg PE
		// to or below the target.
		c.floor = r.sbNextLotLimit(leg, plan, bestBid[other])
		remaining, value := lot, 0.0
		for remaining > 0 && c.i < len(c.levels) {
			l := c.levels[c.i]
			avail := l.Qty - c.used
			if avail <= 0 {
				c.i, c.used = c.i+1, 0
				continue
			}
			if l.Price < c.floor {
				break
			}
			take := avail
			if take > remaining {
				take = remaining
			}
			value += float64(take) * l.Price
			remaining -= take
			c.used += take
		}
		if remaining > 0 {
			r.event("TRANCHE", "tranche %d: %s lot not fillable at/above floor %.2f -- tranche stopped (both legs), re-plan from PMS", r.tranches, leg, c.floor)
			break
		}
		token := atm.CEToken
		if leg == "PE" {
			token = atm.PEToken
		}
		f := PMSFill{Token: token, Strike: atm.Strike, OptionType: leg, Side: "SELL", Qty: lot, Price: value / float64(lot), Role: "BUILD", Tranche: r.tranches}
		r.pms.ApplyFill(f) // verified fill -> PMS immediately
		r.dirty = true
		filled[leg] += lot
	}
	r.event("TRANCHE", "tranche %d done: sold CE %d + PE %d at ATM %.0f (planned %d + %d); sequence %s",
		r.tranches, filled["CE"], filled["PE"], atm.Strike, plan.CEQty, plan.PEQty, strings.Join(seq, " "))
}

// sbRiskLocked runs the minute-end checks on the whole PMS position (every
// strike). Returns true if it exited.
func (s *Service) sbRiskLocked(r *sbRunner, chain *OptionChainSnapshot, now time.Time) bool {
	cfg := r.cfg
	view := r.pms.View(chain)
	timeHit, timeAt := false, ""
	if cfg.ExitTime != "" {
		if at, err := ParseClockTodayIST(cfg.ExitTime, now); err == nil {
			timeAt, timeHit = at.Format("15:04:05"), !now.Before(at)
		}
	}
	if view.Flat {
		r.risk = map[string]string{"state": "no position yet"}
		if timeHit && (r.phase == "BUILDING" || r.phase == "PAUSED") {
			r.phase = "EXITED"
			r.event("EXIT", "exit time %s reached with no position -- build stopped", timeAt)
			if r.isLive {
				s.sbLiveCloseTrade(r.tradeUID, sbStatusClosed)
			}
			return true
		}
		return false
	}
	spot := r.spot
	risk := map[string]string{}
	exit := ""

	// SL / TP per FILLED straddle (not the target size).
	if cfg.SLBps > 0 {
		th := -spot * cfg.SLBps / 10000
		risk["SL"] = fmt.Sprintf("%.2f vs %.2f per filled straddle (%.1f filled)", view.PnLPerStraddle, th, view.FilledStraddles)
		if view.PnLPerStraddle <= th {
			exit = "SL"
		}
	}
	if cfg.TPBps > 0 && exit == "" {
		th := spot * cfg.TPBps / 10000
		risk["TP"] = fmt.Sprintf("%.2f vs %.2f per filled straddle", view.PnLPerStraddle, th)
		if view.PnLPerStraddle >= th {
			exit = "TP"
		}
	}
	if timeAt != "" {
		risk["TIME"] = timeAt
		if timeHit && exit == "" {
			exit = "TIME"
		}
	}
	if view.MissingMarks > 0 {
		risk["MARKS"] = fmt.Sprintf("%d leg(s) outside the chain window -- using last known mark/greeks", view.MissingMarks)
	}

	// Hedge: the same minute-end rule as live trades, capped by FILLED lots,
	// on the whole position's net delta, placed at the current ATM.
	lot := int64(r.lotSize)
	// Standard once-a-minute monitor lines (same format as every other
	// trade's monitor, so scripts/watch_logs.sh shows HEDGE/SL/TP/TIME rows).
	monUID := r.tradeUID
	if monUID == "" {
		monUID = "SB-SHADOW-" + cfg.ID
	}
	minute := now.Format("15:04")
	hedgeLogged := false
	if exit == "" {
		if atm, err := FindATMRow(*chain); err == nil && atm != nil {
			straddle := atm.CELtp + atm.PELtp
			iv := (atm.CEIV + atm.PEIV) / 2 / 100
			allowed := math.Min(straddle/cfg.StraddleDiv, spot*iv/cfg.HedgeDiv)
			out := 0.0
			if view.NetGamma != 0 {
				out = math.Abs(view.NetDelta / view.NetGamma)
			}
			d := decideHedge(hedgeDecisionInput{
				PointsOut: out, PointsAllowed: allowed, Spot: spot, NetDelta: view.NetDelta,
				LotSize: lot, TradeLots: int64(view.FilledStraddles) / lot, MinThresholdBps: cfg.HedgeMinBps,
			})
			risk["HEDGE"] = fmt.Sprintf("%s: out %.2f / allowed %.2f (floor %.2f), net delta %+.2f", d.Action, out, d.EffectiveAllowed, d.Floor, view.NetDelta)
			log.Printf("[MONITOR][%s] minute=%s action=%s points_out=%.4f points_allowed=%.4f min_points=%.4f net_delta=%.4f abs_delta=%.4f lot_size=%d syn_fut=%.2f syn_spot=%.2f",
				monUID, minute, d.Action, out, d.EffectiveAllowed, d.Floor, view.NetDelta, math.Abs(view.NetDelta), lot, spot, spot)
			hedgeLogged = true
			if d.Hedge && d.Lots > 0 {
				ceSide, peSide, ok := hedgeSidesFromSignedDelta(view.NetDelta)
				if ok {
					q := d.Lots * lot
					cePx, pePx := atm.CEAsk, atm.PEBid
					if ceSide == "SELL" {
						cePx, pePx = atm.CEBid, atm.PEAsk
					}
					if cePx <= 0 {
						cePx = atm.CELtp
					}
					if pePx <= 0 {
						pePx = atm.PELtp
					}
					if r.isLive {
						before := view.NetDelta
						r.event("HEDGE", "LIVE %d lot(s) %s CE / %s PE @%.0f with MARKET orders (net delta %+.2f)", d.Lots, ceSide, peSide, atm.Strike, before)
						s.sbLiveMarketAsync(r, "HEDGE", []sbMarketOrder{
							{Token: atm.CEToken, Strike: atm.Strike, OptionType: "CE", Side: ceSide, Qty: q},
							{Token: atm.PEToken, Strike: atm.Strike, OptionType: "PE", Side: peSide, Qty: q},
						}, func(ok bool) {
							if ok {
								r.event("HEDGE", "LIVE hedge confirmed: net delta %+.2f -> %+.2f", before, r.pms.View(r.chain).NetDelta)
							}
						})
					} else {
						r.pms.ApplyFill(PMSFill{Token: atm.CEToken, Strike: atm.Strike, OptionType: "CE", Side: ceSide, Qty: q, Price: cePx, Role: "HEDGE"})
						r.pms.ApplyFill(PMSFill{Token: atm.PEToken, Strike: atm.Strike, OptionType: "PE", Side: peSide, Qty: q, Price: pePx, Role: "HEDGE"})
						r.dirty = true
						after := r.pms.View(chain)
						r.event("HEDGE", "%d lot(s) %s CE / %s PE @%.0f (CE %.2f, PE %.2f): net delta %+.2f -> %+.2f",
							d.Lots, ceSide, peSide, atm.Strike, cePx, pePx, view.NetDelta, after.NetDelta)
					}
				}
			}
		}
	}
	if !hedgeLogged {
		log.Printf("[MONITOR][%s] minute=%s action=%s points_out=0 points_allowed=0 min_points=0 net_delta=%.4f abs_delta=%.4f lot_size=%d syn_fut=%.2f syn_spot=%.2f",
			monUID, minute, map[bool]string{true: exit + "_EXIT", false: "NO_ATM"}[exit != ""], view.NetDelta, math.Abs(view.NetDelta), lot, spot, spot)
	}
	if cfg.SLBps > 0 {
		th := -spot * cfg.SLBps / 10000
		log.Printf("[MONITOR][%s] minute=%s check=SL status=%s source=\"bps_of_spot spot=%.2f bps=%.2f\" pnl=%.2f pnl_per_straddle=%.2f threshold=%.2f",
			monUID, minute, map[bool]string{true: "BREACHED", false: "OK"}[exit == "SL"], spot, cfg.SLBps, view.PnL, view.PnLPerStraddle, th)
	}
	if cfg.TPBps > 0 {
		log.Printf("[MONITOR][%s] minute=%s check=TP status=%s pnl_per_straddle=%.2f threshold=%.2f bps=%.2f",
			monUID, minute, map[bool]string{true: "BREACHED", false: "OK"}[exit == "TP"], view.PnLPerStraddle, spot*cfg.TPBps/10000, cfg.TPBps)
	}
	if timeAt != "" {
		remaining := "0s"
		if at, err := ParseClockTodayIST(cfg.ExitTime, now); err == nil && at.After(now) {
			remaining = at.Sub(now).Truncate(time.Second).String()
		}
		log.Printf("[MONITOR][%s] minute=%s check=TIME status=%s target=%s remaining=%s",
			monUID, minute, map[bool]string{true: "BREACHED", false: "OK"}[timeHit], timeAt, remaining)
	}
	risk["PnL"] = fmt.Sprintf("₹%.0f (%.2f per filled straddle)", view.PnL, view.PnLPerStraddle)
	r.risk = risk
	r.event("RISK", "minute %s: %s | %s | %s | %s", now.Format("15:04"), risk["HEDGE"], risk["SL"], risk["TP"], risk["TIME"])

	if exit != "" {
		if r.busy { // a live hedge was just sent; the exit runs next minute check
			return false
		}
		s.sbExitLocked(r, chain, exit+" triggered")
		return true
	}
	return false
}

// stateLocked builds the tab view. trim keeps the push payload small.
func (r *sbRunner) stateLocked(trim bool) SBState {
	events := r.events
	if trim && len(events) > 150 {
		events = events[len(events)-150:]
	}
	st := SBState{
		RuleID: r.cfg.ID, Mode: map[bool]string{false: "SHADOW", true: "LIVE"}[r.isLive], Phase: r.phase,
		TradeUID: r.tradeUID, Busy: r.busy, Pending: append([]sbPendingOrder(nil), r.pending...), Halt: r.haltReason, LiveCap: sbLiveMaxLots(), Config: r.cfg, Date: r.date, Now: time.Now().In(lutIST()).Format("15:04:05.0"),
		TargetQty: 2 * r.cfg.Straddles, LotSize: r.lotSize, Expiry: r.expiry, ATM: r.atm, Spot: r.spot,
		Strikes: append([]float64(nil), r.strikes...), Tranches: r.tranches, LastPlan: r.lastPlan, LastAuth: r.lastAuth, Risk: r.risk,
		Events: append([]SBEvent(nil), events...), Fills: r.pms.Fills(), Error: r.errText, Live: r.live,
	}
	if !r.startedAt.IsZero() {
		st.StartedAt = r.startedAt.In(lutIST()).Format("15:04:05")
	}
	if !r.resumedAt.IsZero() {
		st.ResumedAt = r.resumedAt.In(lutIST()).Format("15:04:05")
	}
	st.Position = r.pms.View(r.chain)
	st.Remaining = st.TargetQty - (st.Position.BuildCE + st.Position.BuildPE)
	return st
}

// --- persistence (restart: continue where it left off) ---------------------

// sbRunFile is one rule's run as saved to disk.
type sbRunFile struct {
	RuleID    string            `json:"rule_id"`
	Date      string            `json:"date"`
	Config    SBConfig          `json:"config"`
	Phase     string            `json:"phase"`
	StartedAt time.Time         `json:"started_at"`
	ResumedAt time.Time         `json:"resumed_at,omitempty"`
	Tranches  int               `json:"tranches"`
	LotSize   int               `json:"lot_size"`
	Expiry    string            `json:"expiry"`
	BuildATM  float64           `json:"build_atm"`
	Strikes   []float64         `json:"strikes"`
	Fills     []PMSFill         `json:"fills"`
	Marks     []PMSLeg          `json:"marks"` // last known mark/greeks per leg
	Risk      map[string]string `json:"risk"`
	LastAuth  *DepthEntryPlan   `json:"last_authorized,omitempty"`
	Events    []SBEvent         `json:"events"`
	SavedAt   time.Time         `json:"saved_at"`
	Live      bool              `json:"live"`
	TradeUID  string            `json:"trade_uid,omitempty"`
	Pending   []sbPendingOrder  `json:"pending,omitempty"`
	Halt      string            `json:"halt,omitempty"`
}

func sbRunDir() string { return filepath.Join(filepath.Dir(sbRulesPath()), "sbuild_runs") }

func sbRunPath(id string) string { return filepath.Join(sbRunDir(), id+".json") }

// persistLocked writes the run atomically (tmp + rename). Idle runs that
// never started have nothing to save.
func (r *sbRunner) persistLocked() {
	r.dirty = false
	if r.phase == "IDLE" || r.cfg.ID == "" {
		return
	}
	f := sbRunFile{
		RuleID: r.cfg.ID, Date: r.date, Config: r.cfg, Phase: r.phase, StartedAt: r.startedAt, ResumedAt: r.resumedAt,
		Tranches: r.tranches, LotSize: r.lotSize, Expiry: r.expiry, BuildATM: r.buildATM, Strikes: r.strikes,
		Fills: r.pms.Fills(), Marks: r.pms.Marks(), Risk: r.risk, LastAuth: r.lastAuth, Events: r.events, SavedAt: time.Now(),
		Live: r.isLive, TradeUID: r.tradeUID, Pending: r.pending, Halt: r.haltReason,
	}
	b, err := json.Marshal(f)
	if err != nil {
		log.Printf("[SBUILD-SHADOW] rule=%s save: %v", r.cfg.ID, err)
		return
	}
	if err := sbWriteAtomic(sbRunPath(r.cfg.ID), b); err != nil {
		log.Printf("[SBUILD-SHADOW] rule=%s save: %v", r.cfg.ID, err)
	}
}

func sbWriteAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// restoreLocked reloads a saved run. Today's BUILDING / COMPLETE runs
// resume: PMS is rebuilt from every saved fill, the position is re-checked
// (filled vs target, net delta, strikes) and the minute-end checks run on
// the first tick (an exit time / SL / TP hit while down exits at once).
// Runs from an earlier day are not resumed.
func (r *sbRunner) restoreLocked() {
	b, err := os.ReadFile(sbRunPath(r.cfg.ID))
	if err != nil {
		return
	}
	var f sbRunFile
	if err := json.Unmarshal(b, &f); err != nil {
		log.Printf("[SBUILD-SHADOW] rule=%s saved run unreadable: %v", r.cfg.ID, err)
		return
	}
	r.phase, r.date, r.startedAt, r.resumedAt = f.Phase, f.Date, f.StartedAt, f.ResumedAt
	r.tranches, r.lotSize, r.expiry, r.buildATM, r.strikes = f.Tranches, f.LotSize, f.Expiry, f.BuildATM, f.Strikes
	r.risk, r.lastAuth, r.events = f.Risk, f.LastAuth, f.Events
	r.isLive, r.tradeUID, r.pending, r.haltReason = f.Live, f.TradeUID, f.Pending, f.Halt
	if r.risk == nil {
		r.risk = map[string]string{}
	}
	r.pms = NewPMS()
	for _, fl := range f.Fills {
		r.pms.ApplyFill(fl)
	}
	r.pms.SetMarks(f.Marks)
	if r.isLive && len(r.pending) > 0 && r.phase != "HALTED" {
		// Restarted while an order was in flight: its fill is unknown.
		var ids []string
		for _, p := range r.pending {
			ids = append(ids, fmt.Sprintf("%s %s %s %d (intent %s, broker %s)", p.Role, p.Side, p.OptionType, p.Qty, p.IntentID, p.BrokerOrderID))
		}
		r.phase = "HALTED"
		r.haltReason = "restart with order(s) in flight: " + strings.Join(ids, "; ")
		r.event("HALT", "restart: LIVE run had %d order(s) in flight whose fill is unknown -- HALTED, nothing will be sent. Check the broker order book for trade %s: %s",
			len(r.pending), r.tradeUID, strings.Join(ids, "; "))
		r.persistLocked()
		return
	}
	if r.phase == "EXITING" {
		r.phase = "COMPLETE" // exit orders all confirmed before the restart; the minute check re-runs the exit
	}
	if !r.active() {
		return
	}
	// The running build's symbol / expiry are those of its position.
	if f.Config.Symbol != "" {
		r.cfg.Symbol, r.cfg.Expiry = f.Config.Symbol, f.Config.Expiry
	}
	if f.Date != sbToday() && r.isLive && !r.pms.View(nil).Flat {
		r.phase = "HALTED"
		r.haltReason = "LIVE position from " + f.Date + " still open in PMS -- check the broker positions"
		r.event("HALT", "restart: LIVE run from %s still holds a position -- HALTED (no orders); check the broker and exit manually", f.Date)
		r.persistLocked()
		return
	}
	if f.Date != sbToday() {
		r.phase = "STOPPED"
		r.event("RESUME", "restart: run from %s not resumed (previous day) -- marked STOPPED", f.Date)
		r.persistLocked()
		return
	}
	v := r.pms.View(nil)
	target := 2 * r.cfg.Straddles
	filled := v.BuildCE + v.BuildPE
	r.resumedAt = time.Now()
	r.lastMinute = 0           // minute-end checks run on the first tick
	r.lastTranche = time.Now() // one full gap before the next tranche
	var legs []string
	for _, l := range v.Legs {
		if l.Qty != 0 {
			legs = append(legs, fmt.Sprintf("%.0f%s %+d @%.2f", l.Strike, l.OptionType, l.Qty, l.AvgPrice))
		}
	}
	r.event("RESUME", "restart: restored %s %s run saved at %s -- PMS rebuilt from %d fills: CE %d + PE %d = %d of %d sold (remaining %d) across strikes %s; legs [%s]; re-running the minute-end checks now",
		map[bool]string{false: "SHADOW", true: "LIVE"}[r.isLive], r.phase, f.SavedAt.In(lutIST()).Format("15:04:05"), len(f.Fills), v.BuildCE, v.BuildPE, filled, target, target-filled,
		sbStrikesText(r.strikes), strings.Join(legs, ", "))
	if filled > target {
		r.event("RESUME", "WARNING: sold %d is above the target %d -- no more tranches", filled, target)
	}
	if r.lotSize > 0 && r.phase == "BUILDING" && target-filled < int64(r.lotSize) {
		r.phase = "COMPLETE"
		r.event("RESUME", "nothing left to build -- COMPLETE (monitors keep running)")
	}
	r.persistLocked()
}

// --- HTTP -----------------------------------------------------------------

type sbIDBody struct {
	ID string `json:"id"`
}

func sbDecodeID(r *http.Request) string {
	var b sbIDBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	if b.ID == "" {
		b.ID = r.URL.Query().Get("id")
	}
	return strings.TrimSpace(b.ID)
}

type sbStartBody struct {
	ID      string `json:"id"`
	Mode    string `json:"mode"`    // SHADOW (default) / LIVE
	Confirm string `json:"confirm"` // LIVE: "SELL LIVE"; exit: "EXIT"
}

// SBStart: POST /api/sbuild/shadow/start {"id", "mode": "LIVE", "confirm": "SELL LIVE"}
func (h *Handlers) SBStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b sbStartBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	live := strings.EqualFold(strings.TrimSpace(b.Mode), "LIVE")
	if err := h.Service.StartStraddleBuild(strings.TrimSpace(b.ID), live, b.Confirm); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// SBStop: POST /api/sbuild/shadow/stop {"id": rule}
func (h *Handlers) SBStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	if err := h.Service.StopStraddleBuild(sbDecodeID(r)); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// SBExit: POST /api/sbuild/exit {"id", "confirm": "EXIT"} -- flatten now.
func (h *Handlers) SBExit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b sbStartBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := h.Service.ExitStraddleBuild(strings.TrimSpace(b.ID), b.Confirm); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// SBPause: POST /api/sbuild/pause {id, pause: true|false}.
func (h *Handlers) SBPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b struct {
		ID    string `json:"id"`
		Pause bool   `json:"pause"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := h.Service.PauseStraddleBuild(strings.TrimSpace(b.ID), b.Pause); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// SBStateHandler: GET /api/sbuild/shadow/state -> every rule's run.
func (h *Handlers) SBStateHandler(w http.ResponseWriter, r *http.Request) {
	var runs []SBState
	for _, rn := range sbEng.list() {
		rn.mu.Lock()
		runs = append(runs, rn.stateLocked(false))
		rn.mu.Unlock()
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "runs": runs})
}

// sbRoundToLot is MROUND to the lot (nearest multiple, ties up), like the
// manual-build quantity inputs.
func sbRoundToLot(qty, lot int64) int64 {
	if lot <= 0 {
		return qty
	}
	return ((qty + lot/2) / lot) * lot
}

// sbLotSize resolves the lot size for symbol/expiry from the contract master.
func (s *Service) sbLotSize(symbol, expiry string) (int64, error) {
	if s.Snapshot == nil || s.LotSize == nil {
		return 0, fmt.Errorf("lot size service unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if strings.TrimSpace(expiry) == "" {
		chain, err := s.Snapshot.GetOptionChain(ctx, symbol, "")
		if err != nil {
			return 0, fmt.Errorf("%s chain: %w", symbol, err)
		}
		expiry = chain.Expiry
	}
	ls, err := s.LotSize.GetLotSize(ctx, symbol, expiry)
	if err != nil || ls <= 0 {
		return 0, fmt.Errorf("lot size for %s %s not available: %v", symbol, expiry, err)
	}
	return int64(ls), nil
}

// StartSBEngine starts the always-on loop at boot: loads every saved rule,
// restores each rule's saved run (resuming today's open builds) and
// previews every rule's live straddle.
func (s *Service) StartSBEngine() {
	for _, c := range LoadSBRules() {
		r := newSBRunner(c)
		r.mu.Lock()
		r.restoreLocked()
		r.mu.Unlock()
		sbEng.add(r)
	}
	go s.sbLoop()
}

func sbComputeLive(cfg SBConfig, chain *OptionChainSnapshot, lot, remaining int64, posDelta, participation float64) SBLive {
	lv := SBLive{Symbol: cfg.Symbol, Expiry: chain.Expiry, Target: cfg.TargetStraddle, Spot: lutUnderlying(chain),
		Participation: participation, Updated: time.Now().In(lutIST()).Format("15:04:05.0")}
	atm, err := FindATMRow(*chain)
	if err != nil || atm == nil {
		lv.Error = "no ATM row"
		return lv
	}
	lv.ATM = atm.Strike
	lv.CELtp, lv.PELtp, lv.CEBid, lv.CEAsk, lv.PEBid, lv.PEAsk = atm.CELtp, atm.PELtp, atm.CEBid, atm.CEAsk, atm.PEBid, atm.PEAsk
	lv.LTPStraddle = atm.CELtp + atm.PELtp
	if atm.CEDepth != nil {
		lv.CEDepthLevels, lv.CEDepthAgeMs = len(atm.CEDepth.Bids), atm.CEDepth.AgeMs
		if len(atm.CEDepth.Bids) > 0 && atm.CEDepth.Bids[0].Price > 0 {
			lv.CEBid = atm.CEDepth.Bids[0].Price
		}
	}
	if atm.PEDepth != nil {
		lv.PEDepthLevels, lv.PEDepthAgeMs = len(atm.PEDepth.Bids), atm.PEDepth.AgeMs
		if len(atm.PEDepth.Bids) > 0 && atm.PEDepth.Bids[0].Price > 0 {
			lv.PEBid = atm.PEDepth.Bids[0].Price
		}
	}
	lv.BidStraddle = lv.CEBid + lv.PEBid
	lv.LotSize, lv.FullQty = lot, cfg.Straddles
	if atm.CEDepth != nil && atm.PEDepth != nil {
		lv.CEBids, lv.CEAsks, lv.PEBids, lv.PEAsks = atm.CEDepth.Bids, atm.CEDepth.Asks, atm.PEDepth.Bids, atm.PEDepth.Asks
		if lot > 0 {
			if c, _, ok1 := walkBids(atm.CEDepth.Bids, lot, 5); ok1 {
				if p, _, ok2 := walkBids(atm.PEDepth.Bids, lot, 5); ok2 {
					lv.VWAPOneLot = c + p
				}
			}
		}
		if cfg.Straddles > 0 {
			c, _, ok1 := walkBids(atm.CEDepth.Bids, cfg.Straddles, 5)
			p, _, ok2 := walkBids(atm.PEDepth.Bids, cfg.Straddles, 5)
			lv.FullFillable = ok1 && ok2
			if lv.FullFillable {
				lv.VWAPFull = c + p
			}
		}
		if lot > 0 && cfg.TargetStraddle > 0 && remaining > 0 {
			plan := PlanDepthEntry(DepthEntryInput{
				CEBids: atm.CEDepth.Bids, PEBids: atm.PEDepth.Bids, CEDelta: atm.CEDelta, PEDelta: atm.PEDelta,
				TargetStraddle: cfg.TargetStraddle, LotSize: lot, Participation: participation, MaxLevels: cfg.MaxLevels,
				RemainingQty: remaining, PositionDelta: posDelta,
			})
			lv.Preview = &plan
		}
	}
	if cfg.TargetStraddle > 0 {
		lv.Gap = lv.BidStraddle - cfg.TargetStraddle
		lv.AboveTarget = lv.BidStraddle > cfg.TargetStraddle
	}
	return lv
}
