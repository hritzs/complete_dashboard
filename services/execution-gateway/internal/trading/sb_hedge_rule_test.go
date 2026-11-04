package trading

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSBDefaults_StraddleThreeIVThirtyEightLeaveHalf(t *testing.T) {
	c := sbDefaults(SBConfig{})
	if c.StraddleDiv != 3 || c.HedgeDiv != 38 || c.HedgeLeavePct != 50 {
		t.Fatalf("new rule: straddle / %g, IV / %g, leave %g%%", c.StraddleDiv, c.HedgeDiv, c.HedgeLeavePct)
	}
}

func TestSBDefaults_SavedRules(t *testing.T) {
	// Saved before Leave % existed, on the old defaults: moves over.
	c := sbDefaults(SBConfig{StraddleDiv: 4, HedgeDiv: 57})
	if c.StraddleDiv != 3 || c.HedgeDiv != 38 || c.HedgeLeavePct != 50 {
		t.Fatalf("old rule not moved: %g %g %g", c.StraddleDiv, c.HedgeDiv, c.HedgeLeavePct)
	}
	// Saved before Leave % existed with its own divisors: those stay.
	c = sbDefaults(SBConfig{StraddleDiv: 5, HedgeDiv: 40})
	if c.StraddleDiv != 5 || c.HedgeDiv != 40 || c.HedgeLeavePct != 50 {
		t.Fatalf("own divisors changed: %g %g %g", c.StraddleDiv, c.HedgeDiv, c.HedgeLeavePct)
	}
	// Set after the change: stays, 4 / 57 included, on every later load.
	c = sbDefaults(sbDefaults(SBConfig{StraddleDiv: 4, HedgeDiv: 57, HedgeLeavePct: 25}))
	if c.StraddleDiv != 4 || c.HedgeDiv != 57 || c.HedgeLeavePct != 25 {
		t.Fatalf("set values changed: %g %g %g", c.StraddleDiv, c.HedgeDiv, c.HedgeLeavePct)
	}
	if c = sbDefaults(SBConfig{HedgeLeavePct: 150}); c.HedgeLeavePct != 100 {
		t.Fatalf("leave %% above 100: %g", c.HedgeLeavePct)
	}
}

// Out 150, allowed 100, leave 50 %: hedge down to 50 points out, which is
// two thirds of the delta here and about half right at the trigger.
func TestHedgeLeavePct_LeavesThatShareOfAllowed(t *testing.T) {
	r := hedgeRuleOld.withHedgeLeavePct(50)
	if r.LeaveBand != 0.5 || !r.Intra {
		t.Fatalf("rule %+v", r)
	}
	if got, want := r.minuteFrac(150, 100), 1-50.0/150; math.Abs(got-want) > 1e-12 {
		t.Fatalf("share %g, want %g", got, want)
	}
	d := decideHedge(hedgeDecisionInput{PointsOut: 150, PointsAllowed: 100, Spot: 24000, NetDelta: -1300, LotSize: 65, TradeLots: 77, LeaveBand: r.LeaveBand})
	if !d.Hedge || d.Lots != 13 { // 866.67 of 1,300 = 13.33 lots, rounded down
		t.Fatalf("hedge %+v", d)
	}
	if got := r.minuteFrac(101, 100); math.Abs(got-(1-50.0/101)) > 1e-12 {
		t.Fatalf("at the trigger: %g", got)
	}
	if d := decideHedge(hedgeDecisionInput{PointsOut: 99, PointsAllowed: 100, Spot: 24000, NetDelta: -1300, LotSize: 65, TradeLots: 77, LeaveBand: r.LeaveBand}); d.Hedge {
		t.Fatal("no hedge inside the band")
	}
}

// A trade without Leave % -- every LUT trade -- sizes exactly as before, and
// the LUT's share is still the 25 % of hedgeMinuteFracFor.
func TestHedgeLeavePct_ZeroLeavesTheLUTRuleUnchanged(t *testing.T) {
	for _, rule := range []hedgeRule{hedgeRuleOld, hedgeRuleNew} {
		if r := rule.withHedgeLeavePct(0); r != rule {
			t.Fatalf("%s rule changed: %+v", rule.Key, r)
		}
	}
	for _, out := range []float64{105, 150, 300} {
		if a, b := hedgeRuleNew.minuteFrac(out, 100), hedgeMinuteFracFor(out, 100); math.Abs(a-b) > 1e-12 {
			t.Fatalf("LUT share at out %g: %g, was %g", out, a, b)
		}
	}
	if hedgeRuleOld.minuteFrac(150, 100) != 1 {
		t.Fatal("old rule must take the complete delta")
	}
	// The sim that drives a LUT trade carries no Leave % and its own divisors.
	c := simDriveConfigOf(LUTBuildRun{TradeUID: "T", Minute: "09:18"}, 23800, "new")
	if c.HedgeLeavePct != 0 || c.HedgeRule != "new" || paperDefaults(c).StraddleDiv != 4 || paperDefaults(c).HedgeDiv != 57 {
		t.Fatalf("LUT sim config changed: %+v", c)
	}
}

// Today's numbers: allowed = min(390 / 3, 23,900 x 0.16 / 38) = 100.6. Leave
// 50 % at minute end, the complete delta past 1.2 x allowed within the minute.
func TestHedgeLeavePct_MinuteLeavesHalfIntraComplete(t *testing.T) {
	straddle, spot, iv := 390.0, 23900.0, 0.16
	allowed := math.Min(straddle/3, spot*iv/38)
	if math.Abs(allowed-100.63) > 0.01 {
		t.Fatalf("allowed %.2f", allowed)
	}
	r := hedgeRuleNew.withHedgeLeavePct(50)
	in := hedgeDecisionInput{PointsOut: 110, PointsAllowed: allowed, Spot: spot, NetDelta: -1300, LotSize: 65, TradeLots: 77}
	minute := in
	minute.LeaveBand = r.LeaveBand
	if d := decideHedge(minute); !d.Hedge || d.Lots != 10 { // 54 % of 1,300 = 705, 10.85 lots, rounded down
		t.Fatalf("minute end: %+v", d)
	}
	intra := in
	intra.PointsOut, intra.Mult, intra.Frac = 125, hedgeIntraMult, hedgeIntraFrac
	if d := decideHedge(intra); !d.Hedge || d.Lots != 20 { // all 1,300
		t.Fatalf("intra-minute: %+v", d)
	}
	intra.PointsOut = 118 // below 1.2 x allowed = 120.76
	if d := decideHedge(intra); d.Hedge {
		t.Fatalf("intra-minute below the line: %+v", d)
	}
}

func TestApplyTradeHedgeLeavePct(t *testing.T) {
	tr := StoredTrade{TradeUID: "SB-RMW1LJ6RU2-261104093759"}
	if err := applyTradeHedgeLeavePct(&tr, 50); err != nil || tr.Config.HedgeLeavePct != 50 {
		t.Fatalf("%v, %g", err, tr.Config.HedgeLeavePct)
	}
	if err := applyTradeHedgeLeavePct(&tr, 101); err == nil || tr.Config.HedgeLeavePct != 50 {
		t.Fatalf("above 100 must be refused: %v, %g", err, tr.Config.HedgeLeavePct)
	}
	if err := applyTradeHedgeLeavePct(&tr, 0); err != nil || tr.Config.HedgeLeavePct != 0 {
		t.Fatalf("0 goes back to the standard rule: %v, %g", err, tr.Config.HedgeLeavePct)
	}
	simDrive.mu.Lock()
	was := simDrive.d
	simDrive.d = &simDriver{TradeUID: "TRD_LUT"}
	simDrive.mu.Unlock()
	defer func() {
		simDrive.mu.Lock()
		simDrive.d = was
		simDrive.mu.Unlock()
	}()
	lut := StoredTrade{TradeUID: "TRD_LUT"}
	if err := applyTradeHedgeLeavePct(&lut, 50); err == nil || lut.Config.HedgeLeavePct != 0 {
		t.Fatalf("a sim-driven LUT trade must refuse it: %v, %g", err, lut.Config.HedgeLeavePct)
	}
}

func TestSBHandOff_CarriesTheRulesHedge(t *testing.T) {
	s, r := handoffRunner(t)
	r.cfg.StraddleDiv, r.cfg.HedgeDiv, r.cfg.HedgeLeavePct = 3, 38, 50
	r.pms.ApplyFill(PMSFill{Token: 44620, Strike: 22700, OptionType: "CE", Side: "SELL", Qty: 130, Price: 168.3, Role: "BUILD"})
	r.pms.ApplyFill(PMSFill{Token: 44621, Strike: 22700, OptionType: "PE", Side: "SELL", Qty: 130, Price: 146.8, Role: "BUILD"})
	if !s.sbHandOffLocked(r) {
		t.Fatal("expected hand-off")
	}
	defer stopRuntime(s, "SB-T-1")
	tr, _ := s.Store.LoadTrade("SB-T-1")
	if tr.Config.StraddleDiv != 3 || tr.Config.HedgeDiv != 38 || tr.Config.HedgeLeavePct != 50 {
		t.Fatalf("hedge not carried: %g %g %g", tr.Config.StraddleDiv, tr.Config.HedgeDiv, tr.Config.HedgeLeavePct)
	}
}

type hedgeRuleChain struct{ breachChainSnapshot }

// ATM 23900 straddle 390, IV 16 %: allowed = min(130, 100.63) at 3 / 38.
func (hedgeRuleChain) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	return &OptionChainSnapshot{
		Symbol: "NIFTY", ATM: 23900, Expiry: "09-NOV-26", LotSize: 65,
		SyntheticSpot: 23900, SyntheticFuture: 23900,
		Chain: []OptionChainRow{{
			Strike: 23900, IsATM: true, CEToken: 111, PEToken: 222,
			CELtp: 195, PELtp: 195, CEDelta: 0.62, PEDelta: -0.38,
			CEGamma: 0.001, PEGamma: 0.001, CEIV: 16, PEIV: 16,
		}},
	}, nil
}

// The whole minute-end path: a trade's stored settings -> the monitor cycle ->
// the lots it asks to hedge and the rule its snapshot shows.
func monitorHedgeLots(t *testing.T, cfg MonitorConfig) (string, TradeSnapshot) {
	t.Helper()
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("SB-HEDGE-RULE")
	tr.CEQty, tr.PEQty, tr.Lots, tr.CELtp, tr.PELtp = 5005, 5005, 77, 195, 195
	tr.Config = cfg
	tr.Config.SquareOffHardTime = time.Now().Add(time.Hour)
	store.SaveTrade(tr)
	store.SaveRuntime(&RuntimeTrade{Trade: tr, StopCh: make(chan struct{}), DoneCh: make(chan struct{})})
	svc := &Service{Store: store, Snapshot: hedgeRuleChain{}, BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}}}
	out := captureLog(t, func() { svc.runMonitorCycle(tr.TradeUID) })
	snap, _ := store.LoadSnapshot(tr.TradeUID)
	return out, snap
}

func TestMonitorCycle_HedgeRuleEndToEnd(t *testing.T) {
	was, _ := lutDrivingTables.Load().(string)
	lutDrivingTables.Store("new")
	defer lutDrivingTables.Store(was)

	// Net delta -1,201, gamma -10.01: 120 points out.
	// Own rule, leave 50 % of 100.63: hedge 1 - 50.3/120 = 58 % = 696, 10 lots.
	out, snap := monitorHedgeLots(t, MonitorConfig{StraddleDiv: 3, HedgeDiv: 38, HedgeLeavePct: 50})
	if !strings.Contains(out, "hedge_lots=10 ") {
		t.Fatalf("own rule: want 10 lots\n%s", out)
	}
	if math.Abs(snap.PointsAllowed-100.63) > 0.01 || math.Abs(snap.PointsOut-120) > 0.01 {
		t.Fatalf("allowed %.2f out %.2f", snap.PointsAllowed, snap.PointsOut)
	}
	want := "Own rule: min(straddle / 3, IV / 38), minute end leave 50% of allowed, past 1.2 x allowed: full delta"
	if snap.HedgeRule != want {
		t.Fatalf("rule text %q", snap.HedgeRule)
	}

	// No Leave %: the standard rule, 4 / 57, leave 25 % of 67.09: 86 % = 1,033, 15 lots.
	out, snap = monitorHedgeLots(t, MonitorConfig{StraddleDiv: 4, HedgeDiv: 57})
	if !strings.Contains(out, "hedge_lots=15 ") {
		t.Fatalf("standard rule: want 15 lots\n%s", out)
	}
	if snap.HedgeRule != "Standard: min(straddle / 4, IV / 57), minute end leave 25% of allowed, past 1.2 x allowed: full delta" {
		t.Fatalf("rule text %q", snap.HedgeRule)
	}
}

func TestHedgeRuleText_LUTAndOldTables(t *testing.T) {
	was, _ := lutDrivingTables.Load().(string)
	defer lutDrivingTables.Store(was)
	lutDrivingTables.Store("old")
	if got := tradeHedgeRuleText(StoredTrade{TradeUID: "X"}, 4, 57); got != "Standard: min(straddle / 4, IV / 57), minute end full delta, no intra-minute hedge" {
		t.Fatalf("old tables: %q", got)
	}
	lutDrivingTables.Store("new")
	simDrive.mu.Lock()
	d := simDrive.d
	simDrive.d = &simDriver{TradeUID: "TRD_LUT"}
	simDrive.mu.Unlock()
	defer func() {
		simDrive.mu.Lock()
		simDrive.d = d
		simDrive.mu.Unlock()
	}()
	if got := tradeHedgeRuleText(StoredTrade{TradeUID: "TRD_LUT"}, 4, 57); got != "LUT sim: min(straddle / 4, IV / 57), minute end leave 25% of allowed, past 1.2 x allowed: full delta" {
		t.Fatalf("LUT: %q", got)
	}
}

// The Config screen: POST /api/trade/{uid}/modify -> the stored trade.
func TestModifyTrade_SetsDivisorsAndLeavePct(t *testing.T) {
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("SB-RMW1LEQFV1-261104093431")
	tr.Config.StraddleDiv, tr.Config.HedgeDiv = 4, 57
	store.SaveTrade(tr)
	h := &Handlers{Store: store, Service: &Service{Store: store}}
	post := func(uid, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ModifyTrade(w, httptest.NewRequest(http.MethodPost, "/api/trade/"+uid+"/modify", strings.NewReader(body)))
		return w
	}
	if w := post(tr.TradeUID, `{"straddle_div":3,"hedge_div":38,"hedge_leave_pct":50}`); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	got, _ := store.LoadTrade(tr.TradeUID)
	if got.Config.StraddleDiv != 3 || got.Config.HedgeDiv != 38 || got.Config.HedgeLeavePct != 50 {
		t.Fatalf("stored %+v", got.Config)
	}
	if w := post(tr.TradeUID, `{"hedge_leave_pct":150}`); w.Code != http.StatusBadRequest {
		t.Fatalf("150 must be refused: %d", w.Code)
	}

	lut := newTestSquareOffTrade("TRD_LUT")
	store.SaveTrade(lut)
	simDrive.mu.Lock()
	d := simDrive.d
	simDrive.d = &simDriver{TradeUID: "TRD_LUT"}
	simDrive.mu.Unlock()
	defer func() {
		simDrive.mu.Lock()
		simDrive.d = d
		simDrive.mu.Unlock()
	}()
	if w := post("TRD_LUT", `{"hedge_leave_pct":50}`); w.Code != http.StatusBadRequest {
		t.Fatalf("LUT trade must refuse Leave %%: %d", w.Code)
	}
	if got, _ := store.LoadTrade("TRD_LUT"); got.Config.HedgeLeavePct != 0 {
		t.Fatalf("LUT trade changed: %g", got.Config.HedgeLeavePct)
	}
}

// The simulation: each synthetic minute-end hedge leaves that share of allowed.
func TestPaperSim_LeavePct(t *testing.T) {
	mins := mkMinutes(60, func(i int) float64 { return 22600 + float64(i)*12 })
	share := func(c PaperSimConfig) (float64, int) {
		r := RunPaperSim(mins, c, 65, nil)
		left, n := 0.0, 0
		for _, h := range r.Hedges {
			if h.Kind == "RISK_BREACH" && h.DeltaBefore != 0 && h.Allowed > 0 {
				left, n = (h.DeltaAfter/h.DeltaBefore)*h.PointsOut/h.Allowed, n+1
			}
		}
		return left, n
	}
	base := PaperSimConfig{Entry: "09:17", Lots: 1, ExitTime: "10:10", SLBps: 500, TPBps: 500, HedgeMode: "synthetic"}
	own := base
	own.HedgeLeavePct, own.StraddleDiv, own.HedgeDiv = 50, 3, 38
	if left, n := share(own); n == 0 || math.Abs(left-0.5) > 0.001 {
		t.Fatalf("own rule leaves %.3f of allowed over %d hedges", left, n)
	}
	lut := base
	lut.HedgeRule = "new"
	if left, n := share(lut); n == 0 || math.Abs(left-0.25) > 0.001 {
		t.Fatalf("LUT rule leaves %.3f of allowed over %d hedges", left, n)
	}
}

// A saved rules file from before Leave %, loaded at boot.
func TestLoadSBRules_MovesOldRuleOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	t.Setenv("SBUILD_RULES_PATH", path)
	if err := os.WriteFile(path, []byte(`[{"id":"R1","symbol":"NIFTY","target_straddle":388,"straddles":5005,"straddle_div":4,"hedge_div":57}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	rules := LoadSBRules()
	if len(rules) != 1 || rules[0].StraddleDiv != 3 || rules[0].HedgeDiv != 38 || rules[0].HedgeLeavePct != 50 {
		t.Fatalf("rules %+v", rules)
	}
}
