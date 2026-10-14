package trading

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lutNear(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestParseLUTCSV_MappingsLikeReference(t *testing.T) {
	csvText := "DTE,IV_Ratio,Straddle_Ratio,Build_IV,Norm_OG_Gap,Adj_IV_Chg,Trade\n" +
		"1,<=0.65,<=0.95,<0.08,<-0.7501%,<=-0.008,YES\n" +
		"3,0.96-1.00,1.06-1.10,0.12-0.16,Between -0.7501% and 0.7501%,-0.008 to 0.004,YES\n" +
		"7,>1.30,>2.00,>=0.20,>0.7501%,>0.004,TRUE\n" +
		"2,0.66-0.80,0.96-1.00,0.08-0.12,<-0.7501%,<=-0.008,NO\n" +
		"9,bogus,row,x,y,z,YES\n"
	tb, err := ParseLUTCSV(strings.NewReader(csvText), lutSchemaOld)
	if err != nil {
		t.Fatal(err)
	}
	if tb.Yes != 3 {
		t.Fatalf("yes cells = %d, want 3", tb.Yes)
	}
	for _, c := range []LUTCoord{{0, 0, 0, 0, 0, 0}, {2, 4, 3, 2, 1, 1}, {6, 9, 10, 4, 2, 2}} {
		if !tb.Allowed(c) {
			t.Fatalf("%v should be allowed", c)
		}
	}
	if tb.Allowed(LUTCoord{1, 1, 1, 1, 0, 0}) {
		t.Fatal("NO row must not be allowed")
	}
}

// The real profiler tables (old and new set): every YES row must land in a
// distinct cell.
func TestLoadLUTSet_RealTables(t *testing.T) {
	for _, sc := range []*lutSchema{lutSchemaOld, lutSchemaNew} {
		dir := sc.DefaultDir
		if _, err := os.Stat(filepath.Join(dir, lutStageFiles[0])); err != nil {
			t.Skip("profiler tables not mounted")
		}
		set, err := LoadLUTSet(dir, sc)
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range lutStageFiles {
			f, _ := os.Open(filepath.Join(dir, name))
			rows, _ := csv.NewReader(f).ReadAll()
			f.Close()
			tc := len(rows[0]) - 1
			for j, h := range rows[0] {
				if strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")) == "Trade" {
					tc = j
				}
			}
			yes := 0
			for _, r := range rows[1:] {
				if tc < len(r) && strings.EqualFold(strings.TrimSpace(r[tc]), "YES") {
					yes++
				}
			}
			if set[i].Yes != yes {
				t.Fatalf("%s %s: %d YES cells loaded, %d YES rows in file", sc.Key, name, set[i].Yes, yes)
			}
		}
		if sc == lutSchemaNew {
			want := [5]int{1298, 3309, 4534, 3828, 2441} // backtest r46V2
			for i := range want {
				if set[i].Yes != want[i] {
					t.Fatalf("new %s: %d YES, backtest has %d", lutStageFiles[i], set[i].Yes, want[i])
				}
			}
		}
	}
}

// New set: straddle veto and Build-IV floor before the table, event days
// only for the new set, IV change from Final_IV_Chg.
func TestEvaluateLUTMinute_NewSetRules(t *testing.T) {
	newSet := func() LUTSet {
		var s LUTSet
		for i := range s {
			s[i] = &LUTTable{schema: lutSchemaNew, cells: make([]bool, lutSchemaNew.cellCount())}
			for j := range s[i].cells {
				s[i].cells[j] = true // every cell YES: only the rules can say no
			}
		}
		return s
	}
	set := newSet()
	S, K, raw := 22660.0, 22650.0, 4.9
	m := LUTMinuteInput{HHMM: 917, Underlying: S, RawDTE: raw, BusDays: 5, CELTP: 1}
	m.PELTP = lutBSPrice(false, S, K, raw/365, 0.13)
	in := LUTDailyInputs{PrevStraddle: 0, YesterdayIV: 0.12, NormalizedAdjChg: 0.01, FinalAdjChg: -0.01, HasFinalChg: true, WeightedIDV: 0.127}
	in.PrevStraddle = 1.10 * (m.PELTP + m.CELTP) // ratio 1.10 < veto 1.15 (DTE row 4)
	ev := EvaluateLUTMinute(set, in, 0, false, m)
	if !ev.Allowed || ev.Tables != "new" || ev.AdjIVChg != -0.01 || ev.Coord[5] != 0 {
		t.Fatalf("new set: %+v", ev)
	}
	in.PrevStraddle = 1.16 * (m.PELTP + m.CELTP)
	if ev := EvaluateLUTMinute(set, in, 0, false, m); ev.Allowed || !strings.HasPrefix(ev.Skip, "straddle veto") || ev.TableSays != "YES" {
		t.Fatalf("veto: %+v", ev)
	}
	in.PrevStraddle = 1.10 * (m.PELTP + m.CELTP)
	if ev := EvaluateLUTMinute(set, in, 0, true, m); ev.Allowed || ev.Skip == "" {
		t.Fatalf("event day 09:17 must skip on the new set: %+v", ev)
	}
	// 1 DTE: Build IV floor 0.12.
	m1 := m
	m1.RawDTE, m1.BusDays = 0.9, 1
	m1.PELTP = lutBSPrice(false, S, K, 0.9/365, 0.10)
	in.PrevStraddle = 1.2 * (m1.PELTP + m1.CELTP)
	if ev := EvaluateLUTMinute(set, in, 0, false, m1); ev.Allowed || !strings.HasPrefix(ev.Skip, "Build IV floor") {
		t.Fatalf("floor: %+v", ev)
	}
	// Old set: no veto, no floor, no events, the old IV change.
	old := set
	for i := range old {
		old[i] = &LUTTable{schema: lutSchemaOld, cells: make([]bool, lutSchemaOld.cellCount())}
		for j := range old[i].cells {
			old[i].cells[j] = true
		}
	}
	in.PrevStraddle = 1.16 * (m.PELTP + m.CELTP)
	if ev := EvaluateLUTMinute(old, in, 0, true, m); !ev.Allowed || ev.AdjIVChg != 0.01 || ev.Tables != "old" {
		t.Fatalf("old set: %+v", ev)
	}
}

func TestLUTLoadEventDays(t *testing.T) {
	if _, err := os.Stat(lutEventDatesDefault); err != nil {
		t.Skip("EventDates.xlsx not mounted")
	}
	ev, err := lutLoadEventDays(lutEventDatesDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) < 50 || ev["2026-10-14"] {
		t.Fatalf("%d event days, 2026-10-14 listed: %v", len(ev), ev["2026-10-14"])
	}
}

func TestBucketRight_IsSearchsortedRight(t *testing.T) {
	th := []float64{0.0801, 0.1201}
	cases := map[float64]int{0.05: 0, 0.0801: 1, 0.1: 1, 0.1201: 2, 0.5: 2}
	for x, want := range cases {
		if got := bucketRight(th, x); got != want {
			t.Fatalf("bucket(%v)=%d want %d", x, got, want)
		}
	}
}

func TestImpliedVol_RoundTrip(t *testing.T) {
	S, K, T := 22648.95, 22650.0, 5.5/365
	for _, sig := range []float64{0.08, 0.12, 0.25} {
		for _, call := range []bool{true, false} {
			p := lutBSPrice(call, S, K, T, sig)
			iv, ok := lutImpliedVol(call, p, S, K, T)
			if !ok || !lutNear(iv, sig, 1e-4) {
				t.Fatalf("iv(%v,%v)=%v ok=%v", sig, call, iv, ok)
			}
		}
	}
}

// 2025-12-31 09:25:29, expiry 2026-03-30 on the 09:15-15:40 session
// (385 min): 89 + (385 - 10m29s)/385 = 89.97277. (The old 15:30 close
// gave the reference tick data's 89.97204.)
func TestRawDTE_MatchesTickData(t *testing.T) {
	loc := lutIST()
	now := time.Date(2025, 12, 31, 9, 25, 29, 0, loc)
	exp := time.Date(2026, 3, 30, 0, 0, 0, 0, loc)
	if got := lutRawDTE(now, exp); !lutNear(got, 89.97277, 0.0001) {
		t.Fatalf("raw DTE = %v, want 89.97277", got)
	}
}

func TestLoadLUTDailyInputs_NormalisedChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "d.csv")
	content := "Date,PrevDate,Prev2Date,Future,Straddle,Adj_IV,Prev2_IV,Prev2_Adj_IV,IV_Chg,Adj_IV_Chg,IDV_Pure,IDV_With_Prev\n" +
		"2026-10-01,2026-09-30,2026-09-29,22649.75,274.75,0.1186,0.1218,0.1218,-0.0033,-0.0033,0.1297,0.1308\n" +
		"2026-10-05,2026-10-02,2026-09-30,22700,300,0.12,0.1186,0.1190,-0.002,-0.004,0.13,0.131\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := LoadLUTDailyInputs(p, "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	// No gap (09-29 -> 09-30): IV_Chg * 0.1 / Prev2_IV.
	if !lutNear(in.NormalizedAdjChg, -0.0033*0.1/0.1218, 1e-9) {
		t.Fatalf("norm adj chg = %v", in.NormalizedAdjChg)
	}
	if !lutNear(in.WeightedIDV, 0.2*0.1297+0.5*0.1308+0.3*0.1186, 1e-9) {
		t.Fatalf("weighted idv = %v", in.WeightedIDV)
	}
	// Gap (09-30 -> 10-02 is 2 days): Adj_IV_Chg * 0.1 / Prev2_Adj_IV.
	in, err = LoadLUTDailyInputs(p, "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if !lutNear(in.NormalizedAdjChg, -0.004*0.1/0.1190, 1e-9) {
		t.Fatalf("gap norm adj chg = %v", in.NormalizedAdjChg)
	}
	if _, err := LoadLUTDailyInputs(p, "2026-12-31"); err == nil {
		t.Fatal("missing date must be an error")
	}
}

func TestEvaluateLUTMinute_Rules(t *testing.T) {
	in := LUTDailyInputs{PrevStraddle: 274.75, PrevFuture: 22649.75, YesterdayIV: 0.1186, NormalizedAdjChg: -0.00271, WeightedIDV: 0.12692}
	S, K := 22660.0, 22650.0
	rawDTE := 4.9
	T := rawDTE / 365
	pe := lutBSPrice(false, S, K, T, 0.12) // underlying above strike -> PE is OTM
	m := LUTMinuteInput{HHMM: 917, Underlying: S, RawDTE: rawDTE, BusDays: 5, PELTP: pe, CELTP: 1}

	// Find the coordinate, then allow exactly it in the 09:17 table.
	var set LUTSet
	for i := range set {
		set[i] = &LUTTable{schema: lutSchemaOld, cells: make([]bool, lutSchemaOld.cellCount())}
	}
	ev := EvaluateLUTMinute(set, in, 0.03, false, m)
	if ev.Skip != "" || ev.Allowed {
		t.Fatalf("unexpected %+v", ev)
	}
	if !lutNear(ev.BuildIV, 0.12, 1e-4) || ev.Strike != K {
		t.Fatalf("build iv %v strike %v", ev.BuildIV, ev.Strike)
	}
	set[1].cells[lutSchemaOld.index(ev.Coord)] = true
	if ev2 := EvaluateLUTMinute(set, in, 0.03, false, m); !ev2.Allowed || ev2.Stage != "09:17" {
		t.Fatalf("should be allowed at 09:17: %+v", ev2)
	}
	// Same coordinate at 09:18 uses the 09:18 table -> not allowed.
	m18 := m
	m18.HHMM = 918
	if ev3 := EvaluateLUTMinute(set, in, 0.03, false, m18); ev3.Allowed {
		t.Fatal("09:18 must use its own table")
	}
	// Event day: the old set never used event days (the new one does,
	// TestEvaluateLUTMinute_NewSetRules).
	if ev4 := EvaluateLUTMinute(set, in, 0.03, true, m); !ev4.Allowed {
		t.Fatal("old set: event flag must not change the answer")
	}
	// 1 DTE: stop after 09:59.
	m1 := m
	m1.RawDTE, m1.HHMM = 0.6, 1000
	m1.PELTP = lutBSPrice(false, S, K, 0.6/365, 0.12)
	if ev5 := EvaluateLUTMinute(set, in, 0.03, false, m1); !ev5.StopScanning {
		t.Fatalf("1 DTE after 09:59 must stop: %+v", ev5)
	}
	// After 13:30: stop.
	m2 := m
	m2.HHMM = 1331
	if ev6 := EvaluateLUTMinute(set, in, 0.03, false, m2); !ev6.StopScanning {
		t.Fatal("after 13:30 must stop")
	}
}

func TestLUTTPBps_Interpolation(t *testing.T) {
	// DTE idx 0: low 16, high 30. Build IV 0.14 -> fraction 0.5 -> 23.
	if got := lutTPBps(0, 0.14); got != 23 {
		t.Fatalf("tp = %v, want 23", got)
	}
	// Build IV 0.08 -> low.
	if got := lutTPBps(5, 0.1399); math.Abs(got-(8+0.0599/0.12*7)) > 1e-9 { // 11.494, not rounded to 12
		t.Fatalf("TP not exact: %v", got)
	}
	if got := lutTPBps(4, 0.08); got != 8 {
		t.Fatalf("tp = %v, want 8", got)
	}
}

func TestLUTNormOG(t *testing.T) {
	got := LUTNormOG(22700, 22649.75, 0.1186)
	want := ((22700 - 22649.75) / 22649.75 * 100) * (0.19 / 0.1186)
	if !lutNear(got, want, 1e-12) {
		t.Fatalf("norm og %v want %v", got, want)
	}
}

func TestLUTNearestYes_TranslatesCellToMarketLevels(t *testing.T) {
	tb := &LUTTable{schema: lutSchemaOld, cells: make([]bool, lutSchemaOld.cellCount())}
	// DTE idx 4, IV ratio 0.96-1.00 (idx 4), str 1.06-1.10 (idx 3), build 0.12-0.16 (idx 2), OG mid, ADJ mid.
	c := LUTCoord{4, 4, 3, 2, 1, 1}
	tb.cells[lutSchemaOld.index(c)] = true
	// An unreachable cell: IV ratio <=0.65 with build >= 0.20 at IDV 0.127.
	tb.cells[lutSchemaOld.index(LUTCoord{4, 0, 3, 4, 1, 1})] = true

	W, adj, prev := 0.127, 1.0, 274.75
	got := lutNearestYes(tb, "09:20+", 4, 1, 1, W, adj, prev, 0.123, 250, 0)
	if len(got) != 1 {
		t.Fatalf("got %d conditions, want 1 (unreachable cell dropped): %+v", len(got), got)
	}
	g := got[0]
	// IV ratio [0.9501,1.0001) x W intersected with build [0.1201,0.1601).
	if !lutNear(g.BuildIVFrom, math.Max(0.9501*W, 0.1201), 1e-9) || !lutNear(g.BuildIVTo, math.Min(1.0001*W, 0.1601), 1e-9) {
		t.Fatalf("build IV range %v-%v", g.BuildIVFrom, g.BuildIVTo)
	}
	// Straddle ratio [1.0501,1.1001) -> straddle in (prev/1.1001, prev/1.0501].
	if !lutNear(g.StraddleFrom, prev/1.1001, 1e-9) || !lutNear(g.StraddleTo, prev/1.0501, 1e-9) {
		t.Fatalf("straddle range %v-%v", g.StraddleFrom, g.StraddleTo)
	}
	if !g.IVOK || !g.StraddleOK || g.Distance != 0 {
		t.Fatalf("0.123 / 250 should already be inside: %+v", g)
	}
	// Weekend adjustment divides the raw IV bounds by the factor.
	got2 := lutNearestYes(tb, "09:20+", 4, 1, 1, W, 1.1, prev, 0.123, 250, 0)
	if !lutNear(got2[0].BuildIVFrom, g.BuildIVFrom/1.1, 1e-9) {
		t.Fatalf("adj factor not applied: %v", got2[0].BuildIVFrom)
	}
}

func TestLUTUnderlying_AlwaysSynthetic(t *testing.T) {
	c := &OptionChainSnapshot{FutureLtp: 22712.6, SyntheticFuture: 22620}
	if got := lutUnderlying(c); got != 22620 {
		t.Fatalf("got %v, want synthetic 22620 (future_ltp never used)", got)
	}
}
