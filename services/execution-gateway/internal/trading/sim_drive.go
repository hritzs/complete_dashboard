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
	"time"
)

const (
	simDriveEvery = time.Second
	simDriveStale = 5 * time.Second
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
}

type simDriver struct {
	TradeUID string           `json:"trade_uid"`
	Entry    string           `json:"entry"`
	Started  string           `json:"started"`
	LastRun  time.Time        `json:"last_run"`
	SimState string           `json:"sim_status"`
	SimPnL   float64          `json:"sim_pnl_per_qty"`
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
			s.simDriveTick()
		}
	}()
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
	mins := lutLoadSimSet(day, "")
	// The simulation as the Paper Sim runs it, with the SL / TP the live
	// trade was armed with.
	cfg := PaperSimConfig{ID: "LIVE-" + uid, Source: "drives " + uid, Entry: entry, Lots: 1, HedgeMode: "synthetic", ExitTime: exitTime,
		SLBps: run.SLBps, TPBps: run.TPBps, PointsAllowed: run.SimPointsAllowed}
	res := RunPaperSim(mins, cfg, lot, nil)

	simDrive.mu.Lock()
	d.LastRun, d.SimState, d.SimPnL, d.Error = time.Now(), res.Status, res.PnLPerStraddle, res.Error
	if res.Error != "" {
		simDrive.mu.Unlock()
		return
	}
	var todo []PaperSimHedge
	for _, h := range res.Hedges {
		if h.Kind == "ENTRY_NEUTRAL" { // the live build is delta-neutral by its CE:PE ratio
			continue
		}
		key := h.Time + "|" + h.Kind
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
	a := SimDriveAction{At: time.Now().In(lutIST()).Format("15:04:05"), SimTime: h.Time, Kind: h.Kind, SimDelta: h.DeltaBefore}
	frac, how := hedgeIntraFrac, "complete live delta"
	if snap, ok := s.Store.LoadSnapshot(tr.TradeUID); ok {
		a.Delta = snap.NetDelta
		if h.Kind != "INTRA_MINUTE" { // minute end: live points out -> 25 % of live allowed
			eff := math.Max(snap.PointsAllowed, snap.Underlying*resolveHedgeMinThresholdBps(tr.Config.HedgeMinThresholdBps)/10000)
			if run.SimPointsAllowed > 0 {
				eff = run.SimPointsAllowed // test band, as in its simulation
			}
			frac = hedgeMinuteFracFor(snap.PointsOut, eff)
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
		log.Printf("[SIM-DRIVE][%s] %s", tr.TradeUID, reason)
		if err := s.ManualHedgeLots(context.WithValue(context.Background(), hedgeTriggerKey{}, reason), tr.TradeUID, int(a.Lots)); err != nil {
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
	a := SimDriveAction{At: time.Now().In(lutIST()).Format("15:04:05"), SimTime: res.ExitTime, Kind: "EXIT", Reason: res.ExitReason,
		Result: "square-off started (" + reason + ")"}
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
