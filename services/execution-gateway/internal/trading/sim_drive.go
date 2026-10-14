package trading

// Simulation-driven LUT trade (owner, 2026-10-14): the Paper Sim runs as it
// always does (its own size, synthetic hedge, its SL / TP / time exit), and
// the live LUT trade follows its EVENTS end to end:
//   - the build: the LUT fires the real build on the same first YES the
//     simulation enters on (lut_build.go);
//   - each hedge the simulation books -> the live trade hedges at its own
//     size with the same rule on its own numbers: a minute-end hedge brings
//     live points out to 25 % of live points allowed, an intra-minute hedge
//     takes the complete delta (whole lots, rounded down);
//   - the simulation's exit (SL / TP / TIME / MTM) -> the live trade is
//     squared off with that reason.
// Sizes are never copied from the simulation, only the decisions.
//
// The simulation of the live trade is re-run every second on the recorded
// minutes and seconds (the same data the Paper Sim tab shows). From the
// first second until the trade closes, the monitor takes no hedge, SL or TP
// of its own for this trade (simDriveOwns) -- only what the simulation
// triggered; a stalled simulation is reported, not replaced. The hard exit
// time (15:37) stays with the monitor as well. When the trade is closed by
// any path, everything here is released.

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
	"sync/atomic"
	"time"
)

const (
	simDriveEvery = time.Second
	simDriveStale = time.Second // warn when the simulation has not run for this long
)

// SimDriveAction is one simulation event and what the live trade did.
type SimDriveAction struct {
	At       string  `json:"at"`        // when the live trade acted (HH:MM:SS)
	SimTime  string  `json:"sim_time"`  // the event's time in the simulation
	Kind     string  `json:"kind"`      // RISK_BREACH (minute end) / INTRA_MINUTE / EXIT
	Reason   string  `json:"reason"`    // exit reason
	SimDelta float64 `json:"sim_delta"` // simulation's delta before (per its size)
	Delta    float64 `json:"live_delta"`
	Lots     int64   `json:"lots"`
	Result   string  `json:"result"`
	// DecideMs: from the data the simulation acted on (the tick, or the
	// minute's start) to the live decision; OrderMs: the hedge / exit call.
	DecideMs int64 `json:"decide_ms"`
	OrderMs  int64 `json:"order_ms"`
}

// simEventAge: ms from an event's time (HH:MM, HH:MM:SS or HH:MM:SS.mmm
// today, IST) to now.
func simEventAge(t string) int64 {
	now := time.Now().In(lutIST())
	layout := map[int]string{5: "15:04", 8: "15:04:05", 12: "15:04:05.000"}[len(t)]
	if layout == "" {
		return -1
	}
	p, err := time.ParseInLocation("2006-01-02 "+layout, now.Format("2006-01-02")+" "+t, lutIST())
	if err != nil {
		return -1
	}
	return now.Sub(p).Milliseconds()
}

type simDriver struct {
	TradeUID string           `json:"trade_uid"`
	Entry    string           `json:"entry"`
	Started  string           `json:"started"`
	LastRun  time.Time        `json:"last_run"`
	SimState string           `json:"sim_status"`
	SimPnL   float64          `json:"sim_pnl_per_qty"`
	RunMs    int64            `json:"run_ms"` // last simulation pass
	Error    string           `json:"error,omitempty"`
	Actions  []SimDriveAction `json:"actions"`
	seen     map[string]bool
	exited   bool
	day      string
}

var simDrive = struct {
	mu sync.Mutex
	d  *simDriver // today's LUT trade (one per day)
}{}

// simDriveOwns: this trade's hedges and SL / TP come from its simulation;
// fresh = the simulation ran within simDriveStale.
func simDriveOwns(tradeUID string) (owned, fresh bool) {
	simDrive.mu.Lock()
	defer simDrive.mu.Unlock()
	d := simDrive.d
	if d == nil || d.TradeUID != tradeUID {
		return false, false
	}
	return true, d.Error == "" && time.Since(d.LastRun) <= simDriveStale
}

func simDriveActive(tradeUID string) bool {
	owned, fresh := simDriveOwns(tradeUID)
	return owned && fresh
}

// simDriveRelease drops the driver of a closed trade.
func simDriveRelease(tradeUID, why string) {
	simDrive.mu.Lock()
	defer simDrive.mu.Unlock()
	if d := simDrive.d; d != nil && d.TradeUID == tradeUID {
		simDrive.d = nil
		log.Printf("[SIM-DRIVE][%s] released (%s): no further simulation actions", tradeUID, why)
	}
}

// StartSimDrive starts the loop (call once at boot).
func (s *Service) StartSimDrive() {
	go func() {
		t := time.NewTicker(simDriveEvery)
		defer t.Stop()
		for range t.C {
			s.simDriveKick() // backstop; the LUT engine kicks every 100 ms tick
		}
	}()
}

var simDriveBusy atomic.Bool

// simDriveKick runs one driver pass in the background unless one is
// running (never blocks the caller -- the LUT engine tick).
func (s *Service) simDriveKick() {
	if !simDriveBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer simDriveBusy.Store(false)
		s.simDriveTick()
	}()
}

// The recorded minutes, reloaded only when the LUT engine records a new
// minute (simDriveMinutes).
var simDriveCache struct {
	day, key string
	mins     []simMinute
}

// simWithTicks: a copy of the minutes whose seconds, from entry on, are the
// in-memory 100 ms ticks (when held -- today, this gateway run).
func simWithTicks(day string, base []simMinute, entry string) []simMinute {
	mins := make([]simMinute, len(base))
	copy(mins, base)
	ticks := lutTicksFrom(day, lutHHMM(entry))
	for i := range mins {
		if tk, ok := ticks[mins[i].HHMM]; ok && len(tk) > 0 {
			mins[i].secs = tk
		}
	}
	return mins
}

func lutRecordKey() string {
	e := lutEng
	e.mu.Lock()
	defer e.mu.Unlock()
	return fmt.Sprintf("%s|%d|%d|%d", e.day, e.lastChain, e.lastMinute, len(e.minutes))
}

func simDriveMinutes(day string) []simMinute {
	if k := lutRecordKey(); k != simDriveCache.key || day != simDriveCache.day {
		simDriveCache.day, simDriveCache.key = day, k
		simDriveCache.mins = lutLoadSimSetHold(day, "", false)
	}
	return simDriveCache.mins
}

// simDriveCandidates: today's real LUT build, then sim-driven test fires
// (newest first) -- copies, those that made a trade.
func simDriveCandidates() ([]LUTBuildRun, string) {
	e := lutEng
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []LUTBuildRun
	if b := e.build; b != nil && b.TradeUID != "" {
		out = append(out, *b)
	}
	for i := len(e.tests) - 1; i >= 0; i-- {
		if t := e.tests[i]; t.SimDriven && t.TradeUID != "" {
			out = append(out, *t)
		}
	}
	return out, e.day
}

func (s *Service) simDriveTick() {
	runs, day := simDriveCandidates()
	// Release a driver whose trade has closed.
	simDrive.mu.Lock()
	cur := ""
	if simDrive.d != nil {
		cur = simDrive.d.TradeUID
	}
	simDrive.mu.Unlock()
	if cur != "" {
		if t, ok := s.Store.LoadTrade(cur); !ok {
			simDriveRelease(cur, "trade not found")
		} else if t.Status == "SQUARING_OFF" || t.Status == "PARTIAL-SQF" {
			return // exit running: no new actions, kept until it ends
		} else if t.Status != "ACTIVE" {
			simDriveRelease(cur, "trade "+t.Status)
		}
	}
	// The first candidate whose trade is open.
	var run LUTBuildRun
	var tr StoredTrade
	for _, c := range runs {
		if t, ok := s.Store.LoadTrade(c.TradeUID); ok && t.Status == "ACTIVE" {
			run, tr = c, t
			break
		}
	}
	uid, entry, exitTime := run.TradeUID, strings.TrimSuffix(run.Minute, " (test)"), run.ExitTime
	if uid == "" || entry == "" {
		return
	}
	if exitTime == "" {
		exitTime = "15:37"
	}
	simDrive.mu.Lock()
	d := simDrive.d
	if d == nil || d.TradeUID != uid {
		d = &simDriver{TradeUID: uid, Entry: entry, Started: time.Now().In(lutIST()).Format("15:04:05"), seen: map[string]bool{}, day: day}
		simDrive.d = d
	}
	first := len(d.seen) == 0 && d.SimState == ""
	simDrive.mu.Unlock()

	lot := int64(tr.LotSize)
	if lot <= 0 {
		lot = 65
	}
	t0 := time.Now()
	// Minutes from memory (reloaded only on a new recorded minute) and, for
	// the trade's minutes, every 100 ms tick of the future (lut_seconds.go).
	mins := simWithTicks(day, simDriveMinutes(day), entry)
	cfg := simDriveConfigOf(run, tr.Strike, lutDrivingRule().Key)
	cfg.ExitTime = exitTime
	res := RunPaperSim(mins, cfg, lot, nil)
	// Finished minutes: keep only what decides the same next time.
	cut := map[int]string{}
	for _, h := range res.Hedges {
		if h.Kind == "INTRA_MINUTE" {
			cut[lutHHMM(h.Time[:5])] = h.Time
		}
	}
	nowIST := time.Now().In(lutIST())
	lutTicksCompact(day, nowIST.Hour()*100+nowIST.Minute(), cut)

	simDrive.mu.Lock()
	d.LastRun, d.SimState, d.SimPnL, d.Error = time.Now(), res.Status, res.PnLPerStraddle, res.Error
	d.RunMs = time.Since(t0).Milliseconds()
	if res.Error != "" {
		simDrive.mu.Unlock()
		return
	}
	var todo []PaperSimHedge
	for _, h := range res.Hedges {
		if h.Kind == "ENTRY_NEUTRAL" { // the live build is delta-neutral by its CE:PE ratio
			continue
		}
		key := h.Time[:5] + "|" + h.Kind // at most one of each kind per minute
		if d.seen[key] {
			continue
		}
		d.seen[key] = true
		if !first { // events before the driver started (restart) already happened or not
			todo = append(todo, h)
		}
	}
	exitNow := res.Status == "EXITED" && !d.exited
	if exitNow {
		d.exited = true
	}
	if first {
		log.Printf("[SIM-DRIVE][%s] driving from the simulation of entry %s (%d earlier simulation hedge(s) not replayed)", uid, entry, len(d.seen))
	}
	simDrive.mu.Unlock()

	for _, h := range todo {
		s.simDriveHedge(d, tr, run, h, lot)
	}
	if exitNow {
		s.simDriveExit(d, uid, res)
	}
}

func (s *Service) simDriveHedge(d *simDriver, tr StoredTrade, run LUTBuildRun, h PaperSimHedge, lot int64) {
	a := SimDriveAction{At: time.Now().In(lutIST()).Format("15:04:05.000"), SimTime: h.Time, Kind: h.Kind, SimDelta: h.DeltaBefore,
		DecideMs: simEventAge(h.Time)}
	frac, how := hedgeIntraFrac, "complete live delta"
	if snap, ok := s.Store.LoadSnapshot(tr.TradeUID); ok {
		a.Delta = snap.NetDelta
		if rule := simRunRule(run, lutDrivingRule().Key); h.Kind != "INTRA_MINUTE" && rule.LeaveBand > 0 { // new: live points out -> 25 % of live allowed
			eff := math.Max(snap.PointsAllowed, snap.Underlying*resolveHedgeMinThresholdBps(tr.Config.HedgeMinThresholdBps)/10000)
			if run.SimPointsAllowed > 0 {
				eff = run.SimPointsAllowed // test band, as in its simulation
			}
			frac = rule.minuteFrac(snap.PointsOut, eff)
			how = fmt.Sprintf("live points out %.1f -> %.1f (25%% of allowed %.1f)", snap.PointsOut, hedgeLeaveBand*eff, eff)
		}
	}
	a.Lots = hedgeLotsFloor(frac*a.Delta, lot)
	if a.Lots == 0 && run.SimMinOneLot && math.Abs(a.Delta) > 0 {
		a.Lots = 1 // test: a real hedge order on every simulated hedge
	}
	if tr.Lots > 0 && a.Lots > int64(tr.Lots) {
		a.Lots = int64(tr.Lots)
	}
	if a.Lots <= 0 {
		a.Result = fmt.Sprintf("no order: %s needs %.0f%% of live delta %+.1f, under one lot", how, frac*100, a.Delta)
	} else {
		reason := fmt.Sprintf("SIM-DRIVEN %s at %s (sim delta %+.4f per qty): %s = %.0f%% of live delta %+.1f = %d lot(s)",
			h.Kind, h.Time, h.DeltaBefore, how, frac*100, a.Delta, a.Lots)
		log.Printf("[SIM-DRIVE][%s] %s (decided %d ms after the data)", tr.TradeUID, reason, a.DecideMs)
		o0 := time.Now()
		err := s.ManualHedgeLots(context.WithValue(context.Background(), hedgeTriggerKey{}, reason), tr.TradeUID, int(a.Lots))
		a.OrderMs = time.Since(o0).Milliseconds()
		if err != nil {
			a.Result = "hedge failed: " + err.Error()
		} else {
			a.Result = "hedged"
		}
	}
	log.Printf("[SIM-DRIVE][%s] %s %s -> %s", tr.TradeUID, h.Kind, h.Time, a.Result)
	d.record(a)
}

func (s *Service) simDriveExit(d *simDriver, uid string, res PaperSimResult) {
	reason := res.ExitReason
	status := map[string]string{"SL": "CLOSED_SL", "TP": "CLOSED_TP", "TIME": "CLOSED_TIME"}[reason]
	if status == "" {
		reason, status = "MTM", "CLOSED_MTM"
	}
	a := SimDriveAction{At: time.Now().In(lutIST()).Format("15:04:05.000"), SimTime: res.ExitTime, Kind: "EXIT", Reason: res.ExitReason,
		Result: "square-off started (" + reason + ")", DecideMs: simEventAge(res.ExitTime)}
	log.Printf("[SIM-DRIVE][%s] simulation exited %s at %s (pnl %.2f per qty) -> squaring off the live trade", uid, res.ExitReason, res.ExitTime, res.PnLPerStraddle)
	s.executeAutoExit(uid, reason, status)
	d.record(a)
}

func (d *simDriver) record(a SimDriveAction) {
	simDrive.mu.Lock()
	d.Actions = append(d.Actions, a)
	simDrive.mu.Unlock()
	b, _ := json.Marshal(struct {
		Trade string `json:"trade"`
		SimDriveAction
	}{d.TradeUID, a})
	if f, err := os.OpenFile(filepath.Join(lutDataDir(), d.day+"_simdrive.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}

// simDriveConfigOf is the simulation that drives a LUT trade: the Paper
// Sim as it runs, on the trade's own strike, with the SL / TP / exit time
// the trade was armed with and, for a test fire, its fixed hedge band. The
// Paper Sim tab shows the same simulation for that trade.
func simDriveConfigOf(run LUTBuildRun, strike float64, tables string) PaperSimConfig {
	exit := run.ExitTime
	if exit == "" {
		exit = "15:37"
	}
	return PaperSimConfig{ID: "LIVE-" + run.TradeUID, Source: "drives " + run.TradeUID, Entry: strings.TrimSuffix(run.Minute, " (test)"),
		Lots: 1, HedgeMode: "synthetic", ExitTime: exit, SLBps: run.SLBps, TPBps: run.TPBps, PointsAllowed: run.SimPointsAllowed, Strike: strike,
		HedgeRule: simRunRule(run, tables).Key}
}

// simRunRule: a test's own hedge rule, else the driving set's.
func simRunRule(run LUTBuildRun, tables string) hedgeRule {
	if run.SimHedgeRule != "" {
		return hedgeRuleFor(run.SimHedgeRule)
	}
	return hedgeRuleFor(tables)
}

// simDrivenRun finds the LUT run (real build or sim-driven test) that made
// a trade on a day, from the LUT day state.
func simDrivenRun(day, uid string) (LUTBuildRun, bool) {
	_, st := lutLoadDay(day)
	if b := st.Build; b != nil && b.TradeUID == uid {
		return *b, true
	}
	for _, t := range st.Tests {
		if t != nil && t.SimDriven && t.TradeUID == uid {
			return *t, true
		}
	}
	return LUTBuildRun{}, false
}

// SimDriveHandler: GET -> the driven trade, the simulation's state and
// every action taken.
func (h *Handlers) SimDriveHandler(w http.ResponseWriter, r *http.Request) {
	simDrive.mu.Lock()
	var d *simDriver
	if simDrive.d != nil {
		cp := *simDrive.d
		cp.Actions = append([]SimDriveAction(nil), simDrive.d.Actions...)
		d = &cp
	}
	simDrive.mu.Unlock()
	active := d != nil && simDriveActive(d.TradeUID)
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "driver": d, "active": active,
		"note": "a simulated minute-end hedge brings live points out to 25% of live points allowed, an intra-minute one hedges the complete live delta; SL / TP / MTM exits follow the simulation; hard exit 15:37"})
}
