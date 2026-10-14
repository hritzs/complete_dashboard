package trading

// Two table sets on the same day. One set drives the LUT (its minutes go to
// <day>.jsonl, its first YES is the paper entry and fires the armed real
// build); the other is run beside it on exactly the same prices, inputs,
// opening gap and DTE, shown on the tab and recorded in its own files:
//   <day>_lut_<key>.jsonl / .csv   its minutes
//   <day>_lut_<key>_day.json       its first YES (paper entry)
// The second set never fires a build.
//
// Which set drives is fixed per day: the day's state file remembers it, so
// a switch (lut_logic file, /api/lut/logic) takes effect the next day and a
// restart never mixes two sets in one day's record.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type lutAlt struct {
	sc         *lutSchema
	set        LUTSet
	loaded     bool
	err        string
	tried      time.Time
	minutes    []LUTEvaluation
	entry      *LUTEntry
	lastMinute int
	backfilled bool // minutes recorded by the driving set before this one ran
	levelsDay  string
	levelsDone []LUTSellLevel
}

// LUTAltLive is the second set's part of the tab payload.
type LUTAltLive struct {
	Tables        string         `json:"tables"`
	Name          string         `json:"name"`
	TablesError   string         `json:"tables_error,omitempty"`
	TableYes      [5]int         `json:"table_yes"`
	Eval          *LUTEvaluation `json:"eval,omitempty"`
	Grid          *LUTGrid       `json:"grid,omitempty"`
	Grid0920      *LUTGrid       `json:"grid_0920,omitempty"`
	SellRange     *LUTSellRange  `json:"sell_range,omitempty"`
	SellRange0920 *LUTSellRange  `json:"sell_range_0920,omitempty"`
	SellLevels    []LUTSellLevel `json:"sell_levels,omitempty"`
	Entry         *LUTEntry      `json:"entry,omitempty"`
	MinutesCount  int            `json:"minutes_count"`
}

func lutAltStem(day, key string) string { return day + "_lut_" + key }

func lutLogicFile() string { return filepath.Join(lutDataDir(), "lut_logic") }

// lutLogicChoice: the set chosen to drive the LUT from the next day on
// ("old" until switched).
func lutLogicChoice() string {
	if b, err := os.ReadFile(lutLogicFile()); err == nil {
		if k := strings.TrimSpace(string(b)); lutSchemaByKey(k) != nil {
			return k
		}
	}
	if k := strings.TrimSpace(os.Getenv("LUT_LOGIC")); lutSchemaByKey(k) != nil {
		return k
	}
	return "new" // the new tables drive unless chosen otherwise (owner, 2026-10-14)
}

// lutShowOld: "Show old algorithm" -- the old set runs beside the new one
// only while this is on (off by default). File lut_show_old in the data
// dir; read once, then kept in memory.
var (
	lutShowOldOnce sync.Once
	lutShowOldOn   atomic.Bool
)

func lutShowOldFile() string { return filepath.Join(lutDataDir(), "lut_show_old") }

func lutShowOld() bool {
	lutShowOldOnce.Do(func() {
		b, _ := os.ReadFile(lutShowOldFile())
		lutShowOldOn.Store(strings.TrimSpace(string(b)) == "1")
	})
	return lutShowOldOn.Load()
}

func lutSetShowOld(on bool) error {
	lutShowOld()
	lutShowOldOn.Store(on)
	v := "0"
	if on {
		v = "1"
	}
	if err := os.MkdirAll(lutDataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(lutShowOldFile(), []byte(v+"\n"), 0o644)
}

// lutAltWanted: the set beside the driving one runs at all. The new set
// always does (it is the one being adopted); the old set only when shown.
func lutAltWanted(alt *lutSchema) bool {
	return alt == lutSchemaNew || lutShowOld()
}

// lutSwapDriving makes `to` the set driving a day already recorded under
// `from`: its files become the day's main files and `from`'s move to its
// own names. Files are renamed, never deleted; the day's builds, tests and
// 09:16 capture stay in the main day state.
func lutSwapDriving(day string, from, to *lutSchema) {
	dir := lutDataDir()
	fromAlt, toAlt := lutAltStem(day, from.Key), lutAltStem(day, to.Key)
	mv := func(src, dst string) {
		if _, err := os.Stat(src); err != nil {
			return
		}
		if _, err := os.Stat(dst); err == nil { // never overwrite: keep the older copy aside
			_ = os.Rename(dst, dst+".bak-"+time.Now().Format("150405"))
		}
		if err := os.Rename(src, dst); err != nil {
			log.Printf("[LUT] ⚠ switching %s to %s: %v", day, to.Name, err)
		}
	}
	_, prim := lutLoadDay(day)
	_, toSt := lutLoadDayFrom(toAlt, day)
	for _, ext := range []string{".jsonl", ".csv"} {
		mv(filepath.Join(dir, day+ext), filepath.Join(dir, fromAlt+ext))
		mv(filepath.Join(dir, toAlt+ext), filepath.Join(dir, day+ext))
	}
	lutSaveDayTo(fromAlt, lutDayState{Day: day, Entry: prim.Entry, Tables: from.Key})
	prim.Day, prim.Entry, prim.Tables = day, toSt.Entry, to.Key
	lutSaveDay(prim)
	_ = os.Rename(filepath.Join(dir, toAlt+"_day.json"), filepath.Join(dir, toAlt+"_day.json.bak-"+time.Now().Format("150405")))
	log.Printf("[LUT] %s: %s now drive the day (their minutes are the main record; %s kept as %s.*)", day, to.Name, from.Name, fromAlt)
}

// lutOther is the set that is not sc.
func lutOther(sc *lutSchema) *lutSchema {
	if sc == lutSchemaNew {
		return lutSchemaOld
	}
	return lutSchemaNew
}

// lutTablesDir: LUT_DIR_OLD / LUT_DIR_NEW, else the schema's folder. The
// old single LUT_DIR still applies to the old set.
func lutTablesDir(sc *lutSchema) string {
	if d := strings.TrimSpace(os.Getenv("LUT_DIR_" + strings.ToUpper(sc.Key))); d != "" {
		return d
	}
	if sc == lutSchemaOld {
		return lutEnv("LUT_DIR", sc.DefaultDir)
	}
	return sc.DefaultDir
}

// loadAltLocked (re)starts the second set for the day. Caller holds e.mu.
func (e *lutEngine) loadAltLocked(day string) {
	if !lutAltWanted(lutOther(e.sc)) {
		e.alt = nil // the old set is not shown: nothing loaded, evaluated or recorded
		return
	}
	a := &lutAlt{sc: lutOther(e.sc), tried: time.Now()}
	e.alt = a
	mins, st := lutLoadDayFrom(lutAltStem(day, a.sc.Key), day)
	a.minutes, a.entry = mins, st.Entry
	if n := len(mins); n > 0 {
		a.lastMinute = lutHHMM(mins[n-1].Time)
	}
	set, err := LoadLUTSet(lutTablesDir(a.sc), a.sc)
	if err != nil {
		a.err = err.Error()
		log.Printf("[LUT] %s not loaded (shown beside): %v", a.sc.Name, err)
		return
	}
	a.set, a.loaded = set, true
	log.Printf("[LUT] %s: %s loaded beside (display / record only), YES cells %d/%d/%d/%d/%d, %d minute(s) already recorded",
		day, a.sc.Name, set[0].Yes, set[1].Yes, set[2].Yes, set[3].Yes, set[4].Yes, len(mins))
}

// recordAltLocked evaluates a just-recorded minute against the second set
// and records it. Caller holds e.mu.
func (e *lutEngine) recordAltLocked(day string, rc lutCtx, closed *OptionChainSnapshot, lots int) {
	a := e.alt
	if a == nil || !a.loaded || rc.Ev == nil {
		return
	}
	ac := rc.with(a.set)
	rec := *ac.Ev
	rec.Time, rec.Preview = fmt.Sprintf("%02d:%02d", rc.EvalHHMM/100, rc.EvalHHMM%100), false
	rec.NearestYes, rec.NearestYes0920 = nil, nil
	if lutHHMM(rec.Time) <= a.lastMinute {
		return
	}
	e.appendAltLocked(day, rec)
	if rec.Allowed && a.entry == nil {
		a.entry = lutMakeEntry(rec, closed, rc.Row, lots, e.lotSize)
		a.entry.Source = "LUT YES (" + a.sc.Name + ", shown beside -- never executed)"
		lutSaveDayTo(lutAltStem(day, a.sc.Key), lutDayState{Day: day, Entry: a.entry, Tables: a.sc.Key})
		log.Printf("[LUT] %s FIRST YES (%s, never executed) %s strike %.0f straddle %.2f coord %s",
			day, a.sc.Name, rec.Time, rec.Strike, rec.Straddle, rec.CoordText)
	}
}

func (e *lutEngine) appendAltLocked(day string, rec LUTEvaluation) {
	a := e.alt
	a.minutes = append(a.minutes, rec)
	a.lastMinute = lutHHMM(rec.Time)
	lutSaveMinuteTo(lutAltStem(day, a.sc.Key), day, rec)
}

// backfillAltLocked: minutes the driving set recorded before the second set
// ran (gateway started later, or before this code existed) are evaluated
// from their recorded prices. Caller holds e.mu.
func (e *lutEngine) backfillAltLocked(day string) {
	a := e.alt
	if a == nil || !a.loaded || a.backfilled || !e.inputsOK {
		return
	}
	a.backfilled = true
	have := map[string]bool{}
	for _, m := range a.minutes {
		have[m.Time] = true
	}
	n := 0
	for _, r := range e.minutes {
		hm := lutHHMM(r.Time)
		if have[r.Time] || hm <= a.lastMinute || r.Underlying <= 0 || r.RawDTE <= 0 || r.CELTP <= 0 || r.PELTP <= 0 {
			continue
		}
		bus := lutCeil(r.RawDTE) // trading DTE below raw = a weekend in the window
		if r.TradingDTE > 0 && r.TradingDTE < r.RawDTE {
			bus--
		}
		m := LUTMinuteInput{HHMM: hm, Underlying: r.Underlying, RawDTE: r.RawDTE, BusDays: bus,
			CELTP: r.CELTP, PELTP: r.PELTP, PrevIV: r.BuildIV}
		ev := EvaluateLUTMinute(a.set, e.inputs, r.NormOG, e.isEvent && a.sc.UseEvents, m)
		ev.Time = r.Time
		ev.NearestYes, ev.NearestYes0920 = nil, nil
		e.appendAltLocked(day, ev)
		n++
		if ev.Allowed && a.entry == nil {
			a.entry = &LUTEntry{Time: ev.Time, Stage: ev.Stage, Strike: ev.Strike, Underlying: ev.Underlying, Evaluation: ev,
				Source: "LUT YES (" + a.sc.Name + ", from the recorded minute -- never executed)"}
			lutSaveDayTo(lutAltStem(day, a.sc.Key), lutDayState{Day: day, Entry: a.entry, Tables: a.sc.Key})
		}
	}
	if n > 0 {
		log.Printf("[LUT] %s: %s -- %d earlier minute(s) evaluated from their recorded prices", day, a.sc.Name, n)
	}
}

func lutCeil(v float64) int {
	n := int(v)
	if float64(n) < v {
		n++
	}
	return n
}

// altLiveLocked builds the second set's payload from the shown evaluation.
// Caller holds e.mu.
func (e *lutEngine) altLiveLocked(now time.Time, hhmm int, shown lutCtx, withGrid bool, expStr string, levelsBasis string, whatIf, fixedDTE bool) *LUTAltLive {
	a := e.alt
	if a == nil {
		return nil
	}
	out := &LUTAltLive{Tables: a.sc.Key, Name: a.sc.Name, TablesError: a.err, Entry: a.entry, MinutesCount: len(a.minutes)}
	if !a.loaded {
		return out
	}
	for i, t := range a.set {
		out.TableYes[i] = t.Yes
	}
	if shown.Ev == nil {
		return out
	}
	ac := shown.with(a.set)
	ac.Ev.Preview = shown.Ev.Preview
	out.Eval = ac.Ev
	if withGrid && ac.Ev.CoordText != "" {
		stage := lutStage(ac.EvalHHMM)
		g := BuildLUTGrid(a.set[stage], lutStageNames[stage], *ac.Ev, ac.In)
		out.Grid = &g
		sr := lutScanSellRange(a.set, ac, ac.EvalHHMM)
		out.SellRange = &sr
		if stage != 4 {
			g2 := BuildLUTGrid(a.set[4], lutStageNames[4], *ac.Ev, ac.In)
			out.Grid0920 = &g2
			sr2 := lutScanSellRange(a.set, ac, 920)
			out.SellRange0920 = &sr2
		}
	}
	if withGrid && ac.M.Underlying > 0 {
		if hhmm < 930 || whatIf {
			out.SellLevels = lutSellLevelsFor(a.set, a.minutes, now, ac, expStr, fixedDTE, levelsBasis, false)
		} else {
			if a.levelsDay != e.day {
				a.levelsDay, a.levelsDone = e.day, lutSellLevelsFor(a.set, a.minutes, now, ac, expStr, fixedDTE, "", true)
			}
			out.SellLevels = a.levelsDone
		}
	}
	return out
}

// LUTLogicHandler: GET -> which set drives today and which is chosen from
// the next day; POST {"tables":"old"|"new"} chooses (from the next day).
func (h *Handlers) LUTLogicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Tables  string `json:"tables"`
			ShowOld *bool  `json:"show_old"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			lutJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.ShowOld != nil {
			if err := lutSetShowOld(*req.ShowOld); err != nil {
				lutJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			log.Printf("[LUT] show old algorithm: %v", *req.ShowOld)
		}
		if req.Tables == "" {
			lutLogicReply(w)
			return
		}
		if lutSchemaByKey(req.Tables) == nil {
			lutJSON(w, http.StatusBadRequest, map[string]string{"error": `tables must be "old" or "new"`})
			return
		}
		err := os.MkdirAll(lutDataDir(), 0o755)
		if err == nil {
			err = os.WriteFile(lutLogicFile(), []byte(req.Tables+"\n"), 0o644)
		}
		if err != nil {
			lutJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[LUT] table set chosen to drive the LUT: %s (applies now, today included)", req.Tables)
		lutEng.mu.Lock()
		if lutEng.day != "" && (lutEng.sc == nil || lutEng.sc.Key != req.Tables) {
			lutEng.newDay(lutEng.day) // swaps today's files and reloads (lutSwapDriving)
		}
		lutEng.mu.Unlock()
	}
	lutLogicReply(w)
}

func lutLogicReply(w http.ResponseWriter) {
	lutEng.mu.Lock()
	today := ""
	if lutEng.sc != nil {
		today = lutEng.sc.Key
	}
	lutEng.mu.Unlock()
	lutJSON(w, http.StatusOK, map[string]interface{}{"today": today, "chosen": lutLogicChoice(), "show_old": lutShowOld(),
		"note": "the chosen set drives from now on (a day already recorded under the other set has its files swapped); the old set runs beside it only while show_old is on"})
}

func lutAnswerOf(e LUTEvaluation) string {
	switch {
	case e.Skip != "":
		return "SKIP"
	case e.Allowed:
		return "YES"
	}
	return "NO"
}

// lutDaySets is a day's record of both table sets.
type lutDaySets struct {
	Tables, AltTables     string // driving set, the other set
	Minutes, AltMinutes   []LUTEvaluation
	FirstYes, AltFirstYes string // HH:MM ("" = none)
}

func lutFirstYes(mins []LUTEvaluation) string {
	for _, m := range mins {
		if m.Allowed && m.Skip == "" {
			return m.Time
		}
	}
	return ""
}

func lutLoadDaySets(day string) lutDaySets {
	mins, st := lutLoadDay(day)
	sc := lutSchemaByKey(st.Tables)
	if sc == nil {
		sc = lutSchemaOld // files from before two sets existed
	}
	out := lutDaySets{Tables: sc.Key, Minutes: mins, FirstYes: lutFirstYes(mins)}
	if alt := lutOther(sc); lutAltWanted(alt) { // the old set only when shown
		altMins, _ := lutLoadDayFrom(lutAltStem(day, alt.Key), day)
		out.AltTables, out.AltMinutes, out.AltFirstYes = alt.Key, altMins, lutFirstYes(altMins)
	}
	return out
}
