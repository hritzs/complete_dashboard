package trading

// Simulation-driven LUT trade (owner, 2026-10-14): the Paper Sim runs as it
// always does (its own size, synthetic hedge, its SL / TP / time exit), and
// the live LUT trade follows its EVENTS end to end:
//   - the build: the LUT fires the real build on the same first YES the
//     simulation enters on (lut_build.go);
//   - each hedge the simulation books -> the live trade hedges at its own
//     size: a minute-end hedge takes 75 % of the live net delta, an
//     intra-minute hedge the complete delta (whole lots, rounded down);
//   - the simulation's exit (SL / TP / TIME / MTM) -> the live trade is
//     squared off with that reason.
// Sizes are never copied from the simulation, only the decisions.
//
// The simulation of the live trade is re-run every second on the recorded
// minutes and seconds (the same data the Paper Sim tab shows). While it is
// healthy the monitor's own hedge and SL / TP / TIME checks stand aside for
// this trade (simDriveActive); if it cannot run for simDriveStale, the
// monitor's own rules take over again.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

// simDriveActive: the simulation is driving this trade right now.
func simDriveActive(tradeUID string) bool {
	simDrive.mu.Lock()
	defer simDrive.mu.Unlock()
	d := simDrive.d
	return d != nil && d.TradeUID == tradeUID && d.Error == "" && time.Since(d.LastRun) <= simDriveStale
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

// simDriveTarget is today's real LUT build trade, if any.
func simDriveTarget() (uid, entry, exitTime, day string) {
	e := lutEng
	e.mu.Lock()
	defer e.mu.Unlock()
	if b := e.build; b != nil && b.TradeUID != "" {
		return b.TradeUID, b.Minute, b.ExitTime, e.day
	}
	return "", "", "", e.day
}

func (s *Service) simDriveTick() {
	uid, entry, exitTime, day := simDriveTarget()
	if uid == "" || entry == "" {
		return
	}
	tr, ok := s.Store.LoadTrade(uid)
	if !ok || tr.Status != "ACTIVE" {
		return
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
	cfg := PaperSimConfig{ID: "LIVE-" + uid, Source: "drives " + uid, Entry: entry, Lots: 1, HedgeMode: "synthetic", ExitTime: exitTime}
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
		s.simDriveHedge(d, tr, h, lot)
	}
	if exitNow {
		s.simDriveExit(d, uid, res)
	}
}

func (s *Service) simDriveHedge(d *simDriver, tr StoredTrade, h PaperSimHedge, lot int64) {
	frac := hedgeMinuteFrac
	if h.Kind == "INTRA_MINUTE" {
		frac = hedgeIntraFrac
	}
	a := SimDriveAction{At: time.Now().In(lutIST()).Format("15:04:05"), SimTime: h.Time, Kind: h.Kind, SimDelta: h.DeltaBefore}
	if snap, ok := s.Store.LoadSnapshot(tr.TradeUID); ok {
		a.Delta = snap.NetDelta
	}
	a.Lots = hedgeLotsFloor(frac*a.Delta, lot)
	if tr.Lots > 0 && a.Lots > int64(tr.Lots) {
		a.Lots = int64(tr.Lots)
	}
	if a.Lots <= 0 {
		a.Result = fmt.Sprintf("no order: %.0f%% of live delta %+.1f is under one lot", frac*100, a.Delta)
	} else {
		reason := fmt.Sprintf("SIM-DRIVEN %s at %s (sim delta %+.4f per qty): %.0f%% of live delta %+.1f = %d lot(s)",
			h.Kind, h.Time, h.DeltaBefore, frac*100, a.Delta, a.Lots)
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
		"note": strings.TrimSpace(fmt.Sprintf("hedge %.0f%% of live delta at a simulated minute-end hedge, %.0f%% at an intra-minute one; exits follow the simulation", hedgeMinuteFrac*100, hedgeIntraFrac*100))})
}
