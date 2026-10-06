package trading

import (
	"math"
	"testing"
	"time"
)

func TestLUTStraddleIV_RoundTrip(t *testing.T) {
	S, K, T := 22547.25, 22550.0, 1.091/365
	want := 0.1768
	x := lutBSPrice(true, S, K, T, want) + lutBSPrice(false, S, K, T, want)
	iv, ok := lutStraddleIV(x, S, K, T)
	if !ok || math.Abs(iv-want) > 1e-6 {
		t.Fatalf("iv %v ok %v, want %v", iv, ok, want)
	}
	if _, ok := lutStraddleIV(-5, S, K, T); ok {
		t.Fatal("negative straddle must not solve")
	}
}

func TestLUTManual_ApplyDaily(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	base := LUTDailyInputs{PrevStraddle: 252.75, PrevFuture: 22462.15, YesterdayIV: 0.1904, IDVPure: 0.1653, IDVWithPrev: 0.1722, NormalizedAdjChg: 0.0016}
	base.WeightedIDV = lutWeights[0]*base.IDVPure + lutWeights[1]*base.IDVWithPrev + lutWeights[2]*base.YesterdayIV

	// Nothing manual: live inputs untouched.
	in, ok, _ := LUTManual{}.applyDaily(base, true, "")
	if !ok || in != base {
		t.Fatalf("untouched inputs changed: %+v", in)
	}
	// One override: weighted IDV recomputed.
	in, ok, _ = LUTManual{IDVPure: f(0.20)}.applyDaily(base, true, "")
	wantW := lutWeights[0]*0.20 + lutWeights[1]*base.IDVWithPrev + lutWeights[2]*base.YesterdayIV
	if !ok || math.Abs(in.WeightedIDV-wantW) > 1e-12 {
		t.Fatalf("weighted %v want %v", in.WeightedIDV, wantW)
	}
	// CSV missing (zero inputs) + partial manual -> not usable.
	if _, ok, msg := (LUTManual{PrevStraddle: f(250)}).applyDaily(LUTDailyInputs{}, false, "no row"); ok || msg == "" {
		t.Fatal("incomplete manual daily inputs must not be usable")
	}
}

func TestLUTManual_Validate(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	now := lutTestNow()
	for _, bad := range []LUTManual{{Time: "09:10"}, {Time: "14:00"}, {Future: f(-1)}, {BuildIV: f(17)}} {
		if bad.validate(now) == nil {
			t.Fatalf("%+v should be rejected", bad)
		}
	}
	if err := (LUTManual{Time: "09:16", Future: f(22550), Straddle: f(180)}).validate(now); err != nil {
		t.Fatal(err)
	}
}

func lutTestNow() time.Time { return time.Date(2026, 10, 5, 8, 30, 0, 0, lutIST()) }

// During a minute the shown table is the NEXT check's (this minute is
// already recorded at its first tick).
func TestLUTNextCheck(t *testing.T) {
	for _, c := range [][2]int{{900, 900}, {915, 915}, {916, 917}, {917, 918}, {919, 920}, {959, 1000}, {1329, 1330}, {1330, 1330}, {1400, 1400}} {
		if got := lutNextCheck(c[0]); got != c[1] {
			t.Fatalf("lutNextCheck(%d)=%d want %d", c[0], got, c[1])
		}
	}
}

func TestLUTStore_SaveReload(t *testing.T) {
	t.Setenv("LUT_DATA_DIR", t.TempDir())
	day := "2026-10-06"
	lutSaveMinute(day, LUTEvaluation{Time: "09:16", Stage: "09:16", Underlying: 22577.3, BuildIV: 0.144, CoordText: "(0,2,5,2,1,0)"})
	lutSaveMinute(day, LUTEvaluation{Time: "09:17", Stage: "09:17", Underlying: 22583.75, BuildIV: 0.1374, Allowed: true})
	lutSaveDay(lutDayState{Day: day, U0916: 22577.3, OGSource: "captured live at 09:16:00", Entry: &LUTEntry{Time: "09:17", Strike: 22600}})
	mins, st := lutLoadDay(day)
	if len(mins) != 2 || mins[1].Time != "09:17" || !mins[1].Allowed || lutHHMM(mins[1].Time) != 917 {
		t.Fatalf("minutes %+v", mins)
	}
	if st.U0916 != 22577.3 || st.Entry == nil || st.Entry.Strike != 22600 {
		t.Fatalf("day state %+v", st)
	}
	if m, _ := lutLoadDay("2026-10-07"); len(m) != 0 {
		t.Fatal("another day must be empty")
	}
}
