package trading

// LUT actual build: the LUT's first YES while ARMED fires a REAL straddle
// build -- the same build an Automation entry runs (ExecuteFinalBuild ->
// DeployStraddle: delta-neutral CE/PE sizing, the full size sold at once in
// order chunks, SL / TP / exit time / hedge settings attached to the trade
// before any order, then the standard monitor) -- triggered by the LUT's
// answer instead of a clock time.
//
//   - Once per day: the build is recorded in the LUT day state before it
//     fires; once sent (BUILT / FAILED / UNKNOWN) it never fires again
//     that day, across restarts. A build still FIRING when the gateway
//     restarted is marked UNKNOWN and never re-sent. The FIRST YES is the
//     entry: it sells then (nothing transient blocks it -- configuration
//     problems are refused at arming), no later YES fires.
//   - Only the live minute-end YES fires it: never a what-if, never a
//     minute reloaded after a restart. Independent of the paper entry:
//     "Start now" (paper) never blocks it.
//   - The strike and expiry are exactly the ones the LUT evaluated; TP is
//     the LUT's own TP at the YES minute and SL the LUT's 14 bps, unless
//     overridden on the tab.
//   - Arming needs the typed confirmation "SELL LIVE", is refused while the
//     configuration is invalid (account, exit time), and stays armed
//     (saved) until disarmed.

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
	"time"
)

const lutBuildConfirm = "SELL LIVE"

// LUTBuildConfig is what the LUT tab sets for the real build.
type LUTBuildConfig struct {
	Armed            bool    `json:"armed"`
	ArmedAt          string  `json:"armed_at,omitempty"`
	UserID           string  `json:"user_id"`
	BrokerName       string  `json:"broker_name"`
	AccountID        string  `json:"account_id"`
	ExchangeSegment  string  `json:"exchange_segment"`
	ProductType      string  `json:"product_type"`
	Lots             int     `json:"lots"`
	OrderLotsPerCall int     `json:"order_lots_per_call"`
	ExitTime         string  `json:"exit_time"`
	SLBps            float64 `json:"sl_bps"` // 0 = the LUT's own (14 bps)
	TPBps            float64 `json:"tp_bps"` // 0 = the LUT's TP at the YES minute
	WingPct          float64 `json:"wing_pct"`
	BuyBuffer        float64 `json:"buy_buffer"`
	SellBuffer       float64 `json:"sell_buffer"`
	HedgeDiv         float64 `json:"hedge_div"`
	StraddleDiv      float64 `json:"straddle_div"`
}

// LUTBuildRun is the day's one real build (saved in the LUT day state).
type LUTBuildRun struct {
	Status     string  `json:"status"` // FIRING / BUILT / FAILED / REFUSED / UNKNOWN
	Minute     string  `json:"minute"` // the LUT YES minute
	FiredAt    string  `json:"fired_at"`
	FinishedAt string  `json:"finished_at,omitempty"`
	Expiry     string  `json:"expiry"`
	Strike     float64 `json:"strike"`
	Lots       int     `json:"lots"`
	SLBps      float64 `json:"sl_bps"`
	TPBps      float64 `json:"tp_bps"`
	ExitTime   string  `json:"exit_time"`
	Account    string  `json:"account"`
	TradeUID   string  `json:"trade_uid,omitempty"`
	Message    string  `json:"message,omitempty"`
	Error      string  `json:"error,omitempty"`
	Test       bool    `json:"test,omitempty"` // a user test fire, not the LUT's YES
}

func lutBuildDefaults(c LUTBuildConfig) LUTBuildConfig {
	def := func(s *string, v string) {
		if strings.TrimSpace(*s) == "" {
			*s = v
		}
	}
	def(&c.UserID, "U001")
	def(&c.BrokerName, "greeksoft")
	def(&c.AccountID, "147")
	def(&c.ExchangeSegment, "NSEFO")
	def(&c.ProductType, "NRML")
	def(&c.ExitTime, "15:37:00")
	if c.Lots <= 0 {
		c.Lots = 1
	}
	if c.OrderLotsPerCall <= 0 {
		c.OrderLotsPerCall = 1
	}
	if c.HedgeDiv <= 0 {
		c.HedgeDiv = 57
	}
	if c.StraddleDiv <= 0 {
		c.StraddleDiv = 4
	}
	return c
}

func lutBuildFile() string { return filepath.Join(lutDataDir(), "lut_build.json") }

func loadLUTBuildConfig() LUTBuildConfig {
	var c LUTBuildConfig
	if b, err := os.ReadFile(lutBuildFile()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return lutBuildDefaults(c)
}

func saveLUTBuildConfig(c LUTBuildConfig) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(lutDataDir(), 0o755); err != nil {
		return err
	}
	return sbWriteAtomic(lutBuildFile(), b)
}

// lutBuildRisk is the risk the real build attaches: the LUT's own SL/TP
// unless the tab overrides them.
func lutBuildRisk(c LUTBuildConfig, rec LUTEvaluation) *BuildRiskConfig {
	sl, tp := c.SLBps, c.TPBps
	if sl <= 0 {
		sl = lutSLBps
	}
	if tp <= 0 {
		tp = rec.TPBps
	}
	return &BuildRiskConfig{ExitTime: c.ExitTime, SlBps: sl, TpBps: tp, BuyBuffer: c.BuyBuffer, SellBuffer: c.SellBuffer,
		HedgeDiv: c.HedgeDiv, StraddleDiv: c.StraddleDiv, WingPct: c.WingPct}
}

// lutBuildChecks lists why an armed build could NOT fire right now (empty =
// ready). Shown on the tab and enforced at the YES.
func (s *Service) lutBuildChecks(c LUTBuildConfig, now time.Time) []string {
	var out []string
	if !c.Armed {
		out = append(out, "not armed")
	}
	if strings.TrimSpace(c.BrokerName) == "" || strings.TrimSpace(c.AccountID) == "" {
		out = append(out, "broker / account not set")
	}
	if exit, err := ParseClockTodayIST(c.ExitTime, now); err != nil {
		out = append(out, "exit time: "+err.Error())
	} else if hm := exit.Hour()*100 + exit.Minute(); hm <= lutLastScan {
		out = append(out, fmt.Sprintf("exit time %s is inside the LUT scan window (ends %02d:%02d)", c.ExitTime, lutLastScan/100, lutLastScan%100))
	}
	return out
}

// lutBuildWarnings: conditions that do NOT stop the build (it confirms
// fills from the reconciler DB with a broker-REST fallback) but are shown.
func (s *Service) lutBuildWarnings() []string {
	if s.OrderEvents == nil || !s.OrderEvents.Healthy() {
		return []string{"order confirmations (reconciler push) not healthy right now -- a build would confirm fills via the broker REST fallback"}
	}
	return nil
}

// lutFireBuildLocked runs at the LUT's first live YES (caller holds e.mu).
// It records the day's build BEFORE sending anything, then fires the real
// build in the background.
func (s *Service) lutFireBuildLocked(day string, rec LUTEvaluation, entry *LUTEntry) {
	e := lutEng
	if entry == nil || e.build != nil {
		return // the FIRST YES is the day's one entry
	}
	c := loadLUTBuildConfig()
	if !c.Armed {
		return
	}
	now := time.Now().In(lutIST())
	run := &LUTBuildRun{Minute: rec.Time, FiredAt: now.Format("15:04:05.000"), Expiry: entry.Expiry, Strike: entry.Strike,
		Lots: c.Lots, ExitTime: c.ExitTime, Account: c.BrokerName + "/" + c.AccountID}
	risk := lutBuildRisk(c, rec)
	run.SLBps, run.TPBps = risk.SlBps, risk.TpBps
	if why := s.lutBuildChecks(c, now); len(why) > 0 {
		run.Status, run.Error = "REFUSED", strings.Join(why, "; ")
	} else if err := risk.Validate(now); err != nil {
		run.Status, run.Error = "REFUSED", err.Error()
	} else if entry.Expiry == "" || entry.Strike <= 0 {
		run.Status, run.Error = "REFUSED", "the LUT YES has no expiry / strike"
	} else {
		run.Status = "FIRING"
	}
	e.build = run
	e.saveDayLocked(day) // recorded before any order: never fires twice
	if run.Status != "FIRING" {
		log.Printf("[LUT-BUILD] ⛔ LUT YES at %s but the REAL build was REFUSED: %s", rec.Time, run.Error)
		return
	}
	log.Printf("[LUT-BUILD] 🚀 LUT YES at %s -> REAL BUILD %s %s %.0f straddle %d lot(s), delta-neutral, SL %.2f bps TP %.2f bps exit %s, account %s",
		rec.Time, lutSymbol, entry.Expiry, entry.Strike, c.Lots, risk.SlBps, risk.TpBps, c.ExitTime, run.Account)
	s.lutSendBuild(run, lutBuildRequest(c, entry.Expiry, entry.Strike, risk))
}

// lutBuildRequest is THE request a LUT build sends -- the real YES and the
// test fire use exactly this.
func lutBuildRequest(c LUTBuildConfig, expiry string, strike float64, risk *BuildRiskConfig) FinalBuildRequest {
	k := int(math.Round(strike))
	return FinalBuildRequest{Mode: BuildModeLUT, UserID: c.UserID, BrokerName: c.BrokerName, AccountID: c.AccountID,
		ExchangeSegment: c.ExchangeSegment, ProductType: c.ProductType, Symbol: lutSymbol, TargetExpiry: expiry,
		Lots: c.Lots, OrderLotsPerCall: c.OrderLotsPerCall, DeltaNeutral: true, Risk: risk,
		CEStrikePrice: k, PEStrikePrice: k}
}

// lutSendBuild fires the build in the background and records the outcome
// on run (saved with the day state).
func (s *Service) lutSendBuild(run *LUTBuildRun, req FinalBuildRequest) {
	e := lutEng
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		resp, err := s.ExecuteFinalBuild(ctx, req)
		e.mu.Lock()
		defer e.mu.Unlock()
		run.FinishedAt = time.Now().In(lutIST()).Format("15:04:05.000")
		switch {
		case err != nil:
			run.Status, run.Error = "FAILED", err.Error()
			if resp != nil {
				run.TradeUID = resp.TradeUID
			}
			log.Printf("[LUT-BUILD] ❌ %s build failed: %v (trade %s) -- check Portfolio / broker", run.Kind(), err, run.TradeUID)
		default:
			run.Status, run.TradeUID, run.Message = "BUILT", resp.TradeUID, resp.Message
			if resp.Status != "" {
				run.Message = strings.TrimSpace(resp.Status + " " + resp.Message)
			}
			log.Printf("[LUT-BUILD] ✅ %s build done: trade %s CE %d / PE %d status %s", run.Kind(), resp.TradeUID, resp.CEQty, resp.PEQty, resp.Status)
		}
		if e.day != "" {
			e.saveDayLocked(e.day)
		}
	}()
}

// Kind names the run in logs.
func (r *LUTBuildRun) Kind() string {
	if r.Test {
		return "TEST"
	}
	return "REAL"
}

// lutTestFireLocked fires ONE test build now through exactly the real
// path, as if the LUT had just said YES at the current minute: the LUT's
// current strike, expiry and TP, the tab's config. Never consumes the
// day's real entry. Caller holds e.mu.
func (s *Service) lutTestFireLocked() (*LUTBuildRun, error) {
	e := lutEng
	ev := e.live.Eval
	if ev == nil || ev.Strike <= 0 || ev.TPBps <= 0 {
		return nil, fmt.Errorf("no live LUT evaluation (strike / TP) to use right now")
	}
	expiry := e.expiry
	if expiry == "" {
		return nil, fmt.Errorf("the LUT has no expiry yet")
	}
	c := loadLUTBuildConfig()
	now := time.Now().In(lutIST())
	if !sbLiveWindow(now) {
		return nil, fmt.Errorf("outside the broker session (09:15-15:40)")
	}
	var why []string
	for _, w := range s.lutBuildChecks(c, now) {
		if w != "not armed" && !strings.Contains(w, "scan window") { // a test may run after 13:30
			why = append(why, w)
		}
	}
	risk := lutBuildRisk(c, *ev)
	if len(why) == 0 {
		if err := risk.Validate(now); err != nil {
			why = append(why, err.Error())
		}
	}
	if len(why) > 0 {
		return nil, fmt.Errorf("test refused: %s", strings.Join(why, "; "))
	}
	run := &LUTBuildRun{Test: true, Status: "FIRING", Minute: now.Format("15:04") + " (test)", FiredAt: now.Format("15:04:05.000"),
		Expiry: expiry, Strike: ev.Strike, Lots: c.Lots, SLBps: risk.SlBps, TPBps: risk.TpBps, ExitTime: c.ExitTime,
		Account: c.BrokerName + "/" + c.AccountID}
	e.tests = append(e.tests, run)
	e.saveDayLocked(e.day)
	log.Printf("[LUT-BUILD] 🧪 TEST FIRE (user) -> REAL ORDERS %s %s %.0f straddle %d lot(s), delta-neutral, SL %.2f bps TP %.2f bps exit %s, account %s",
		lutSymbol, expiry, ev.Strike, c.Lots, risk.SlBps, risk.TpBps, c.ExitTime, run.Account)
	s.lutSendBuild(run, lutBuildRequest(c, expiry, ev.Strike, risk))
	return run, nil
}

// LUTBuildTestHandler: POST /api/lut/build/test {"confirm": "SELL LIVE"}.
func (h *Handlers) LUTBuildTestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b struct {
		Confirm string `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if strings.TrimSpace(b.Confirm) != lutBuildConfirm {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": fmt.Sprintf("a test fire sends REAL orders: type %q", lutBuildConfirm)})
		return
	}
	lutEng.mu.Lock()
	run, err := h.Service.lutTestFireLocked()
	var cp LUTBuildRun
	if run != nil {
		cp = *run
	}
	lutEng.mu.Unlock()
	if err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "test": cp})
}

// saveDayLocked persists the LUT day state (caller holds e.mu).
func (e *lutEngine) saveDayLocked(day string) {
	lutSaveDay(lutDayState{Day: day, U0916: e.u0916, OGSource: e.ogSource, Entry: e.entry, Build: e.build, Tests: e.tests})
}

// LUTBuildHandler: GET -> config, today's run, readiness checks.
// POST {config..., "arm": true, "confirm": "SELL LIVE"} saves the config
// and arms; {"arm": false} disarms (always allowed).
func (h *Handlers) LUTBuildHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(lutIST())
	reply := func(status int, extra map[string]interface{}) {
		c := loadLUTBuildConfig()
		lutEng.mu.Lock()
		var run *LUTBuildRun
		if lutEng.build != nil {
			cp := *lutEng.build
			run = &cp
		}
		taken := lutEng.entry != nil
		tests := make([]LUTBuildRun, 0, len(lutEng.tests))
		for _, t := range lutEng.tests {
			tests = append(tests, *t)
		}
		lutEng.mu.Unlock()
		out := map[string]interface{}{"success": status == http.StatusOK, "config": c, "today": run, "lut_yes_taken_today": taken,
			"not_ready": h.Service.lutBuildChecks(c, now), "warnings": h.Service.lutBuildWarnings(), "confirm_text": lutBuildConfirm, "tests": tests}
		for k, v := range extra {
			out[k] = v
		}
		lutJSON(w, status, out)
	}
	if r.Method == http.MethodGet {
		reply(http.StatusOK, nil)
		return
	}
	if r.Method != http.MethodPost {
		reply(http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
		return
	}
	var b struct {
		LUTBuildConfig
		Arm     *bool  `json:"arm"`
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		reply(http.StatusBadRequest, map[string]interface{}{"error": "bad body: " + err.Error()})
		return
	}
	c := lutBuildDefaults(b.LUTBuildConfig)
	prev := loadLUTBuildConfig()
	c.Armed, c.ArmedAt = prev.Armed, prev.ArmedAt
	if b.Arm != nil {
		if *b.Arm {
			if strings.TrimSpace(b.Confirm) != lutBuildConfirm {
				reply(http.StatusBadRequest, map[string]interface{}{"error": fmt.Sprintf("arming the REAL build needs the confirmation text %q", lutBuildConfirm)})
				return
			}
			// Configuration problems are refused NOW, at arming -- never
			// discovered at the first YES.
			chk := c
			chk.Armed = true
			if why := h.Service.lutBuildChecks(chk, now); len(why) > 0 {
				reply(http.StatusBadRequest, map[string]interface{}{"error": "cannot arm: " + strings.Join(why, "; ")})
				return
			}
			c.Armed, c.ArmedAt = true, now.Format("2006-01-02 15:04:05")
		} else {
			c.Armed, c.ArmedAt = false, ""
		}
	} else if prev.Armed && c != prev {
		// Changing a live-armed config needs the confirmation again.
		if strings.TrimSpace(b.Confirm) != lutBuildConfirm {
			reply(http.StatusBadRequest, map[string]interface{}{"error": fmt.Sprintf("the build is ARMED: changing it needs the confirmation text %q (or disarm first)", lutBuildConfirm)})
			return
		}
	}
	if err := saveLUTBuildConfig(c); err != nil {
		reply(http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	log.Printf("[LUT-BUILD] config saved: armed=%v lots=%d account=%s/%s exit=%s SL %.2f TP %.2f (0 = LUT's own)",
		c.Armed, c.Lots, c.BrokerName, c.AccountID, c.ExitTime, c.SLBps, c.TPBps)
	reply(http.StatusOK, nil)
}
