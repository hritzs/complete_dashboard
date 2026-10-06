package trading

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

// ATM moved 22550 -> 22650 and 22550 left the chain window: its legs keep
// their last known mark / delta, so net delta and PnL stay whole-position.
func TestPMS_StrikeLeavesChain_KeepsLastGreeks(t *testing.T) {
	p := NewPMS()
	p.ApplyFill(PMSFill{Token: 1, Strike: 22550, OptionType: "CE", Side: "SELL", Qty: 65, Price: 100, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 2, Strike: 22550, OptionType: "PE", Side: "SELL", Qty: 65, Price: 90, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 5, Strike: 22650, OptionType: "CE", Side: "SELL", Qty: 65, Price: 70, Role: "BUILD"})
	p.ApplyFill(PMSFill{Token: 6, Strike: 22650, OptionType: "PE", Side: "SELL", Qty: 65, Price: 110, Role: "BUILD"})
	both := &OptionChainSnapshot{Chain: []OptionChainRow{
		{Strike: 22550, CEToken: 1, PEToken: 2, CEAsk: 120, PEAsk: 70, CEDelta: 0.7, PEDelta: -0.3},
		{Strike: 22650, CEToken: 5, PEToken: 6, CEAsk: 72, PEAsk: 108, CEDelta: 0.5, PEDelta: -0.5},
	}}
	full := p.View(both)
	onlyNew := &OptionChainSnapshot{Chain: []OptionChainRow{both.Chain[1]}}
	v := p.View(onlyNew)
	if v.MissingMarks != 2 {
		t.Fatalf("missing marks %d, want 2", v.MissingMarks)
	}
	if math.Abs(v.NetDelta-full.NetDelta) > 1e-9 || math.Abs(v.PnL-full.PnL) > 1e-9 {
		t.Fatalf("lost the old strike: delta %v vs %v, pnl %v vs %v", v.NetDelta, full.NetDelta, v.PnL, full.PnL)
	}
	if v.BuildCE != 130 || v.BuildPE != 130 {
		t.Fatalf("build %d/%d across strikes, want 130/130", v.BuildCE, v.BuildPE)
	}
}

func sbTestDir(t *testing.T) {
	t.Setenv("SBUILD_RULES_PATH", filepath.Join(t.TempDir(), "sbuild_rules.json"))
}

func sbTestRun(date string) *sbRunner {
	r := newSBRunner(sbDefaults(SBConfig{ID: "RTEST", Symbol: "NIFTY", TargetStraddle: 180, Straddles: 260}))
	r.phase, r.date, r.startedAt, r.lotSize, r.tranches = "BUILDING", date, time.Now(), 65, 2
	r.strikes, r.buildATM = []float64{22550, 22600}, 22600
	r.pms.ApplyFill(PMSFill{Token: 1, Strike: 22550, OptionType: "CE", Side: "SELL", Qty: 65, Price: 100, Role: "BUILD", Tranche: 1})
	r.pms.ApplyFill(PMSFill{Token: 2, Strike: 22550, OptionType: "PE", Side: "SELL", Qty: 65, Price: 90, Role: "BUILD", Tranche: 1})
	r.pms.ApplyFill(PMSFill{Token: 3, Strike: 22600, OptionType: "CE", Side: "SELL", Qty: 65, Price: 80, Role: "BUILD", Tranche: 2})
	r.pms.ApplyFill(PMSFill{Token: 4, Strike: 22600, OptionType: "PE", Side: "SELL", Qty: 130, Price: 95, Role: "BUILD", Tranche: 2})
	r.pms.ApplyFill(PMSFill{Token: 4, Strike: 22600, OptionType: "PE", Side: "BUY", Qty: 65, Price: 96, Role: "HEDGE"})
	return r
}

// Restart mid-build: the run comes back BUILDING with the same position,
// strikes and counters, and re-runs the minute-end checks first.
func TestSBRun_RestartResumesWhereItLeftOff(t *testing.T) {
	sbTestDir(t)
	a := sbTestRun(sbToday())
	a.mu.Lock()
	a.persistLocked()
	before := a.pms.View(nil)
	a.mu.Unlock()

	b := newSBRunner(a.cfg)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restoreLocked()
	after := b.pms.View(nil)
	if b.phase != "BUILDING" || b.tranches != 2 || b.lotSize != 65 || len(b.strikes) != 2 {
		t.Fatalf("restored phase %s tranches %d lot %d strikes %v", b.phase, b.tranches, b.lotSize, b.strikes)
	}
	if after.BuildCE != before.BuildCE || after.BuildPE != before.BuildPE || len(b.pms.Fills()) != 5 {
		t.Fatalf("PMS not rebuilt: %d/%d (want %d/%d), %d fills", after.BuildCE, after.BuildPE, before.BuildCE, before.BuildPE, len(b.pms.Fills()))
	}
	for i, l := range after.Legs {
		if l.Qty != before.Legs[i].Qty || math.Abs(l.AvgPrice-before.Legs[i].AvgPrice) > 1e-9 || math.Abs(l.Realized-before.Legs[i].Realized) > 1e-9 {
			t.Fatalf("leg %d differs after restart: %+v vs %+v", i, l, before.Legs[i])
		}
	}
	if b.lastMinute != 0 || b.resumedAt.IsZero() {
		t.Fatal("minute-end checks must re-run on the first tick after a restart")
	}
	if n := len(b.events); n == 0 || b.events[n-1].Kind != "RESUME" {
		t.Fatalf("no RESUME event: %+v", b.events)
	}
}

func TestSBRun_PreviousDayNotResumed(t *testing.T) {
	sbTestDir(t)
	a := sbTestRun("2000-01-01")
	a.mu.Lock()
	a.persistLocked()
	a.mu.Unlock()
	b := newSBRunner(a.cfg)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restoreLocked()
	if b.phase != "STOPPED" {
		t.Fatalf("previous-day run phase %s, want STOPPED", b.phase)
	}
}

// Fully built before the restart -> comes back COMPLETE (monitors only).
func TestSBRun_RestartCompleteWhenNothingLeft(t *testing.T) {
	sbTestDir(t)
	a := sbTestRun(sbToday())
	a.cfg.Straddles = 130 // target 260 = CE 130 + PE 195 already sold
	a.mu.Lock()
	a.persistLocked()
	a.mu.Unlock()
	b := newSBRunner(a.cfg)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restoreLocked()
	if b.phase != "COMPLETE" {
		t.Fatalf("phase %s, want COMPLETE", b.phase)
	}
}

func TestSBRules_SaveLoadRoundTrip(t *testing.T) {
	sbTestDir(t)
	in := []SBConfig{
		sbDefaults(SBConfig{ID: "RA", Symbol: "NIFTY", TargetStraddle: 183, Straddles: 130}),
		sbDefaults(SBConfig{ID: "RB", Symbol: "BANKNIFTY", TargetStraddle: 640, Straddles: 60}),
	}
	if err := saveSBRules(in); err != nil {
		t.Fatal(err)
	}
	out := LoadSBRules()
	if len(out) != 2 || out[0].ID != "RA" || out[1].Symbol != "BANKNIFTY" || out[1].TargetStraddle != 640 {
		t.Fatalf("rules %+v", out)
	}
}
