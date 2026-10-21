package trading

// Muhurat tab: the LUT on a special session's own clock (special_session.go)
// and a one-shot armed build. Nothing here runs on a date that is not listed.
//
//   - Each minute of the session the LUT engine's minute record (GreekSoft
//     closes, lut_sim.go) is also evaluated on the session clock: the first
//     minute after the open is the 09:16 table, the next 09:17, ... (the
//     whole session lies before the 13:30 scan end). The opening gap is the
//     first session minute's future against the previous trading day's.
//     DTE is the normal calendar DTE plus only the session's remaining
//     trading minutes (lutRawDTE); the listed date counts as a trading day.
//   - Armed (typed confirmation), the first YES fires ONE real build -- the
//     LUT build's request (delta-neutral, the strike the LUT evaluated) with
//     the exit muhuratExitBefore ahead of the close; the trade is then run
//     by the standard monitor (hedge / SL / TP / exit). It disarms itself
//     when it fires and records the build before any order, so it never
//     fires twice.
//   - Its own files: muhurat_build.json (settings) and <day>_muhurat.json.

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

const muhuratConfirm = "SELL MUHURAT"

// MuhuratRecord is one session minute's LUT check.
type MuhuratRecord struct {
	Time  string        `json:"time"`  // the real minute, HH:MM
	Clock string        `json:"clock"` // the session clock it is checked as (09:16 ...)
	Eval  LUTEvaluation `json:"eval"`
}

// MuhuratDay is the session day's state, saved as <day>_muhurat.json.
type MuhuratDay struct {
	Day     string          `json:"day"`
	U0916   float64         `json:"u0916"` // the first session minute's future (opening gap)
	U0916At string          `json:"u0916_at,omitempty"`
	Minutes []MuhuratRecord `json:"minutes"`
	Build   *LUTBuildRun    `json:"build,omitempty"`
}

var muh struct {
	sync.Mutex
	d MuhuratDay
}

func muhuratBuildFile() string { return filepath.Join(lutDataDir(), "muhurat_build.json") }
func muhuratDayFile(day string) string {
	return filepath.Join(lutDataDir(), day+"_muhurat.json")
}

func loadMuhuratBuildConfig() LUTBuildConfig {
	var c LUTBuildConfig
	if b, err := stateRead(muhuratBuildFile()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return lutBuildDefaults(c)
}

func saveMuhuratBuildConfig(c LUTBuildConfig) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(lutDataDir(), 0o755); err != nil {
		return err
	}
	return sbWriteAtomic(muhuratBuildFile(), b)
}

func loadMuhuratDay(day string) MuhuratDay {
	d := MuhuratDay{Day: day}
	if b, err := stateRead(muhuratDayFile(day)); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	d.Day = day
	if d.Build != nil && d.Build.Status == "FIRING" { // a restart while it was being sent
		d.Build.Status = "UNKNOWN"
	}
	return d
}

func saveMuhuratDayLocked() {
	b, _ := json.MarshalIndent(muh.d, "", "  ")
	if err := os.MkdirAll(lutDataDir(), 0o755); err == nil {
		if err = sbWriteAtomic(muhuratDayFile(muh.d.Day), b); err != nil {
			log.Printf("[MUHURAT] ⚠ day state not saved: %v", err)
		}
	}
}

// muhuratExitTime: the timed exit of a build fired during the session.
func muhuratExitTime(close time.Time) string {
	return close.Add(-muhuratExitBefore).Format("15:04:05")
}

// muhuratRecordLocked runs inside the LUT engine's minute record (caller
// holds lutEng.mu) with that minute's closed chain.
func (s *Service) muhuratRecordLocked(now time.Time, closed *OptionChainSnapshot, cfg LUTConfig) {
	clock, ok := sessionHHMM(now)
	if !ok || clock < 916 || clock > lutLastScan || closed == nil {
		return
	}
	e := lutEng
	day := now.In(lutIST()).Format("2006-01-02")
	minute := now.In(lutIST()).Format("15:04")
	muh.Lock()
	defer muh.Unlock()
	if muh.d.Day != day {
		muh.d = loadMuhuratDay(day)
	}
	for _, r := range muh.d.Minutes {
		if r.Time == minute {
			return // this minute is recorded already
		}
	}
	if muh.d.U0916 <= 0 {
		if S := lutUnderlying(closed); S > 0 {
			muh.d.U0916, muh.d.U0916At = S, now.In(lutIST()).Format("15:04:05")
		}
	}
	mcfg := cfg
	mcfg.Underlying0916 = muh.d.U0916
	rc := e.evaluate(now, clock, closed, mcfg, LUTManual{})
	if rc.Ev == nil || rc.Preview {
		return
	}
	rec := *rc.Ev
	rec.Preview, rec.NearestYes, rec.NearestYes0920 = false, nil, nil
	clk := fmt.Sprintf("%02d:%02d", clock/100, clock%100)
	rec.Time = clk
	muh.d.Minutes = append(muh.d.Minutes, MuhuratRecord{Time: minute, Clock: clk, Eval: rec})
	saveMuhuratDayLocked()
	verdict := "NO"
	if rec.Skip != "" {
		verdict = "SKIP (" + rec.Skip + ")"
	} else if rec.Allowed {
		verdict = "YES"
	}
	log.Printf("[MUHURAT] %s = table %s fut=%.2f atm=%.0f ce=%.2f pe=%.2f straddle=%.2f dte=%.3f/%.3f adj_iv=%.4f iv_ratio=%.4f str_ratio=%.4f coord=%s -> %s",
		minute, rec.Stage, rec.Underlying, rec.Strike, rec.CELTP, rec.PELTP, rec.Straddle, rec.RawDTE, rec.TradingDTE,
		rec.AdjBuildIV, rec.IVRatio, rec.StrRatio, rec.CoordText, verdict)
	if rec.Allowed && muh.d.Build == nil {
		s.muhuratFireLocked(now, minute, rec, closed, rc.Row)
	}
}

// muhuratFireLocked: the session's first YES while armed (caller holds
// muh and lutEng.mu). Recorded before any order; disarms itself.
func (s *Service) muhuratFireLocked(now time.Time, minute string, rec LUTEvaluation, closed *OptionChainSnapshot, row *OptionChainRow) {
	c := loadMuhuratBuildConfig()
	if !c.Armed {
		return
	}
	_, _, close, ok := specialSessionOn(now)
	if !ok {
		return
	}
	e := lutEng
	if e.isEvent && e.sc != nil && e.sc.UseEvents {
		c.Lots = max(1, int(float64(c.Lots)*lutEventSize))
	}
	c.ExitTime = muhuratExitTime(close)
	entry := lutMakeEntry(rec, closed, row, c.Lots, e.lotSize)
	run := &LUTBuildRun{Minute: minute + " (table " + rec.Time + ")", FiredAt: now.In(lutIST()).Format("15:04:05.000"),
		Expiry: entry.Expiry, Strike: entry.Strike, Lots: c.Lots, ExitTime: c.ExitTime, Account: c.BrokerName + "/" + c.AccountID}
	risk := lutBuildRisk(c, rec)
	run.SLBps, run.TPBps = risk.SlBps, risk.TpBps
	switch {
	case strings.TrimSpace(c.BrokerName) == "" || strings.TrimSpace(c.AccountID) == "":
		run.Status, run.Error = "REFUSED", "broker / account not set"
	case entry.Expiry == "" || entry.Strike <= 0:
		run.Status, run.Error = "REFUSED", "the YES has no expiry / strike"
	default:
		if err := risk.Validate(now); err != nil {
			run.Status, run.Error = "REFUSED", err.Error()
		} else {
			run.Status = "FIRING"
		}
	}
	muh.d.Build = run
	saveMuhuratDayLocked() // recorded before any order: never fires twice
	if run.Status != "FIRING" {
		log.Printf("[MUHURAT-BUILD] ⛔ YES at %s but the REAL build was REFUSED: %s", run.Minute, run.Error)
		return
	}
	log.Printf("[MUHURAT-BUILD] 🚀 YES at %s -> REAL BUILD %s %s %.0f straddle %d lot(s), delta-neutral, SL %.2f bps TP %.2f bps exit %s, %s, account %s",
		run.Minute, lutSymbol, entry.Expiry, entry.Strike, c.Lots, risk.SlBps, risk.TpBps, c.ExitTime, lutWingsText(c.WingPct), run.Account)
	req := lutBuildRequest(c, entry.Expiry, entry.Strike, risk)
	req.Mode = BuildModeMuhurat
	c.Armed, c.ArmedAt = false, ""
	if err := saveMuhuratBuildConfig(c); err != nil {
		log.Printf("[MUHURAT-BUILD] ⚠ could not save the disarm: %v (the day record still blocks another build)", err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		resp, err := s.ExecuteFinalBuild(ctx, req)
		muh.Lock()
		defer muh.Unlock()
		run.FinishedAt = time.Now().In(lutIST()).Format("15:04:05.000")
		if err != nil {
			run.Status, run.Error = "FAILED", err.Error()
			if resp != nil {
				run.TradeUID = resp.TradeUID
			}
			log.Printf("[MUHURAT-BUILD] ❌ build failed: %v (trade %s) -- check Portfolio / broker", err, run.TradeUID)
		} else {
			run.Status, run.TradeUID = "BUILT", resp.TradeUID
			run.Message = strings.TrimSpace(resp.Status + " " + resp.Message)
			log.Printf("[MUHURAT-BUILD] ✅ build done: trade %s CE %d / PE %d status %s", resp.TradeUID, resp.CEQty, resp.PEQty, resp.Status)
		}
		saveMuhuratDayLocked()
	}()
}

// MuhuratStatus is the tab's payload.
type MuhuratStatus struct {
	Success     bool             `json:"success"`
	Error       string           `json:"error,omitempty"`
	Now         string           `json:"now"`
	Sessions    []SpecialSession `json:"sessions"`
	Today       *SpecialSession  `json:"today,omitempty"`
	Next        *SpecialSession  `json:"next,omitempty"`
	Phase       string           `json:"phase"`
	Clock       string           `json:"clock,omitempty"` // the LUT clock now (09:16 ...)
	ExitTime    string           `json:"exit_time,omitempty"`
	Config      LUTBuildConfig   `json:"config"`
	Checks      []string         `json:"checks"`
	ConfirmText string           `json:"confirm_text"`
	Day         *MuhuratDay      `json:"day,omitempty"`
	Live        *MuhuratLive     `json:"live,omitempty"`
}

// MuhuratLive: the ATM now, with time to expiry and greeks on the session
// clock (one IV per strike from the OTM leg, as the backtest).
type MuhuratLive struct {
	Expiry   string  `json:"expiry"`
	Future   float64 `json:"future"`
	ATM      float64 `json:"atm"`
	CE       float64 `json:"ce"`
	PE       float64 `json:"pe"`
	Straddle float64 `json:"straddle"`
	DTE      float64 `json:"dte"`        // calendar days + session trading minutes
	DTENorm  float64 `json:"dte_normal"` // what a normal 09:15-15:40 day would give
	IV       float64 `json:"iv"`
	CEDelta  float64 `json:"ce_delta"`
	PEDelta  float64 `json:"pe_delta"`
	Gamma    float64 `json:"gamma"` // per option
}

func (s *Service) muhuratStatus(now time.Time) MuhuratStatus {
	st := MuhuratStatus{Success: true, Now: now.In(lutIST()).Format("2006-01-02 15:04:05"), Sessions: specialSessionList(),
		ConfirmText: muhuratConfirm, Config: loadMuhuratBuildConfig()}
	today := now.In(lutIST()).Format("2006-01-02")
	for i := range st.Sessions {
		ss := st.Sessions[i]
		if ss.Date == today {
			st.Today = &ss
		} else if ss.Date > today && st.Next == nil {
			st.Next = &ss
		}
	}
	if !st.Config.Armed {
		st.Checks = append(st.Checks, "not armed")
	}
	if strings.TrimSpace(st.Config.BrokerName) == "" || strings.TrimSpace(st.Config.AccountID) == "" {
		st.Checks = append(st.Checks, "broker / account not set")
	}
	if st.Today == nil {
		st.Phase = "no special session today -- nothing runs"
		if st.Next != nil {
			st.Phase = fmt.Sprintf("next session %s %s %s-%s", st.Next.Name, st.Next.Date, st.Next.Open, st.Next.Close)
		}
		return st
	}
	_, open, close, _ := specialSessionOn(now)
	st.ExitTime = muhuratExitTime(close)
	switch clock, ok := sessionHHMM(now); {
	case now.Before(open):
		st.Phase = fmt.Sprintf("before the open (%s) -- the first check is %s as the 09:16 table", st.Today.Open, open.Add(time.Minute).Format("15:04"))
	case ok:
		st.Clock = fmt.Sprintf("%02d:%02d", clock/100, clock%100)
		st.Phase = "session live -- each minute checked on the session clock"
	default:
		st.Phase = "session over"
	}
	muh.Lock()
	if muh.d.Day != today {
		muh.d = loadMuhuratDay(today)
	}
	d := muh.d
	d.Minutes = append([]MuhuratRecord(nil), muh.d.Minutes...)
	if muh.d.Build != nil {
		b := *muh.d.Build
		d.Build = &b
	}
	muh.Unlock()
	st.Day = &d
	st.Live = s.muhuratLive(now)
	return st
}

func (s *Service) muhuratLive(now time.Time) *MuhuratLive {
	if s.Snapshot == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	c, err := s.Snapshot.GetOptionChain(ctx, lutSymbol, "")
	cancel()
	if err != nil || c == nil {
		return nil
	}
	F := lutUnderlying(c)
	exp, perr := lutParseExpiry(c.Expiry)
	if F <= 0 || perr != nil {
		return nil
	}
	l := &MuhuratLive{Expiry: c.Expiry, Future: F, ATM: math.Round(F/lutStrikeStep) * lutStrikeStep, DTE: lutRawDTE(now, exp)}
	l.DTENorm = lutRawDTENormal(now, exp)
	if r, ferr := FindRowByStrike(*c, int(l.ATM)); ferr == nil && r != nil {
		l.CE, l.PE, l.Straddle = r.CELtp, r.PELtp, r.CELtp+r.PELtp
		T := math.Max(l.DTE/365.0, 1e-5)
		if iv := simStrikeIV(F, l.ATM, T, r.CELtp, r.PELtp); iv > 0 {
			l.IV = iv
			_, l.CEDelta, l.Gamma = simBlack76(true, F, l.ATM, T, iv)
			_, l.PEDelta, _ = simBlack76(false, F, l.ATM, T, iv)
		}
	}
	return l
}

// lutRawDTENormal: the DTE a normal 09:15-15:40 day would give (display).
func lutRawDTENormal(now, expiry time.Time) float64 {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	exp := time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 0, 0, 0, 0, loc)
	days := math.Round(exp.Sub(today).Hours() / 24)
	session := (6*time.Hour + 25*time.Minute).Seconds()
	remaining := today.Add(15*time.Hour + 40*time.Minute).Sub(now).Seconds()
	return days + math.Max(0, math.Min(remaining, session))/session
}

// MuhuratHandler: GET /api/muhurat (status); POST {action, ...}:
// session_save {session}, session_delete {date}, config {config},
// arm {confirm}, disarm.
func (h *Handlers) MuhuratHandler(w http.ResponseWriter, r *http.Request) {
	s := h.Service
	now := time.Now().In(lutIST())
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusOK, s.muhuratStatus(now))
		return
	}
	var body struct {
		Action  string         `json:"action"`
		Session SpecialSession `json:"session"`
		Date    string         `json:"date"`
		Config  LUTBuildConfig `json:"config"`
		Confirm string         `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		lutJSON(w, http.StatusBadRequest, MuhuratStatus{Error: "bad json: " + err.Error()})
		return
	}
	fail := func(msg string) {
		st := s.muhuratStatus(now)
		st.Success, st.Error = false, msg
		lutJSON(w, http.StatusOK, st)
	}
	switch body.Action {
	case "session_save":
		ss := body.Session
		ss.Name = strings.TrimSpace(ss.Name)
		if ss.Name == "" {
			ss.Name = "Diwali Muhurat"
		}
		if err := ss.validate(); err != nil {
			fail(err.Error())
			return
		}
		list := []SpecialSession{ss}
		for _, o := range specialSessionList() {
			if o.Date != ss.Date {
				list = append(list, o)
			}
		}
		if err := saveSpecialSessions(list); err != nil {
			fail(err.Error())
			return
		}
		log.Printf("[MUHURAT] session saved: %s %s pre-open %s, trading %s-%s", ss.Date, ss.Name, ss.PreOpen, ss.Open, ss.Close)
	case "session_delete":
		list := []SpecialSession{}
		for _, o := range specialSessionList() {
			if o.Date != body.Date {
				list = append(list, o)
			}
		}
		if err := saveSpecialSessions(list); err != nil {
			fail(err.Error())
			return
		}
		log.Printf("[MUHURAT] session %s removed", body.Date)
	case "config":
		c := loadMuhuratBuildConfig()
		n := body.Config
		armed, armedAt := c.Armed, c.ArmedAt
		c = lutBuildDefaults(n)
		c.Armed, c.ArmedAt = armed, armedAt
		if err := saveMuhuratBuildConfig(c); err != nil {
			fail(err.Error())
			return
		}
	case "arm":
		if strings.TrimSpace(body.Confirm) != muhuratConfirm {
			fail(fmt.Sprintf("type %q to arm", muhuratConfirm))
			return
		}
		c := loadMuhuratBuildConfig()
		if strings.TrimSpace(c.BrokerName) == "" || strings.TrimSpace(c.AccountID) == "" {
			fail("broker / account not set")
			return
		}
		if len(specialSessionList()) == 0 {
			fail("no special session listed -- save the session first")
			return
		}
		c.Armed, c.ArmedAt = true, now.Format("2006-01-02 15:04:05")
		if err := saveMuhuratBuildConfig(c); err != nil {
			fail(err.Error())
			return
		}
		log.Printf("[MUHURAT-BUILD] ARMED: the first YES of a listed session fires ONE real build of %d lot(s) on %s/%s", c.Lots, c.BrokerName, c.AccountID)
	case "disarm":
		c := loadMuhuratBuildConfig()
		c.Armed, c.ArmedAt = false, ""
		if err := saveMuhuratBuildConfig(c); err != nil {
			fail(err.Error())
			return
		}
		log.Printf("[MUHURAT-BUILD] disarmed")
	default:
		fail("unknown action " + body.Action)
		return
	}
	lutJSON(w, http.StatusOK, s.muhuratStatus(now))
}
