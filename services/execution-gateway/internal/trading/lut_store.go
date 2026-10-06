package trading

// Durable record of the LUT entry check, one set of files per day in
// LUT_DATA_DIR (default ~/.trading-platform/lut):
//   YYYY-MM-DD.jsonl  every recorded minute (full evaluation, reloaded at boot)
//   YYYY-MM-DD.csv    the same minutes, one row each, for spreadsheets
//   YYYY-MM-DD_day.json  captured 09:16 future + the paper entry
// A gateway restart mid-day reloads all of it, so the timeline, the first
// YES and the opening gap (captured 09:16 future) survive.

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func lutDataDir() string {
	if p := strings.TrimSpace(os.Getenv("LUT_DATA_DIR")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".trading-platform", "lut")
}

// lutDayState is what besides the minutes must survive a restart.
type lutDayState struct {
	Day      string    `json:"day"`
	U0916    float64   `json:"underlying_0916"`
	OGSource string    `json:"og_source"`
	Entry    *LUTEntry `json:"entry,omitempty"`
}

var lutCSVHeader = []string{
	"date", "time", "table", "answer", "skip", "future", "atm", "ce_ltp", "pe_ltp", "otm_leg", "otm_price",
	"dte_raw", "dte_trading", "adj_factor", "build_iv", "adj_build_iv", "bld_bucket", "iv_ratio", "iv_bucket",
	"straddle", "str_ratio", "str_bucket", "norm_og", "og_bucket", "adj_iv_chg", "adj_bucket", "coord", "tp_bps",
}

func lutCSVRow(day string, ev LUTEvaluation) []string {
	f := func(v float64, d int) string { return strconv.FormatFloat(v, 'f', d, 64) }
	answer := "NO"
	if ev.Allowed {
		answer = "YES"
	}
	if ev.Skip != "" {
		answer = "SKIP"
	}
	return []string{
		day, ev.Time, ev.Stage, answer, ev.Skip, f(ev.Underlying, 2), f(ev.Strike, 0), f(ev.CELTP, 2), f(ev.PELTP, 2), ev.OTMLeg, f(ev.OTMPrice, 2),
		f(ev.RawDTE, 4), f(ev.TradingDTE, 4), f(ev.AdjFactor, 4), f(ev.BuildIV, 4), f(ev.AdjBuildIV, 4), ev.BldLabel, f(ev.IVRatio, 4), ev.IVLabel,
		f(ev.Straddle, 2), f(ev.StrRatio, 4), ev.StrLabel, f(ev.NormOG, 4), ev.OGLabel, f(ev.AdjIVChg, 5), ev.AdjLabel, ev.CoordText, f(ev.TPBps, 0),
	}
}

// lutSaveMinute appends one recorded minute to the day's JSONL and CSV.
func lutSaveMinute(day string, ev LUTEvaluation) {
	dir := lutDataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[LUT] ⚠ cannot save minute %s: %v", ev.Time, err)
		return
	}
	b, err := json.Marshal(ev)
	if err == nil {
		err = lutAppend(filepath.Join(dir, day+".jsonl"), append(b, '\n'))
	}
	if err != nil {
		log.Printf("[LUT] ⚠ cannot save minute %s (jsonl): %v", ev.Time, err)
	}
	csvPath := filepath.Join(dir, day+".csv")
	_, statErr := os.Stat(csvPath)
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	if os.IsNotExist(statErr) {
		_ = w.Write(lutCSVHeader)
	}
	_ = w.Write(lutCSVRow(day, ev))
	w.Flush()
	if err := lutAppend(csvPath, []byte(sb.String())); err != nil {
		log.Printf("[LUT] ⚠ cannot save minute %s (csv): %v", ev.Time, err)
	}
}

func lutAppend(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// lutSaveDay writes the day state (09:16 capture + paper entry).
func lutSaveDay(st lutDayState) {
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := sbWriteAtomic(filepath.Join(lutDataDir(), st.Day+"_day.json"), b); err != nil {
		log.Printf("[LUT] ⚠ cannot save day state: %v", err)
	}
}

// lutLoadDay reloads a day's recorded minutes and state (missing = empty).
func lutLoadDay(day string) ([]LUTEvaluation, lutDayState) {
	st := lutDayState{Day: day}
	if b, err := os.ReadFile(filepath.Join(lutDataDir(), day+"_day.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	var mins []LUTEvaluation
	f, err := os.Open(filepath.Join(lutDataDir(), day+".jsonl"))
	if err != nil {
		return nil, st
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var ev LUTEvaluation
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Time != "" {
			mins = append(mins, ev)
		}
	}
	return mins, st
}

// lutHHMM parses "HH:MM" (0 if not).
func lutHHMM(s string) int {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0
	}
	return h*100 + m
}

// --- minute-end option chain (every strike), for the paper simulation (09:15-15:40) -----

// lutChainRow is one strike at a minute's first tick.
type lutChainRow struct {
	K       float64 `json:"k"`
	CEToken int64   `json:"ct,omitempty"`
	PEToken int64   `json:"pt,omitempty"`
	CE      float64 `json:"ce"`
	PE      float64 `json:"pe"`
	CEBid   float64 `json:"cb,omitempty"`
	CEAsk   float64 `json:"ca,omitempty"`
	PEBid   float64 `json:"pb,omitempty"`
	PEAsk   float64 `json:"pa,omitempty"`
	CEDelta float64 `json:"cd"`
	PEDelta float64 `json:"pd"`
	CEGamma float64 `json:"cg"`
	PEGamma float64 `json:"pg"`
	CEIV    float64 `json:"civ,omitempty"`
	PEIV    float64 `json:"piv,omitempty"`
	Src     string  `json:"src,omitempty"` // "" = recorded here; else where it was imported from
}

type lutChainMinute struct {
	Time   string        `json:"t"`            // HH:MM
	Ts     string        `json:"ts,omitempty"` // exact time recorded (HH:MM:SS.mmm)
	Future float64       `json:"f"`            // synthetic future
	ATM    float64       `json:"atm"`
	Expiry string        `json:"exp"`
	Src    string        `json:"src,omitempty"`
	Rows   []lutChainRow `json:"rows"`
}

// lutChainFile is the minute-chain file of a set: "" = current expiry,
// "next" = the next weekly expiry.
func lutChainFile(day, set string) string {
	if set == "" || set == "current" {
		return filepath.Join(lutDataDir(), day+"_chain.jsonl")
	}
	return filepath.Join(lutDataDir(), day+"_chain_"+set+".jsonl")
}

func lutChainImportFile(day, set string) string {
	if set == "" || set == "current" {
		return filepath.Join(lutDataDir(), day+"_chain_import.jsonl")
	}
	return filepath.Join(lutDataDir(), day+"_chain_"+set+"_import.jsonl")
}

// lutSaveChainMinute appends the whole current-expiry chain at this minute's first tick.
func lutSaveChainMinute(day string, hhmm int, chain *OptionChainSnapshot) {
	lutSaveChainMinuteTo(day, "", hhmm, chain)
}

// lutSaveChainMinuteTo appends a chain to a set's minute file.
func lutSaveChainMinuteTo(day, set string, hhmm int, chain *OptionChainSnapshot) {
	if chain == nil {
		return
	}
	m := lutChainMinute{Time: fmt.Sprintf("%02d:%02d", hhmm/100, hhmm%100), Ts: time.Now().In(lutIST()).Format("15:04:05.000"),
		Future: lutUnderlying(chain), ATM: chain.ATM, Expiry: chain.Expiry}
	for _, r := range chain.Chain {
		if r.CELtp <= 0 && r.PELtp <= 0 {
			continue
		}
		m.Rows = append(m.Rows, lutChainRow{K: r.Strike, CEToken: r.CEToken, PEToken: r.PEToken, CE: r.CELtp, PE: r.PELtp,
			CEBid: r.CEBid, CEAsk: r.CEAsk, PEBid: r.PEBid, PEAsk: r.PEAsk,
			CEDelta: r.CEDelta, PEDelta: r.PEDelta, CEGamma: r.CEGamma, PEGamma: r.PEGamma, CEIV: r.CEIV, PEIV: r.PEIV})
	}
	if len(m.Rows) == 0 {
		return
	}
	b, err := json.Marshal(m)
	if err == nil {
		if err = os.MkdirAll(lutDataDir(), 0o755); err == nil {
			err = lutAppend(lutChainFile(day, set), append(b, '\n'))
		}
	}
	if err != nil {
		log.Printf("[LUT] ⚠ cannot save chain minute %s: %v", m.Time, err)
	}
}

// lutLoadChainDay returns the chain minutes recorded here, then fills the
// minutes / strikes they lack from YYYY-MM-DD_chain_import.jsonl (minute
// data imported from another recorder; rows keep their Src tag).
func lutLoadChainDay(day string) map[string]lutChainMinute { return lutLoadChainSet(day, "") }

// lutLoadChainSet is lutLoadChainDay for a set ("" current / "next").
func lutLoadChainSet(day, set string) map[string]lutChainMinute {
	out := lutLoadChainFile(lutChainFile(day, set))
	for t, im := range lutLoadChainFile(lutChainImportFile(day, set)) {
		for i := range im.Rows {
			if im.Rows[i].Src == "" {
				im.Rows[i].Src = im.Src
			}
		}
		cur, ok := out[t]
		if !ok {
			out[t] = im
			continue
		}
		have := map[float64]bool{}
		for _, r := range cur.Rows {
			have[r.K] = true
		}
		for _, r := range im.Rows {
			if !have[r.K] {
				cur.Rows = append(cur.Rows, r)
			}
		}
		out[t] = cur
	}
	return out
}

// lutLoadChainRecorded is only what this gateway recorded (first per minute).
func lutLoadChainRecorded(day string) map[string]lutChainMinute {
	return lutLoadChainFile(lutChainFile(day, ""))
}

// lutNextExpiry is the first listed expiry after the chain's own.
func lutNextExpiry(chain *OptionChainSnapshot) string {
	if chain == nil {
		return ""
	}
	cur, err := lutParseExpiry(chain.Expiry)
	if err != nil {
		return ""
	}
	best, bestT := "", time.Time{}
	for _, e := range chain.AvailableExpiries {
		t, err := lutParseExpiry(e)
		if err != nil || !t.After(cur) {
			continue
		}
		if best == "" || t.Before(bestT) {
			best, bestT = e, t
		}
	}
	return best
}

func lutLoadChainFile(path string) map[string]lutChainMinute {
	out := map[string]lutChainMinute{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var m lutChainMinute
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Time != "" {
			if _, ok := out[m.Time]; !ok {
				out[m.Time] = m
			}
		}
	}
	return out
}
