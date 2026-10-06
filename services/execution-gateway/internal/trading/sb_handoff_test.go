package trading

import (
	"path/filepath"
	"testing"
)

func handoffRunner(t *testing.T) (*Service, *sbRunner) {
	t.Setenv("SBUILD_RULES_PATH", filepath.Join(t.TempDir(), "sbuild_rules.json"))
	store := NewMemoryStore()
	store.SaveTrade(StoredTrade{TradeUID: "SB-T-1", Status: sbStatusLive, Symbol: "NIFTY", BrokerName: "GREEKSOFT", AccountID: "147"})
	s := &Service{Store: store}
	r := newSBRunner(SBConfig{ID: "T", Symbol: "NIFTY", Straddles: 130, SLBps: 14, TPBps: 14, ExitTime: "15:37:00", StraddleDiv: 4, HedgeDiv: 57, HedgeMinBps: 8})
	r.isLive, r.phase, r.lotSize, r.tradeUID, r.expiry = true, "COMPLETE", 65, "SB-T-1", "13-OCT-26"
	return s, r
}

func stopRuntime(s *Service, uid string) {
	if rt, ok := s.Store.LoadRuntime(uid); ok {
		close(rt.StopCh)
		s.Store.DeleteRuntime(uid)
	}
}

func TestSBHandOff_PlainStraddleBecomesNormalTrade(t *testing.T) {
	s, r := handoffRunner(t)
	r.pms.ApplyFill(PMSFill{Token: 44620, Strike: 22700, OptionType: "CE", Side: "SELL", Qty: 130, Price: 168.3, Role: "BUILD"})
	r.pms.ApplyFill(PMSFill{Token: 44621, Strike: 22700, OptionType: "PE", Side: "SELL", Qty: 65, Price: 146.8, Role: "BUILD"})
	if !s.sbHandOffLocked(r) {
		t.Fatal("expected hand-off")
	}
	defer stopRuntime(s, "SB-T-1")
	tr, _ := s.Store.LoadTrade("SB-T-1")
	if tr.Status != "ACTIVE" || tr.CEToken != 44620 || tr.PEToken != 44621 || tr.CEQty != 130 || tr.PEQty != 65 || tr.Strike != 22700 {
		t.Fatalf("trade %+v", tr)
	}
	if tr.CELtp != 168.3 || tr.PELtp != 146.8 || tr.Config.SLPnLBpsOfSpot != 14 || tr.Config.TPPnLBpsOfSpot != 14 || tr.Config.SquareOffHardTime.IsZero() {
		t.Fatalf("entry / risk not carried: %+v", tr.Config)
	}
	if !tr.Config.PartialFill || tr.Config.RequestedPEQty != 130 {
		t.Fatal("PE 65 of 130 must be marked partial")
	}
	if r.phase != sbPhaseHandedOff || r.active() || r.holding() {
		t.Fatalf("runner must stop monitoring: phase %s", r.phase)
	}
	if _, ok := s.Store.LoadRuntime("SB-T-1"); !ok {
		t.Fatal("standard monitor not started")
	}
}

func TestSBHandOff_HedgeLegStaysWithBuild(t *testing.T) {
	s, r := handoffRunner(t)
	r.pms.ApplyFill(PMSFill{Token: 1, Strike: 22700, OptionType: "CE", Side: "SELL", Qty: 65, Price: 100, Role: "BUILD"})
	r.pms.ApplyFill(PMSFill{Token: 2, Strike: 22700, OptionType: "PE", Side: "SELL", Qty: 65, Price: 90, Role: "BUILD"})
	r.pms.ApplyFill(PMSFill{Token: 3, Strike: 22750, OptionType: "CE", Side: "BUY", Qty: 65, Price: 80, Role: "HEDGE"})
	if s.sbHandOffLocked(r) {
		stopRuntime(s, "SB-T-1")
		t.Fatal("a build with a hedge leg must stay with its own monitor")
	}
	if r.phase != "COMPLETE" {
		t.Fatalf("phase %s", r.phase)
	}
}
