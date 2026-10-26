package trading

import (
	"math"
	"testing"
	"time"
)

// A fired LUT build samples its strike's bids once a second for 30 s from
// the fire and keeps the mean; a second tick in the same second is ignored.
func TestLUTEntryAvgCapture(t *testing.T) {
	t.Setenv("LUT_DATA_DIR", t.TempDir())
	day := "2026-10-26"
	run := &LUTBuildRun{Status: "FIRING", Minute: "09:16", FiredAt: "09:16:01.743", Strike: 22500}
	e := &lutEngine{day: day, build: run}
	t0, _ := time.ParseInLocation("2006-01-02 15:04:05.000", day+" 09:16:01.743", lutIST())
	chainAt := func(ce, pe float64) *OptionChainSnapshot {
		return &OptionChainSnapshot{Chain: []OptionChainRow{{Strike: 22500, CEBid: ce, PEBid: pe}, {Strike: 22550, CEBid: 1, PEBid: 1}}}
	}
	e.noteEntryAvgLocked(t0.Add(-time.Second), day, chainAt(500, 500)) // before the fire: not taken
	for s := 0; s < lutEntryAvgSecs; s++ {
		at := t0.Add(time.Duration(s) * time.Second)
		e.noteEntryAvgLocked(at, day, chainAt(140+float64(s), 120-float64(s)))
		e.noteEntryAvgLocked(at.Add(100*time.Millisecond), day, chainAt(999, 999)) // same second: ignored
	}
	if run.EntryAvgN != 30 || math.Abs(run.EntryAvgCE-154.5) > 1e-9 || math.Abs(run.EntryAvgPE-105.5) > 1e-9 {
		t.Fatalf("avg CE %.4f PE %.4f n %d", run.EntryAvgCE, run.EntryAvgPE, run.EntryAvgN)
	}
	if run.EntryAvgFrom != "09:16:01" || run.EntryAvgTo != "09:16:30" {
		t.Fatalf("window %s-%s", run.EntryAvgFrom, run.EntryAvgTo)
	}
	// the driving simulation enters at it
	cfg := simDriveConfigOf(*run, 22500, "new")
	if cfg.EntryCE != run.EntryAvgCE || cfg.EntryPE != run.EntryAvgPE {
		t.Fatalf("driver config %+v", cfg)
	}
	// no bids at all: tried, minute close kept
	r2 := &LUTBuildRun{Status: "FIRING", Minute: "09:17", FiredAt: "09:17:01.000", Strike: 22450}
	e2 := &lutEngine{day: day, build: r2}
	t1, _ := time.ParseInLocation("2006-01-02 15:04:05.000", day+" 09:17:01.000", lutIST())
	e2.noteEntryAvgLocked(t1.Add(time.Second), day, chainAt(1, 1))
	e2.noteEntryAvgLocked(t1.Add(lutEntryAvgWait+time.Second), day, chainAt(1, 1))
	if r2.EntryAvgN != -1 || simDriveConfigOf(*r2, 22450, "new").EntryCE != 0 {
		t.Fatalf("no-bid capture %+v", r2)
	}
}

// The simulation's entry takes the given prices instead of the minute close.
func TestPaperSim_EntryOverride(t *testing.T) {
	mins := mkMinutes(5, func(i int) float64 { return 22600 })
	base := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "off", SLBps: 5000, TPBps: 5000}, 65, nil)
	over := RunPaperSim(mins, PaperSimConfig{Entry: "09:16", Lots: 1, HedgeMode: "off", SLBps: 5000, TPBps: 5000,
		EntryCE: base.CEEntry + 2, EntryPE: base.PEEntry - 1, EntryNote: "test"}, 65, nil)
	if over.CEEntry != base.CEEntry+2 || over.PEEntry != base.PEEntry-1 || math.Abs(over.EntryStraddle-(base.EntryStraddle+1)) > 1e-9 {
		t.Fatalf("entry %v/%v vs %v/%v", over.CEEntry, over.PEEntry, base.CEEntry, base.PEEntry)
	}
	if math.Abs(over.PnLPerStraddle-(base.PnLPerStraddle+1)) > 1e-6 {
		t.Fatalf("P&L moves by the extra credit: %.4f vs %.4f", over.PnLPerStraddle, base.PnLPerStraddle)
	}
}

func TestLUTNearestExpiry(t *testing.T) {
	c := &OptionChainSnapshot{Expiry: "03-NOV-26", AvailableExpiries: []string{"03-NOV-26", "27-OCT-26", "10-NOV-26"}}
	if got := lutNearestExpiry(c); got != "27-OCT-26" {
		t.Fatalf("nearest %s", got)
	}
}
