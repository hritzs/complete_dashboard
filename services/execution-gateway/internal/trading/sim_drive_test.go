package trading

import (
	"math"
	"testing"
	"time"
)

// The live hedge is sized at the simulation's future, not the snapshot's:
// live 2026-10-16 10:03:06.989 (snapshot fut 23025.25, sim tick 23033.55).
func TestLiveDeltaAtFuture(t *testing.T) {
	snap := TradeSnapshot{NetDelta: -1658.176, NetGamma: -34.12201, Underlying: 23025.25, PointsOut: 48.6}
	d, out := liveDeltaAtFuture(snap, 23033.55)
	if d > -1941 || d < -1942 || out < 56.8 || out > 57.0 {
		t.Fatalf("at sim fut: delta %.1f out %.2f, want -1941.4 / 56.9", d, out)
	}
	if d, out := liveDeltaAtFuture(snap, 0); d != snap.NetDelta || out != snap.PointsOut {
		t.Fatalf("no sim fut: got %.1f / %.1f, want the snapshot's own numbers", d, out)
	}
}

// DTE rows 0 / 1 (delta band 0.25 / 0.20): a minute-end hedge leaves
// |delta| = 25 % x band x straddle units, at the simulation's future, with
// no bps floor; intra-minute takes the complete delta. Points band rows
// are unchanged.
func TestSimDriveSizing_DeltaBandRows(t *testing.T) {
	const Q = 12480
	// Gamma -120 (expiry-day sized), snapshot 5 points behind the sim.
	snap := TradeSnapshot{NetDelta: 4000, NetGamma: -120, Underlying: 23000, PointsOut: 33.3, PointsAllowed: 48}
	minute := PaperSimHedge{Kind: "RISK_BREACH", Future: 22995}
	for _, band := range []float64{0.25, 0.20} {
		delta, frac, _ := simDriveSizing(snap, minute, hedgeRuleNew, band, Q, 0, 8)
		if math.Abs(delta-4600) > 1e-6 {
			t.Fatalf("band %.2f: delta %.1f, want 4600 (re-valued at the sim's future)", band, delta)
		}
		left := delta * (1 - frac)
		if want := hedgeLeaveBand * band * Q; math.Abs(left-want) > 1e-6 {
			t.Fatalf("band %.2f: leaves %.1f delta, want %.1f", band, left, want)
		}
	}
	// Intra-minute on a band row: the complete delta.
	if _, frac, _ := simDriveSizing(snap, PaperSimHedge{Kind: "INTRA_MINUTE", Future: 22995}, hedgeRuleNew, 0.25, Q, 0, 8); frac != 1 {
		t.Fatalf("intra frac %.3f, want 1", frac)
	}
	// Points band row: leaves 25 % of max(allowed, bps floor) in points.
	delta, frac, _ := simDriveSizing(snap, minute, hedgeRuleNew, 0, Q, 0, 8)
	if got, want := math.Abs(delta*(1-frac)/snap.NetGamma), hedgeLeaveBand*48; math.Abs(got-want) > 1e-6 {
		t.Fatalf("points band leaves %.2f points, want %.2f", got, want)
	}
	// Old rule: the complete delta at minute end.
	if _, frac, _ := simDriveSizing(snap, minute, hedgeRuleOld, 0.25, Q, 0, 8); frac != 1 {
		t.Fatalf("old rule frac %.3f, want 1", frac)
	}
}

// A partial delta-neutral build keeps sizing on the given straddles scaled
// by the filled share, not min(CE, PE) / lot size.
func TestPartialFilledStraddles(t *testing.T) {
	cases := []struct {
		name                            string
		lots, ceQ, peQ, reqCE, reqPE, w int
	}{
		{"full delta-neutral build", 192, 11635, 13325, 11635, 13325, 192},
		{"half filled", 192, 5850, 6695, 11635, 13325, 96},
		{"CE short of its share", 192, 5850, 13325, 11635, 13325, 96},
		{"one leg only", 192, 0, 6695, 11635, 13325, 96},
		{"no requested quantities", 192, 5850, 6695, 0, 0, 90},
	}
	for _, c := range cases {
		if got := partialFilledStraddles(c.lots, 65, c.ceQ, c.peQ, c.reqCE, c.reqPE); got != c.w {
			t.Fatalf("%s: %d straddle lots, want %d", c.name, got, c.w)
		}
	}
}

// The driver owns its trade until released; a stale run is still owned
// (no fallback to the monitor's own rules) but not fresh.
func TestSimDriveOwnsAndRelease(t *testing.T) {
	simDrive.mu.Lock()
	simDrive.d = &simDriver{TradeUID: "T1", LastRun: time.Now(), seen: map[string]bool{}}
	simDrive.mu.Unlock()
	if o, f := simDriveOwns("T1"); !o || !f {
		t.Fatal("fresh driver must own and be fresh")
	}
	if o, _ := simDriveOwns("T2"); o {
		t.Fatal("other trade not owned")
	}
	simDrive.mu.Lock()
	simDrive.d.LastRun = time.Now().Add(-2 * simDriveStale)
	simDrive.mu.Unlock()
	if o, f := simDriveOwns("T1"); !o || f {
		t.Fatal("stale driver still owns, not fresh")
	}
	simDriveRelease("T1", "test")
	if o, _ := simDriveOwns("T1"); o {
		t.Fatal("released driver must not own")
	}
}

// A contract holding both wing and hedge fills: realized and the open
// entry of each part come from its own orders only.
func TestLegFlowsWingSplit(t *testing.T) {
	// Hedge: sold 1300 @ 40, bought back 650 @ 30 -> realized +6500, open short 650 @ 40.
	real := legFlows{sellQ: 1300, sellV: 1300 * 40, buyQ: 650, buyV: 650 * 30}
	// Wing: bought 1300 @ 5 earlier, still held.
	wing := legFlows{buyQ: 1300, buyV: 1300 * 5}
	if got := real.realized(); math.Abs(got-6500) > 1e-9 {
		t.Fatalf("hedge realized %.2f, want 6500", got)
	}
	if real.entry() != 40 || wing.entry() != 5 || wing.realized() != 0 {
		t.Fatalf("entries hedge %.2f wing %.2f (want 40 / 5), wing realized %.2f (want 0)", real.entry(), wing.entry(), wing.realized())
	}
	leg := OpenLeg{EntryPrice: 22.5, WingQty: 1300, Qty: 650, WingEntry: wing.entry(), RealEntry: real.entry()}
	if leg.entryFor(true) != 5 || leg.entryFor(false) != 40 {
		t.Fatalf("entryFor wing %.2f non-wing %.2f, want 5 / 40", leg.entryFor(true), leg.entryFor(false))
	}
	if plain := (OpenLeg{EntryPrice: 22.5}); plain.entryFor(true) != 22.5 || plain.entryFor(false) != 22.5 {
		t.Fatal("a leg without a split keeps its blended entry")
	}
}

// A sim-driven LUT trade built at the LUT's first YES is the LUT table's own
// simulation: it points there instead of running a second identical one.
// Anything that changes the outcome keeps its own table.
func TestSameSimAs(t *testing.T) {
	lut := PaperSimResult{Status: "OPEN", Strike: 22950, TPBps: 20,
		Config: paperDefaults(PaperSimConfig{ID: "LUT-NEW", Entry: "09:17", HedgeMode: "synthetic", HedgeRule: "new"})}
	drive := PaperSimConfig{ID: "ACTUAL-T", Entry: "09:17", Lots: 192, HedgeMode: "synthetic", ExitTime: "15:37:00",
		SLBps: 14, TPBps: 20, Strike: 22950, HedgeRule: "new"}
	if got := sameSimAs([]PaperSimResult{lut}, drive, true); got != "LUT-NEW" {
		t.Fatalf("identical setup: got %q, want LUT-NEW", got)
	}
	for name, mod := range map[string]func(*PaperSimConfig){
		"other entry":  func(c *PaperSimConfig) { c.Entry = "09:42" },
		"other strike": func(c *PaperSimConfig) { c.Strike = 23000 },
		"other TP":     func(c *PaperSimConfig) { c.TPBps = 25 },
		"other exit":   func(c *PaperSimConfig) { c.ExitTime = "15:00" },
		"test band":    func(c *PaperSimConfig) { c.PointsAllowed = 30 },
		"old rule":     func(c *PaperSimConfig) { c.HedgeRule = "old" },
	} {
		c := drive
		mod(&c)
		if got := sameSimAs([]PaperSimResult{lut}, c, true); got != "" {
			t.Fatalf("%s: got %q, want its own table", name, got)
		}
	}
	if got := sameSimAs([]PaperSimResult{lut}, drive, false); got != "" {
		t.Fatalf("rupee view with other lots: got %q, want its own table", got)
	}
}
