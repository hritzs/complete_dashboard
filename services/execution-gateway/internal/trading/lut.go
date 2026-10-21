package trading

// LUT-based entry (port of refernce_py_code/7DOptimizerVMonotonicCustom5StageProfilerInput.py).
//
// Each minute from 09:16 the market is reduced to a 6-part coordinate
//   (DTE, IV/IDV ratio, prev straddle / current straddle, build IV,
//    normalised opening gap, normalised adj IV change)
// and looked up in one of five pre-computed boolean tables (09:16, 09:17,
// 09:18, 09:19, 09:20 onwards). The first minute whose table says YES is
// the entry: SL 14 bps of the underlying, TP interpolated from the build
// IV by DTE. Event days: no entry before 09:18, one-third size. 1 DTE: no
// entry after 09:59.
//
// Everything in this file is pure (no I/O beyond the loaders) so it can be
// unit-tested against the reference's numbers.

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reference parameters.
const (
	lutWkndAdj     = 1.0
	lutSLBps       = 14.0
	lutSLBpsExpiry = 16.0 // new logic, DTE row 0 (backtest SL_BPS_BY_DTE)
	lutStrikeStep  = 50.0
	lutLastScan    = 1330 // hhmm
)

var (
	lutWeights      = [3]float64{0.2, 0.5, 0.3} // pure IDV, IDV with prev, yesterday IV
	lutOGThresholds = []float64{-0.7501, 0.7501}
	lutTPHigh       = [7]float64{30, 25, 25, 20, 15, 15, 15}
	lutTPLow        = [7]float64{16, 13, 13, 10, 8, 8, 8}
	lutStageFiles   = [5]string{"LUT_0916.csv", "LUT_0917.csv", "LUT_0918.csv", "LUT_0919.csv", "LUT_0920_Onwards.csv"}
	lutStageNames   = [5]string{"09:16", "09:17", "09:18", "09:19", "09:20+"}
	lutCSVDTEMap    = map[string]int{"1": 0, "2": 1, "3": 2, "4": 3, "5": 4, "6": 5, "7": 6}
	lutDTELabels    = []string{"1", "2", "3", "4", "5", "6", "7"}
	lutOGLabels     = []string{"< -0.7501%", "-0.7501% to 0.7501%", "> 0.7501%"}
)

// lutSchema is one generation of the entry tables: their bucket edges, the
// label strings their CSV rows use (in bucket order) and the rules applied
// before the table. Two exist side by side so both can be shown and
// recorded on the same day.
type lutSchema struct {
	Key, Name     string
	DefaultDir    string
	IVThresholds  []float64
	StrThresholds []float64
	BldThresholds []float64
	AdjThresholds []float64
	IVLabels      []string
	StrLabels     []string
	BldLabels     []string
	AdjLabels     []string
	// Rules the backtest applies before the table
	// (GammaShort_Backtest_Final.py): straddle-ratio veto per DTE row and a
	// Build-IV floor; nil = none.
	VetoStr     map[int]float64 // DTE row -> max straddle ratio
	IVFloor     map[int]float64 // DTE row -> min adjusted Build IV
	UseFinalChg bool            // IV change from the daily file's Final_IV_Chg
	UseEvents   bool            // event days (EventDates.xlsx): nothing before 09:18, one third size
	RoundTP     bool            // TP bps rounded to a whole number (backtest), from adj IV rounded to 2 dp

	csvIV, csvStr, csvBld map[string]int
}

func (s *lutSchema) init() *lutSchema {
	s.csvIV, s.csvStr, s.csvBld = lutLabelIndex(s.IVLabels), lutLabelIndex(s.StrLabels), lutLabelIndex(s.BldLabels)
	return s
}

// dims are the bucket counts: DTE, IV, STR, BLD, OG, ADJ.
func (s *lutSchema) dims() [6]int {
	return [6]int{len(lutDTELabels), len(s.IVLabels), len(s.StrLabels), len(s.BldLabels), len(lutOGLabels), len(s.AdjLabels)}
}

func (s *lutSchema) cellCount() int {
	n := 1
	for _, d := range s.dims() {
		n *= d
	}
	return n
}

// index flattens a coordinate (row-major, as the CSV rows are ordered).
func (s *lutSchema) index(c LUTCoord) int {
	d := s.dims()
	i := 0
	for k := 0; k < 6; k++ {
		if c[k] < 0 || c[k] >= d[k] {
			return -1
		}
		i = i*d[k] + c[k]
	}
	return i
}

var (
	// lutSchemaOld: \\172.16.1.85\Shared\Hardik\Profilers (in use to 2026-10-14).
	lutSchemaOld = (&lutSchema{
		Key: "old", Name: "old tables (Profilers)", DefaultDir: "/mnt/shared/Hardik/Profilers",
		IVThresholds:  []float64{0.6501, 0.8001, 0.9001, 0.9501, 1.0001, 1.0501, 1.1001, 1.2001, 1.3001},
		StrThresholds: []float64{0.9501, 1.0001, 1.0501, 1.1001, 1.1501, 1.2001, 1.3001, 1.5001, 1.7501, 2.0001},
		BldThresholds: []float64{0.0801, 0.1201, 0.1601, 0.2001},
		AdjThresholds: []float64{-0.008, 0.004},
		IVLabels:      []string{"<=0.65", "0.66-0.80", "0.81-0.90", "0.91-0.95", "0.96-1.00", "1.01-1.05", "1.06-1.10", "1.11-1.20", "1.21-1.30", ">1.30"},
		StrLabels:     []string{"<=0.95", "0.96-1.00", "1.01-1.05", "1.06-1.10", "1.11-1.15", "1.16-1.20", "1.21-1.30", "1.31-1.50", "1.51-1.75", "1.76-2.00", ">2.00"},
		BldLabels:     []string{"<0.08", "0.08-0.12", "0.12-0.16", "0.16-0.20", ">=0.20"},
		AdjLabels:     []string{"<= -0.008", "-0.008 to 0.004", "> 0.004"},
	}).init()
	// lutSchemaNew: \\172.16.1.85\Shared\Hardik\Profilers\12102026 = the
	// backtest's r46V2 tables, with its veto, floor and IV change.
	lutSchemaNew = (&lutSchema{
		Key: "new", Name: "new tables (Profilers/12102026 = backtest)", DefaultDir: "/mnt/shared/Hardik/Profilers/12102026",
		IVThresholds:  []float64{0.6501, 0.8001, 0.9001, 0.9501, 1.0001, 1.0501, 1.1001, 1.2001, 1.3001},
		StrThresholds: []float64{0.9501, 1.0001, 1.0501, 1.1001, 1.1501, 1.2001, 1.3001, 1.4001, 1.5001, 1.7501, 2.0001},
		BldThresholds: []float64{0.0801, 0.0901, 0.1001, 0.1201, 0.1601, 0.2001},
		AdjThresholds: []float64{-0.007, 0.005},
		IVLabels:      []string{"<=0.65", "0.65-0.80", "0.80-0.90", "0.90-0.95", "0.95-1.00", "1.00-1.05", "1.05-1.10", "1.10-1.20", "1.20-1.30", ">1.30"},
		StrLabels:     []string{"<=0.95", "0.95-1.00", "1.00-1.05", "1.05-1.10", "1.10-1.15", "1.15-1.20", "1.20-1.30", "1.30-1.40", "1.40-1.50", "1.50-1.75", "1.75-2.00", ">2.00"},
		BldLabels:     []string{"<=0.08", "0.08-0.09", "0.09-0.10", "0.10-0.12", "0.12-0.16", "0.16-0.20", ">0.20"},
		AdjLabels:     []string{"<= -0.007", "-0.007 to 0.005", ">= 0.005"},
		VetoStr:       map[int]float64{0: 1.70, 1: 1.275, 2: 1.225, 3: 1.175, 4: 1.15, 5: 1.125},
		IVFloor:       map[int]float64{0: 0.12, 1: 0.12},
		UseFinalChg:   true,
		UseEvents:     true,
		RoundTP:       true,
	}).init()
)

// lutSchemaByKey: "old" / "new" (anything else = nil).
func lutSchemaByKey(k string) *lutSchema {
	switch k {
	case "old":
		return lutSchemaOld
	case "new":
		return lutSchemaNew
	}
	return nil
}

// LUTCondition is one YES cell of a table translated into market levels:
// the build IV (raw, as read off the chain) and the ATM straddle that would
// put today's coordinate into it. DTE, opening gap and adj IV change are
// fixed for the day, so only these two move.
type LUTCondition struct {
	Table        string  `json:"table"`
	Coord        string  `json:"coord"`
	IVBucket     string  `json:"iv_bucket"`
	StrBucket    string  `json:"str_bucket"`
	BldBucket    string  `json:"bld_bucket"`
	BuildIVFrom  float64 `json:"build_iv_from"` // 0 = no lower bound
	BuildIVTo    float64 `json:"build_iv_to"`   // 0 = no upper bound
	StraddleFrom float64 `json:"straddle_from"` // 0 = no lower bound
	StraddleTo   float64 `json:"straddle_to"`   // 0 = no upper bound
	IVOK         bool    `json:"iv_ok"`         // today's build IV already inside
	StraddleOK   bool    `json:"straddle_ok"`   // today's straddle already inside
	Distance     float64 `json:"distance"`      // relative gap to reach the cell
}

// bucketBounds returns [lo, hi) of bucket b for thresholds (+/-Inf at the ends).
func bucketBounds(thresholds []float64, b int) (float64, float64) {
	lo, hi := math.Inf(-1), math.Inf(1)
	if b > 0 {
		lo = thresholds[b-1]
	}
	if b < len(thresholds) {
		hi = thresholds[b]
	}
	return lo, hi
}

func finiteOrZero(v float64) float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) || v < 0 {
		return 0
	}
	return v
}

// lutNearestYes lists the YES cells of one table that today's fixed
// (DTE, OG, ADJ) can still reach, as build-IV and straddle ranges, nearest
// first. curIV is the raw build IV, curStraddle the current ATM straddle.
func lutNearestYes(t *LUTTable, table string, dteIdx, ogIdx, adjIdx int, weightedIDV, adjFactor, prevStraddle, curIV, curStraddle float64, limit int) []LUTCondition {
	if t == nil || weightedIDV <= 0 || adjFactor <= 0 || prevStraddle <= 0 {
		return nil
	}
	gap := func(v, from, to float64) float64 { // relative distance to [from, to)
		if v <= 0 {
			return 1
		}
		if from > 0 && v < from {
			return (from - v) / v
		}
		if to > 0 && v >= to {
			return (v - to) / v
		}
		return 0
	}
	sc := t.schema
	var out []LUTCondition
	for iv := 0; iv < len(sc.IVLabels); iv++ {
		for st := 0; st < len(sc.StrLabels); st++ {
			for b := 0; b < len(sc.BldLabels); b++ {
				c := LUTCoord{dteIdx, iv, st, b, ogIdx, adjIdx}
				if !t.Allowed(c) {
					continue
				}
				// Adjusted build IV must satisfy BOTH the IV-ratio bucket and
				// the build-IV bucket.
				rLo, rHi := bucketBounds(sc.IVThresholds, iv)
				bLo, bHi := bucketBounds(sc.BldThresholds, b)
				lo, hi := math.Max(rLo*weightedIDV, bLo), math.Min(rHi*weightedIDV, bHi)
				if !(lo < hi) {
					continue // unreachable cell
				}
				// Straddle ratio = prev / current -> current in (prev/hi, prev/lo].
				sLo, sHi := bucketBounds(sc.StrThresholds, st)
				cond := LUTCondition{
					Table: table, Coord: c.String(),
					IVBucket: sc.IVLabels[iv], StrBucket: sc.StrLabels[st], BldBucket: sc.BldLabels[b],
					BuildIVFrom: finiteOrZero(lo / adjFactor), BuildIVTo: finiteOrZero(hi / adjFactor),
					StraddleFrom: finiteOrZero(prevStraddle / sHi), StraddleTo: finiteOrZero(prevStraddle / sLo),
				}
				gIV := gap(curIV, cond.BuildIVFrom, cond.BuildIVTo)
				gS := gap(curStraddle, cond.StraddleFrom, cond.StraddleTo)
				cond.IVOK, cond.StraddleOK = gIV == 0, gS == 0
				cond.Distance = gIV + gS
				out = append(out, cond)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// LUTCoord is one cell of a table: DTE, IV, STR, BLD, OG, ADJ bucket.
type LUTCoord [6]int

// lutLabelIndex maps each label (spaces removed) to its position.
func lutLabelIndex(labels []string) map[string]int {
	m := make(map[string]int, len(labels))
	for i, l := range labels {
		m[strings.ReplaceAll(l, " ", "")] = i
	}
	return m
}

func (c LUTCoord) String() string {
	return fmt.Sprintf("(%d,%d,%d,%d,%d,%d)", c[0], c[1], c[2], c[3], c[4], c[5])
}

// LUTTable is one stage's YES/NO table.
type LUTTable struct {
	schema *lutSchema
	cells  []bool
	Yes    int
}

func (t *LUTTable) Allowed(c LUTCoord) bool {
	if t == nil {
		return false
	}
	i := t.schema.index(c)
	return i >= 0 && i < len(t.cells) && t.cells[i]
}

// LUTSet is the five stage tables (09:16 .. 09:20+).
type LUTSet [5]*LUTTable

// schema of the set (nil when not loaded).
func (s LUTSet) schema() *lutSchema {
	if s[0] == nil {
		return nil
	}
	return s[0].schema
}

// ParseLUTCSV reads one profiler CSV (header DTE,IV_Ratio,Straddle_Ratio,
// Build_IV,Norm_OG_Gap,Adj_IV_Chg,Trade) with the reference's mappings.
func ParseLUTCSV(r io.Reader, sc *lutSchema) (*LUTTable, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("LUT csv has no data rows")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimSpace(strings.TrimPrefix(h, string(rune(0xFEFF))))] = i
	}
	for _, need := range []string{"DTE", "IV_Ratio", "Straddle_Ratio", "Build_IV", "Norm_OG_Gap", "Adj_IV_Chg", "Trade"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("LUT csv missing column %s", need)
		}
	}
	get := func(row []string, name string) string {
		if i := col[name]; i < len(row) {
			return row[i]
		}
		return ""
	}
	nospace := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), " ", "") }

	t := &LUTTable{schema: sc, cells: make([]bool, sc.cellCount())}
	for _, row := range rows[1:] {
		trade := strings.ToUpper(strings.TrimSpace(get(row, "Trade")))
		if trade != "YES" && trade != "TRUE" && trade != "1" {
			continue
		}
		d, ok1 := lutCSVDTEMap[strings.TrimSuffix(strings.TrimSpace(get(row, "DTE")), ".0")]
		iv, ok2 := sc.csvIV[nospace(get(row, "IV_Ratio"))]
		st, ok3 := sc.csvStr[nospace(get(row, "Straddle_Ratio"))]
		b, ok4 := sc.csvBld[nospace(get(row, "Build_IV"))]
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue // reference skips unmappable rows (KeyError -> pass)
		}
		ogs := nospace(get(row, "Norm_OG_Gap"))
		og := 1
		if strings.Contains(ogs, "<") && !strings.Contains(ogs, "Between") {
			og = 0
		} else if strings.Contains(ogs, ">") && !strings.Contains(ogs, "Between") {
			og = 2
		}
		as := nospace(get(row, "Adj_IV_Chg"))
		a := 1
		if strings.Contains(as, "<") && !strings.Contains(as, "to") {
			a = 0
		} else if strings.Contains(as, ">") && !strings.Contains(as, "to") {
			a = 2
		}
		c := LUTCoord{d, iv, st, b, og, a}
		if i := sc.index(c); i >= 0 && !t.cells[i] {
			t.cells[i] = true
			t.Yes++
		}
	}
	return t, nil
}

// LoadLUTSet loads the five stage tables of one schema from dir.
func LoadLUTSet(dir string, sc *lutSchema) (LUTSet, error) {
	var set LUTSet
	for i, name := range lutStageFiles {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return set, fmt.Errorf("open %s: %w", name, err)
		}
		t, err := ParseLUTCSV(f, sc)
		f.Close()
		if err != nil {
			return set, fmt.Errorf("parse %s: %w", name, err)
		}
		set[i] = t
	}
	return set, nil
}

// bucketRight is numpy searchsorted(thresholds, x, side='right'): the number
// of thresholds <= x.
func bucketRight(thresholds []float64, x float64) int {
	return sort.Search(len(thresholds), func(i int) bool { return thresholds[i] > x })
}

// LUTDailyInputs is today's row of GammaShortDailydata.csv, as the reference
// uses it.
type LUTDailyInputs struct {
	Date             string
	PrevStraddle     float64 // Straddle
	PrevFuture       float64 // Future
	YesterdayIV      float64 // Adj_IV
	IDVPure          float64
	IDVWithPrev      float64
	NormalizedAdjChg float64
	WeightedIDV      float64
	// FinalAdjChg: the same normalised change from Final_IV_Chg (what the
	// backtest and the new tables use); HasFinalChg false = column absent.
	FinalAdjChg float64
	HasFinalChg bool
	// Next-week columns (the next-week check, lut_nw.go); 0 / "" when absent.
	NWAdjIV  float64
	NWExpiry string
}

// adjChg is the normalised IV change a schema buckets.
func (in LUTDailyInputs) adjChg(sc *lutSchema) float64 {
	if sc != nil && sc.UseFinalChg && in.HasFinalChg {
		return in.FinalAdjChg
	}
	return in.NormalizedAdjChg
}

// LoadLUTDailyInputs reads the row for date (YYYY-MM-DD) from the daily CSV.
func LoadLUTDailyInputs(path, date string) (LUTDailyInputs, error) {
	var in LUTDailyInputs
	f, err := os.Open(path)
	if err != nil {
		return in, err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return in, err
	}
	if len(rows) < 2 {
		return in, fmt.Errorf("daily csv is empty")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimSpace(strings.TrimPrefix(h, string(rune(0xFEFF))))] = i
	}
	var row []string
	for _, r := range rows[1:] {
		if i, ok := col["Date"]; ok && i < len(r) && strings.HasPrefix(strings.TrimSpace(r[i]), date) {
			row = r // last match wins, like iloc[-1]
		}
	}
	if row == nil {
		return in, fmt.Errorf("no row for %s in %s (daily data not prepared yet?)", date, filepath.Base(path))
	}
	num := func(name string) (float64, error) {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return 0, fmt.Errorf("column %s missing", name)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
		if err != nil {
			return 0, fmt.Errorf("column %s=%q: %w", name, row[i], err)
		}
		return v, nil
	}
	day := func(name string) (time.Time, error) {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return time.Time{}, fmt.Errorf("column %s missing", name)
		}
		s := strings.TrimSpace(row[i])
		if len(s) >= 10 {
			s = s[:10]
		}
		return time.Parse("2006-01-02", s)
	}
	var e error
	pick := func(name string) float64 {
		v, err := num(name)
		if err != nil && e == nil {
			e = err
		}
		return v
	}
	in.Date = date
	in.PrevStraddle = pick("Straddle")
	in.PrevFuture = pick("Future")
	in.YesterdayIV = pick("Adj_IV")
	in.IDVPure = pick("IDV_Pure")
	in.IDVWithPrev = pick("IDV_With_Prev")
	ivChg, adjChg := pick("IV_Chg"), pick("Adj_IV_Chg")
	prev2IV, prev2AdjIV := pick("Prev2_IV"), pick("Prev2_Adj_IV")
	if e != nil {
		return in, e
	}
	// Final_IV_Chg, when the file has it, is the change the backtest uses
	// (GammaShort_Backtest_Final.py load_day_inputs); the base still follows
	// the gap rule below. Kept beside the old change, not instead of it.
	finalChg, finalErr := num("Final_IV_Chg")
	prevDate, err1 := day("PrevDate")
	prev2Date, err2 := day("Prev2Date")
	if err1 != nil || err2 != nil {
		return in, fmt.Errorf("PrevDate/Prev2Date unreadable")
	}
	// Gap day (weekend/holiday between Prev2 and Prev): use the adjusted
	// change and base, else the plain ones -- exactly as the reference.
	chg, base := ivChg, prev2IV
	if prevDate.Sub(prev2Date).Hours()/24 > 1 {
		chg, base = adjChg, prev2AdjIV
	}
	if base == 0 {
		return in, fmt.Errorf("IV base is 0")
	}
	in.NormalizedAdjChg = chg * 0.1 / base
	if finalErr == nil {
		in.FinalAdjChg, in.HasFinalChg = finalChg*0.1/base, true
	}
	in.WeightedIDV = lutWeights[0]*in.IDVPure + lutWeights[1]*in.IDVWithPrev + lutWeights[2]*in.YesterdayIV
	if v, err := num("NW_Adj_IV"); err == nil {
		in.NWAdjIV = v
	}
	if i, ok := col["NW_Expiry"]; ok && i < len(row) {
		in.NWExpiry = strings.TrimSpace(row[i])
	}
	if in.PrevStraddle <= 0 || in.PrevFuture <= 0 || in.YesterdayIV <= 0 || in.IDVPure <= 0 || in.IDVWithPrev <= 0 || in.WeightedIDV <= 0 {
		return in, fmt.Errorf("daily inputs not usable: %+v", in)
	}
	return in, nil
}

// --- Black-Scholes (r=0), exactly the reference kernels ------------------

func lutNormCDF(x float64) float64 { return 0.5 * (1.0 + math.Erf(x/math.Sqrt2)) }

func lutBSPrice(isCall bool, S, K, T, sig float64) float64 {
	if T <= 0 || sig <= 0 {
		return 0
	}
	sq := math.Sqrt(T)
	d1 := (math.Log(S/K) + 0.5*sig*sig*T) / (sig * sq)
	d2 := d1 - sig*sq
	if isCall {
		return S*lutNormCDF(d1) - K*lutNormCDF(d2)
	}
	return K*lutNormCDF(-d2) - S*lutNormCDF(-d1)
}

// lutImpliedVol is the reference's 30-step bisection on [1e-6, 5].
func lutImpliedVol(isCall bool, price, S, K, T float64) (float64, bool) {
	if price <= 0 || T <= 0 {
		return 0, false
	}
	lo, hi := 1e-6, 5.0
	if lutBSPrice(isCall, S, K, T, lo) > price || lutBSPrice(isCall, S, K, T, hi) < price {
		return 0, false
	}
	for i := 0; i < 30; i++ {
		mid := 0.5 * (lo + hi)
		pm := lutBSPrice(isCall, S, K, T, mid)
		if math.Abs(pm-price) < 1e-5 {
			return mid, true
		}
		if pm < price {
			lo = mid
		} else {
			hi = mid
		}
	}
	return 0.5 * (lo + hi), true
}

// --- DTE -----------------------------------------------------------------

// lutRawDTE is DTE on the session clock: whole calendar days to expiry plus
// the fraction of today's 09:15-15:40 session still remaining (NSE's
// session now runs to 15:40: 385 minutes).
func lutRawDTE(now time.Time, expiry time.Time) float64 {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	exp := time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 0, 0, 0, 0, loc)
	days := math.Round(exp.Sub(today).Hours() / 24)
	open := today.Add(9*time.Hour + 15*time.Minute)
	closeT := today.Add(15*time.Hour + 40*time.Minute) // session 09:15-15:40
	remaining := closeT.Sub(now).Seconds()
	session := closeT.Sub(open).Seconds()
	limit := session
	if _, so, sc, ok := specialSessionOn(now); ok {
		// A special session (Muhurat): only its own remaining trading
		// minutes count, as a share of a normal day.
		remaining, limit = sc.Sub(now).Seconds(), sc.Sub(so).Seconds()
	}
	if remaining < 0 {
		remaining = 0
	}
	if remaining > limit {
		remaining = limit
	}
	return days + remaining/session
}

// lutBusDays is numpy busday_count(start, end): weekdays in [start, end).
func lutBusDays(start time.Time, days int) int {
	n := 0
	d := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	for i := 0; i < days; i++ {
		if wd := d.AddDate(0, 0, i).Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// LUTMinuteInput is what one minute's evaluation needs from the market.
type LUTMinuteInput struct {
	HHMM       int     // e.g. 916
	Underlying float64 // futures LTP
	RawDTE     float64
	BusDays    int     // busday_count(today, today+ceil(raw DTE at 09:15))
	CELTP      float64 // at the ATM strike
	PELTP      float64
	PrevIV     float64 // last good build IV (reference ffills); 0 if none
}

// LUTEvaluation is one minute's full decision, for logs and the UI.
type LUTEvaluation struct {
	Tables       string   `json:"tables,omitempty"` // "old" / "new" table set (lutSchema.Key)
	Time         string   `json:"time"`
	Stage        string   `json:"stage"`
	Strike       float64  `json:"strike"`
	Underlying   float64  `json:"underlying"`
	RawDTE       float64  `json:"raw_dte"`
	TradingDTE   float64  `json:"trading_dte"`
	AdjFactor    float64  `json:"adj_factor"`
	BuildIV      float64  `json:"build_iv"`
	AdjBuildIV   float64  `json:"adj_build_iv"`
	IVRatio      float64  `json:"iv_ratio"`
	Straddle     float64  `json:"straddle"`
	StrRatio     float64  `json:"str_ratio"`
	NormOG       float64  `json:"norm_og"`
	AdjIVChg     float64  `json:"adj_iv_chg"`
	Coord        LUTCoord `json:"coord"`
	CoordText    string   `json:"coord_text"`
	Allowed      bool     `json:"allowed"`
	TPBps        float64  `json:"tp_bps"`
	Skip         string   `json:"skip,omitempty"`
	StopScanning bool     `json:"stop_scanning,omitempty"`

	// Bucket labels of the coordinate (CSV wording).
	DTELabel string `json:"dte_label,omitempty"`
	IVLabel  string `json:"iv_label,omitempty"`
	StrLabel string `json:"str_label,omitempty"`
	BldLabel string `json:"bld_label,omitempty"`
	OGLabel  string `json:"og_label,omitempty"`
	AdjLabel string `json:"adj_label,omitempty"`

	// When NO: the nearest YES cells as market levels, for this minute's
	// table and (before 09:20) the 09:20+ table used from then on.
	NearestYes         []LUTCondition `json:"nearest_yes,omitempty"`
	NearestYes0920     []LUTCondition `json:"nearest_yes_0920,omitempty"`
	TableYesCellsToday int            `json:"table_yes_cells_today"` // YES cells reachable today in this table

	// Raw inputs read off the chain this minute, for verification.
	CELTP    float64 `json:"ce_ltp"`
	PELTP    float64 `json:"pe_ltp"`
	OTMLeg   string  `json:"otm_leg"`
	OTMPrice float64 `json:"otm_price"`
	TYears   float64 `json:"t_years"`
	Preview  bool    `json:"preview,omitempty"` // evaluated outside 09:16-13:30 for display
	// TableSays: on a SKIP minute that was still fully evaluated (no-entry
	// window), what the table answered -- for the record only.
	TableSays string `json:"table_says,omitempty"`
}

// lutStage maps hhmm to the table index: 0=09:16 .. 3=09:19, 4=09:20+.
func lutStage(hhmm int) int {
	switch hhmm {
	case 916:
		return 0
	case 917:
		return 1
	case 918:
		return 2
	case 919:
		return 3
	}
	return 4
}

// lutRefreshTP recomputes a recorded minute's TP from its own recorded
// inputs (adj build IV, trading DTE) with the current formula, so minutes
// recorded before a formula change show the TP the rules give now. The
// LUT answer itself is never touched. Reports whether it changed.
func lutRefreshTP(ev *LUTEvaluation) bool {
	if ev == nil || ev.AdjBuildIV <= 0 || ev.TradingDTE <= 0 {
		return false
	}
	tp := lutTPFor(lutSchemaByKey(ev.Tables), lutDTEIndex(ev.TradingDTE), ev.AdjBuildIV)
	if tp == ev.TPBps {
		return false
	}
	ev.TPBps = tp
	return true
}

// lutDTEIndex is the DTE row (0-6) for a trading-days-to-expiry value.
func lutDTEIndex(trading float64) int {
	return min(max(int(math.Ceil(trading))-1, 0), 6)
}

// lutTPBpsRounded is the backtest's TP (GammaShort_Backtest_Final.py
// simulate_cw): max(1, round(low + (round(adj IV, 2) - 0.08) / 0.12 x (high - low))).
func lutTPBpsRounded(dteIdx int, adjBuildIV float64) float64 {
	fraction := (math.Round(adjBuildIV*100)/100 - 0.08) / 0.12
	return math.Max(1, math.Round(lutTPLow[dteIdx]+fraction*(lutTPHigh[dteIdx]-lutTPLow[dteIdx])))
}

// lutTPFor: the TP of a table set (rounded for the new set).
func lutTPFor(sc *lutSchema, dteIdx int, adjBuildIV float64) float64 {
	if sc != nil && sc.RoundTP {
		return lutTPBpsRounded(dteIdx, adjBuildIV)
	}
	return lutTPBps(dteIdx, adjBuildIV)
}

// lutTPBps interpolates TP by adj build IV between the DTE row's low/high.
// Exact: neither the IV nor the resulting bps is rounded.
func lutTPBps(dteIdx int, adjBuildIV float64) float64 {
	fraction := (adjBuildIV - 0.08) / 0.12
	tp := lutTPLow[dteIdx] + fraction*(lutTPHigh[dteIdx]-lutTPLow[dteIdx])
	return math.Max(1, tp)
}

// EvaluateLUTMinute reduces one minute to its coordinate and looks it up.
func EvaluateLUTMinute(set LUTSet, in LUTDailyInputs, normOG float64, isEvent bool, m LUTMinuteInput) LUTEvaluation {
	sc := set.schema()
	if sc == nil {
		sc = lutSchemaOld
	}
	ev := LUTEvaluation{
		Tables:     sc.Key,
		Time:       fmt.Sprintf("%02d:%02d", m.HHMM/100, m.HHMM%100),
		Stage:      lutStageNames[lutStage(m.HHMM)],
		Underlying: m.Underlying,
		RawDTE:     m.RawDTE,
		NormOG:     normOG,
		AdjIVChg:   in.adjChg(sc),
	}
	switch {
	case m.HHMM < 916:
		ev.Skip = "before 09:16"
		return ev
	case isEvent && sc.UseEvents && m.HHMM < 918:
		ev.Skip = "event day: no entry before 09:18"
		return ev
	case m.RawDTE <= 0:
		ev.Skip = "DTE <= 0"
		return ev
	case m.Underlying <= 0:
		ev.Skip = "no underlying"
		return ev
	}

	K := math.Round(m.Underlying/lutStrikeStep) * lutStrikeStep
	ev.Strike = K
	T := math.Max(m.RawDTE/365.0, 1e-5)

	// OTM option's IV (CE if underlying below strike, else PE).
	isCall := m.Underlying < K
	price := m.PELTP
	if isCall {
		price = m.CELTP
	}
	ev.CELTP, ev.PELTP, ev.OTMPrice, ev.TYears = m.CELTP, m.PELTP, price, T
	ev.OTMLeg = "PE"
	if isCall {
		ev.OTMLeg = "CE"
	}
	iv, ok := 0.0, false
	if price > 0.1 {
		iv, ok = lutImpliedVol(isCall, price, m.Underlying, K, T)
	}
	if !ok || iv > 2.0 || iv < 0.01 {
		if m.PrevIV <= 0 {
			ev.Skip = "no valid ATM IV yet"
			return ev
		}
		iv = m.PrevIV // reference forward-fills
	}
	ev.BuildIV = iv
	// Straddle = the recorded ATM CE LTP + PE LTP, as traded -- not a
	// model price from the OTM leg's IV (that read ~2pts off the screen).
	if m.CELTP <= 0 || m.PELTP <= 0 {
		ev.Skip = fmt.Sprintf("ATM LTP missing (CE %.2f PE %.2f)", m.CELTP, m.PELTP)
		return ev
	}
	ev.Straddle = m.CELTP + m.PELTP
	ev.StrRatio = in.PrevStraddle / ev.Straddle

	// weekend_days = ceil(this minute's DTE) - busday_count(today,
	// today + ceil(first minute's DTE)), as the reference.
	weekendDays := int(math.Ceil(m.RawDTE)) - m.BusDays
	trading := m.RawDTE
	if weekendDays > 0 {
		trading = math.Max(0, m.RawDTE-lutWkndAdj)
	}
	ev.TradingDTE = trading
	ev.AdjFactor = 1
	if m.RawDTE > 0 && trading > 0 && m.RawDTE != trading {
		ev.AdjFactor = math.Sqrt(m.RawDTE / trading)
	}
	dteIdx := lutDTEIndex(trading)
	// No-entry windows still get every number computed and recorded (the
	// data is kept); the minute is marked SKIP at the end, never a YES.
	lateSkip := ""
	switch {
	case m.HHMM > lutLastScan:
		lateSkip = "after 13:30"
	case dteIdx == 0 && m.HHMM > 959:
		lateSkip = "1 DTE: no entry after 09:59"
	}

	ev.AdjBuildIV = iv * ev.AdjFactor
	ev.IVRatio = ev.AdjBuildIV / in.WeightedIDV
	ev.Coord = LUTCoord{
		dteIdx,
		bucketRight(sc.IVThresholds, ev.IVRatio),
		bucketRight(sc.StrThresholds, ev.StrRatio),
		bucketRight(sc.BldThresholds, ev.AdjBuildIV),
		bucketRight(lutOGThresholds, normOG),
		bucketRight(sc.AdjThresholds, ev.AdjIVChg),
	}
	ev.CoordText = ev.Coord.String()
	ev.Allowed = set[lutStage(m.HHMM)].Allowed(ev.Coord)
	// Veto and floor come before the table (backtest order). The minute is
	// still fully recorded; what the table said is kept in TableSays.
	ruleSkip := ""
	if thr, ok := sc.VetoStr[dteIdx]; ok && ev.StrRatio > thr {
		ruleSkip = fmt.Sprintf("straddle veto: ratio %.3f > %.3f for DTE %d", ev.StrRatio, thr, dteIdx+1)
	} else if fl, ok := sc.IVFloor[dteIdx]; ok && ev.AdjBuildIV < fl {
		ruleSkip = fmt.Sprintf("Build IV floor: %.4f < %.2f on DTE %d", ev.AdjBuildIV, fl, dteIdx+1)
	}
	ev.TPBps = lutTPFor(sc, dteIdx, ev.AdjBuildIV)
	ev.DTELabel = lutDTELabels[ev.Coord[0]]
	ev.IVLabel = sc.IVLabels[ev.Coord[1]]
	ev.StrLabel = sc.StrLabels[ev.Coord[2]]
	ev.BldLabel = sc.BldLabels[ev.Coord[3]]
	ev.OGLabel = lutOGLabels[ev.Coord[4]]
	ev.AdjLabel = sc.AdjLabels[ev.Coord[5]]

	stage := lutStage(m.HHMM)
	all := lutNearestYes(set[stage], lutStageNames[stage], dteIdx, ev.Coord[4], ev.Coord[5],
		in.WeightedIDV, ev.AdjFactor, in.PrevStraddle, ev.BuildIV, ev.Straddle, 0)
	ev.TableYesCellsToday = len(all)
	if !ev.Allowed {
		if len(all) > 8 {
			all = all[:8]
		}
		ev.NearestYes = all
		if stage < 4 {
			ev.NearestYes0920 = lutNearestYes(set[4], lutStageNames[4], dteIdx, ev.Coord[4], ev.Coord[5],
				in.WeightedIDV, ev.AdjFactor, in.PrevStraddle, ev.BuildIV, ev.Straddle, 8)
		}
	}
	if lateSkip != "" {
		ev.TableSays = "NO"
		if ev.Allowed {
			ev.TableSays = "YES"
		}
		ev.Skip, ev.Allowed, ev.StopScanning = lateSkip, false, true
		ev.NearestYes, ev.NearestYes0920 = nil, nil
	} else if ruleSkip != "" {
		ev.TableSays = "NO"
		if ev.Allowed {
			ev.TableSays = "YES"
		}
		ev.Skip, ev.Allowed = ruleSkip, false
	}
	return ev
}

// LUTNormOG is the reference's normalised opening gap off the 09:16 future.
func LUTNormOG(underlying0916, prevFuture, yesterdayIV float64) float64 {
	if prevFuture <= 0 || yesterdayIV <= 0 {
		return 0
	}
	return ((underlying0916 - prevFuture) / prevFuture * 100.0) * (0.19 / yesterdayIV)
}

// LUTParams is every fixed parameter of the entry rule, for display.
type LUTParams struct {
	Schema         string     `json:"schema"` // "old" / "new"
	SchemaName     string     `json:"schema_name"`
	Weights        [3]float64 `json:"weights"` // IDV pure, IDV with prev, yesterday IV
	IVThresholds   []float64  `json:"iv_thresholds"`
	StrThresholds  []float64  `json:"str_thresholds"`
	BldThresholds  []float64  `json:"bld_thresholds"`
	OGThresholds   []float64  `json:"og_thresholds"`
	AdjThresholds  []float64  `json:"adj_thresholds"`
	IVLabels       []string   `json:"iv_labels"`
	StrLabels      []string   `json:"str_labels"`
	BldLabels      []string   `json:"bld_labels"`
	OGLabels       []string   `json:"og_labels"`
	AdjLabels      []string   `json:"adj_labels"`
	TPHighBps      [7]float64 `json:"tp_high_bps"`
	TPLowBps       [7]float64 `json:"tp_low_bps"`
	SLBps          float64    `json:"sl_bps"`
	WeekendAdjDays float64    `json:"weekend_adj_days"`
	StrikeStep     float64    `json:"strike_step"`
	ScanFrom       string     `json:"scan_from"`
	ScanTo         string     `json:"scan_to"`
	Tables         [5]string  `json:"tables"`
	Rules          []string   `json:"rules"`
}

func lutParams(sc *lutSchema) LUTParams {
	if sc == nil {
		sc = lutSchemaOld
	}
	p := LUTParams{
		Schema: sc.Key, SchemaName: sc.Name,
		Weights: lutWeights, IVThresholds: sc.IVThresholds, StrThresholds: sc.StrThresholds,
		BldThresholds: sc.BldThresholds, OGThresholds: lutOGThresholds, AdjThresholds: sc.AdjThresholds,
		IVLabels: sc.IVLabels, StrLabels: sc.StrLabels, BldLabels: sc.BldLabels, OGLabels: lutOGLabels, AdjLabels: sc.AdjLabels,
		TPHighBps: lutTPHigh, TPLowBps: lutTPLow, SLBps: lutSLBps, WeekendAdjDays: lutWkndAdj, StrikeStep: lutStrikeStep,
		ScanFrom: "09:16", ScanTo: "13:30", Tables: lutStageNames,
		Rules: []string{
			"Weighted IDV = 0.2 x IDV_Pure + 0.5 x IDV_With_Prev + 0.3 x yesterday Adj_IV",
			"Norm OG = (09:16 future - prev future) / prev future x 100 x (0.19 / yesterday IV)",
			"Norm adj IV chg = change x 0.1 / base (Adj_IV_Chg / Prev2_Adj_IV after a gap day, else IV_Chg / Prev2_IV)",
			"Build IV = IV of the OTM option at the ATM (CE if future < strike, else PE), Black-Scholes r=0",
			"Straddle = BS call + put at the ATM with that IV; straddle ratio = prev straddle / straddle",
			"Weekend in the expiry window: trading DTE = DTE - 1 and build IV x sqrt(DTE / trading DTE)",
			"IV ratio = adjusted build IV / weighted IDV",
			"Tables: 09:16, 09:17, 09:18, 09:19 each their own; 09:20 to 13:30 the 09:20+ table; first YES wins",
			"1 DTE: nothing after 09:59",
			"TP bps = low + (adj build IV - 0.08) / 0.12 x (high - low) for the DTE row, min 1; SL 14 bps",
		},
	}
	if sc.VetoStr != nil {
		p.Rules = append(p.Rules, fmt.Sprintf("Straddle veto (before the table): no entry when the straddle ratio is above %v by DTE row (0 = 1 DTE)", sc.VetoStr))
	}
	if sc.IVFloor != nil {
		p.Rules = append(p.Rules, fmt.Sprintf("Build IV floor (before the table): no entry when the adjusted build IV is below %v by DTE row", sc.IVFloor))
	}
	if sc.UseEvents {
		p.Rules = append(p.Rules, "Event day (EventDates.xlsx): nothing before 09:18 and one-third size")
	}
	if sc.UseFinalChg {
		p.Rules = append(p.Rules, "IV change from the daily file's Final_IV_Chg (as the backtest)")
	}
	return p
}

// LUTGridCell is one (IV-ratio bucket, straddle-ratio bucket) cell of a
// table for today's fixed DTE / opening gap / IV change.
type LUTGridCell struct {
	Allowed         bool     `json:"allowed"`   // some reachable build-IV bucket is YES
	Reachable       bool     `json:"reachable"` // the IV-ratio bucket overlaps any build bucket
	Current         bool     `json:"current"`
	BuildIVFrom     float64  `json:"build_iv_from"` // raw IV range (YES part if allowed)
	BuildIVTo       float64  `json:"build_iv_to"`
	StraddleFrom    float64  `json:"straddle_from"`
	StraddleTo      float64  `json:"straddle_to"`
	IVMovePct       float64  `json:"iv_move_pct"`       // signed % from today's build IV to the range (0 = inside)
	StraddleMovePct float64  `json:"straddle_move_pct"` // signed % from today's straddle to the range
	YesBuckets      []string `json:"yes_buckets,omitempty"`
}

// LUTGrid is a whole table as a matrix: rows = IV-ratio buckets, columns =
// straddle-ratio buckets.
type LUTGrid struct {
	Table     string          `json:"table"`
	IVLabels  []string        `json:"iv_labels"`
	StrLabels []string        `json:"str_labels"`
	Cells     [][]LUTGridCell `json:"cells"`
	YesCount  int             `json:"yes_count"`
}

func lutMovePct(cur, from, to float64) float64 {
	if cur <= 0 {
		return 0
	}
	if from > 0 && cur < from {
		return (from - cur) / cur * 100
	}
	if to > 0 && cur >= to {
		return (to - cur) / cur * 100
	}
	return 0
}

// BuildLUTGrid lays one table out for today's fixed (DTE, OG, ADJ).
func BuildLUTGrid(t *LUTTable, table string, ev LUTEvaluation, in LUTDailyInputs) LUTGrid {
	g := LUTGrid{Table: table}
	if t == nil {
		return g
	}
	sc := t.schema
	g.IVLabels, g.StrLabels = sc.IVLabels, sc.StrLabels
	if ev.AdjFactor <= 0 || in.WeightedIDV <= 0 || in.PrevStraddle <= 0 {
		return g
	}
	dte, og, adj := ev.Coord[0], ev.Coord[4], ev.Coord[5]
	g.Cells = make([][]LUTGridCell, len(sc.IVLabels))
	for iv := range sc.IVLabels {
		g.Cells[iv] = make([]LUTGridCell, len(sc.StrLabels))
		rLo, rHi := bucketBounds(sc.IVThresholds, iv)
		for st := range sc.StrLabels {
			sLo, sHi := bucketBounds(sc.StrThresholds, st)
			cell := LUTGridCell{
				Current:      ev.Skip == "" && iv == ev.Coord[1] && st == ev.Coord[2],
				StraddleFrom: finiteOrZero(in.PrevStraddle / sHi),
				StraddleTo:   finiteOrZero(in.PrevStraddle / sLo),
			}
			yesLo, yesHi := math.Inf(1), math.Inf(-1)
			anyLo, anyHi := math.Inf(1), math.Inf(-1)
			for b := range sc.BldLabels {
				bLo, bHi := bucketBounds(sc.BldThresholds, b)
				lo, hi := math.Max(rLo*in.WeightedIDV, bLo), math.Min(rHi*in.WeightedIDV, bHi)
				if !(lo < hi) {
					continue
				}
				cell.Reachable = true
				anyLo, anyHi = math.Min(anyLo, lo), math.Max(anyHi, hi)
				if t.Allowed(LUTCoord{dte, iv, st, b, og, adj}) {
					cell.Allowed = true
					cell.YesBuckets = append(cell.YesBuckets, sc.BldLabels[b])
					yesLo, yesHi = math.Min(yesLo, lo), math.Max(yesHi, hi)
				}
			}
			lo, hi := anyLo, anyHi
			if cell.Allowed {
				lo, hi = yesLo, yesHi
				g.YesCount++
			}
			if cell.Reachable {
				cell.BuildIVFrom = finiteOrZero(lo / ev.AdjFactor)
				cell.BuildIVTo = finiteOrZero(hi / ev.AdjFactor)
			}
			cell.IVMovePct = lutMovePct(ev.BuildIV, cell.BuildIVFrom, cell.BuildIVTo)
			cell.StraddleMovePct = lutMovePct(ev.Straddle, cell.StraddleFrom, cell.StraddleTo)
			g.Cells[iv][st] = cell
		}
	}
	return g
}
