package trading

// Special session (Diwali Muhurat): the LUT sells exactly as on a normal day
// -- only the session's open and close differ (special_session.go). Nothing
// here runs on a date that is not listed.
//
//   - Each session minute the LUT engine's minute record (GreekSoft closes,
//     lut_sim.go) is evaluated on the session clock: the first minute after
//     the open is the 09:16 table, the next 09:17, ... (the whole session
//     lies before the 13:30 scan end). The opening gap is the first session
//     minute's future against the previous trading day's. DTE is the normal
//     calendar DTE plus only the session's remaining trading minutes
//     (lutRawDTE); the listed date counts as a trading day.
//   - A YES there IS the day's LUT YES: the paper entry and, when armed on
//     the LUT tab, the normal LUT build (lutFireBuildLocked: sim-driven,
//     once per day, self-disarming), on the real minute. Every timed exit
//     that day is capped muhuratExitBefore ahead of the close
//     (sessionExitCap).
//   - The Muhurat tab lists the session and shows the clock, DTE, greeks and
//     each session minute's check (<day>_muhurat.json).

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

// MuhuratRecord is one session minute's LUT check.
type MuhuratRecord struct {
	Time  string        `json:"time"`  // the real minute, HH:MM
	Clock string        `json:"clock"` // the session clock it is checked as (09:16 ...)
	Eval  LUTEvaluation `json:"eval"`
}

// MuhuratDay is the session day's record, saved as <day>_muhurat.json.
type MuhuratDay struct {
	Day     string          `json:"day"`
	U0916   float64         `json:"u0916"` // the first session minute's future (opening gap)
	U0916At string          `json:"u0916_at,omitempty"`
	Minutes []MuhuratRecord `json:"minutes"`
}

var muh struct {
	sync.Mutex
	d MuhuratDay
}

func muhuratDayFile(day string) string {
	return filepath.Join(lutDataDir(), day+"_muhurat.json")
}

func loadMuhuratDay(day string) MuhuratDay {
	d := MuhuratDay{Day: day}
	if b, err := stateRead(muhuratDayFile(day)); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	d.Day = day
	return d
}

func saveMuhuratDayLocked() {
	b, _ := json.MarshalIndent(muh.d, "", "  ")
	if err := os.MkdirAll(lutDataDir(), 0o755); err == nil {
		if err = sbWriteAtomic(muhuratDayFile(muh.d.Day), b); err != nil {
			log.Printf("[MUHURAT] ⚠ day record not saved: %v", err)
		}
	}
}

// muhuratRecordLocked runs inside the LUT engine's minute record (caller
// holds lutEng.mu) with that minute's closed chain.
func (s *Service) muhuratRecordLocked(day string, now time.Time, closed *OptionChainSnapshot, cfg LUTConfig) {
	clock, ok := sessionHHMM(now)
	if !ok || clock < 916 || clock > lutLastScan || closed == nil {
		return
	}
	e := lutEng
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
	if mcfg.Underlying0916 <= 0 {
		mcfg.Underlying0916 = muh.d.U0916
	}
	rc := e.evaluate(now, clock, closed, mcfg, LUTManual{})
	if rc.Ev == nil || rc.Preview {
		return
	}
	rec := *rc.Ev
	rec.Preview, rec.NearestYes, rec.NearestYes0920 = false, nil, nil
	clk := fmt.Sprintf("%02d:%02d", clock/100, clock%100)
	rec.Time = minute // the real minute: the build and its simulation run on the recorded minutes
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
	if !rec.Allowed {
		return
	}
	// The day's LUT YES, as on a normal day: the real build when the LUT tab
	// is armed (once per day), and the paper entry.
	s.lutFireBuildLocked(day, rec, lutMakeEntry(rec, closed, rc.Row, cfg.Lots, e.lotSize))
	if e.entry == nil {
		e.entry = lutMakeEntry(rec, closed, rc.Row, cfg.Lots, e.lotSize)
		e.entry.Source = "LUT YES (session clock " + clk + ")"
		d := e.entry
		log.Printf("[LUT] ✅ FIRST YES (special session, table %s) -- PAPER ENTRY %s NIFTY %s %.0f SELL CE %d + SELL PE %d fut %.2f adj build IV %.4f coord %s",
			rec.Stage, d.Time, d.Expiry, d.Strike, d.Qty, d.Qty, d.Underlying, rec.AdjBuildIV, rec.CoordText)
		e.saveDayLocked(day)
	}
}

// MuhuratStatus is the tab's payload.
type MuhuratStatus struct {
	Success  bool             `json:"success"`
	Error    string           `json:"error,omitempty"`
	Now      string           `json:"now"`
	Sessions []SpecialSession `json:"sessions"`
	Today    *SpecialSession  `json:"today,omitempty"`
	Next     *SpecialSession  `json:"next,omitempty"`
	Phase    string           `json:"phase"`
	Clock    string           `json:"clock,omitempty"` // the LUT clock now (09:16 ...)
	ExitTime string           `json:"exit_time,omitempty"`
	LUTArmed bool             `json:"lut_armed"` // the LUT tab's real build
	Build    *LUTBuildRun     `json:"build,omitempty"`
	Day      *MuhuratDay      `json:"day,omitempty"`
	Live     *MuhuratLive     `json:"live,omitempty"`
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
		LUTArmed: loadLUTBuildConfig().Armed}
	today := now.In(lutIST()).Format("2006-01-02")
	for i := range st.Sessions {
		ss := st.Sessions[i]
		if ss.Date == today {
			st.Today = &ss
		} else if ss.Date > today && st.Next == nil {
			st.Next = &ss
		}
	}
	if st.Today == nil {
		st.Phase = "no special session today -- the normal 09:15-15:40 clock"
		if st.Next != nil {
			st.Phase = fmt.Sprintf("next special session %s %s %s-%s", st.Next.Name, st.Next.Date, st.Next.Open, st.Next.Close)
		}
		return st
	}
	_, open, close, _ := specialSessionOn(now)
	st.ExitTime = close.Add(-muhuratExitBefore).Format("15:04:05")
	switch clock, ok := sessionHHMM(now); {
	case now.Before(open):
		st.Phase = fmt.Sprintf("before the open (%s) -- the first check is %s as the 09:16 table", st.Today.Open, open.Add(time.Minute).Format("15:04"))
	case ok:
		st.Clock = fmt.Sprintf("%02d:%02d", clock/100, clock%100)
		st.Phase = "session live -- each minute checked on the session clock"
	default:
		st.Phase = "session over"
	}
	lutEng.mu.Lock()
	if b := lutEng.build; b != nil && lutEng.day == today {
		bc := *b
		st.Build = &bc
	}
	lutEng.mu.Unlock()
	muh.Lock()
	if muh.d.Day != today {
		muh.d = loadMuhuratDay(today)
	}
	d := muh.d
	d.Minutes = append([]MuhuratRecord(nil), muh.d.Minutes...)
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
// session_save {session}, session_delete {date}.
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
	default:
		fail("unknown action " + body.Action)
		return
	}
	lutJSON(w, http.StatusOK, s.muhuratStatus(now))
}
