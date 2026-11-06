package trading

import (
	"strings"
	"testing"
	"time"
)

var muhuratSell2026 = SpecialSession{Date: "2026-11-08", Name: "Diwali Muhurat", PreOpen: "14:05", Open: "14:20", Close: "15:40"}

// LIVE only inside the listed session, until 3 minutes before its close; the
// exit time is that cap. A paper dry run may start any time.
func TestMuhuratSellCheck(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{muhuratSell2026})
	ist := lutIST()
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, ist) }
	cap := at(8, 15, 37)

	if _, err := muhuratSellCheck(at(6, 11, 0), true); err == nil || !strings.Contains(err.Error(), "special-session date") {
		t.Fatalf("LIVE on a normal day: %v, want refused", err)
	}
	if exit, err := muhuratSellCheck(at(6, 11, 0), false); err != nil || !exit.IsZero() {
		t.Fatalf("paper on a normal day: exit %v err %v, want allowed with no session exit", exit, err)
	}
	if _, err := muhuratSellCheck(at(8, 14, 10), true); err == nil || !strings.Contains(err.Error(), "opens at 14:20") {
		t.Fatalf("LIVE before the open: %v, want refused", err)
	}
	for _, m := range []int{20, 21, 59} {
		exit, err := muhuratSellCheck(at(8, 14, m), true)
		if err != nil || !exit.Equal(cap) {
			t.Fatalf("LIVE at 14:%02d: exit %v err %v, want allowed, exit 15:37", m, exit, err)
		}
	}
	if _, err := muhuratSellCheck(at(8, 15, 37), true); err == nil || !strings.Contains(err.Error(), "has passed") {
		t.Fatalf("LIVE at the exit cap: %v, want refused", err)
	}
	if exit, err := muhuratSellCheck(at(8, 15, 38), false); err != nil || !exit.Equal(cap) {
		t.Fatalf("paper after the cap on the session day: exit %v err %v", exit, err)
	}
}

// A Muhurat rule: id MUH..., exit time cleared (set at start), cannot move to the
// normal tab; a normal rule is saved exactly as before; unknown sessions refused.
func TestSaveSBRuleMuhurat(t *testing.T) {
	t.Setenv("SBUILD_RULES_PATH", t.TempDir()+"/sbuild_rules.json")
	s := &Service{}
	m, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 300, Straddles: 650, SLBps: 14, TPBps: 10, ExitTime: "15:20", Session: sbSessionMuhurat})
	if err != nil {
		t.Fatal(err)
	}
	defer sbEng.remove(m.ID)
	if !strings.HasPrefix(m.ID, "MUH") || m.ExitTime != "" || m.Session != sbSessionMuhurat || m.SLBps != 14 || m.TPBps != 10 {
		t.Fatalf("muhurat rule saved as %+v", m)
	}
	if tr, err := (&Service{Store: NewMemoryStore()}).sbLiveCreateTrade(m, "10-NOV-26", 65); err != nil || !strings.HasPrefix(tr.TradeUID, "SB-MUH") {
		t.Fatalf("trade id %q (err %v), want SB-MUH...", tr.TradeUID, err)
	}
	m.Session = ""
	if _, _, err := s.SaveSBRule(m); err == nil || !strings.Contains(err.Error(), "cannot move") {
		t.Fatalf("moving a Muhurat rule to the normal tab: %v, want refused", err)
	}
	n, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 300, Straddles: 650, ExitTime: "15:37:00"})
	if err != nil {
		t.Fatal(err)
	}
	defer sbEng.remove(n.ID)
	if !strings.HasPrefix(n.ID, "R") || n.ExitTime != "15:37:00" || n.Session != "" {
		t.Fatalf("normal rule changed: %+v", n)
	}
	if _, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 300, Straddles: 650, Session: "diwali"}); err == nil {
		t.Fatalf("unknown session saved")
	}
}

// StartStraddleBuild refuses a Muhurat rule LIVE on a day with no session
// (before any lot-size / order checks), and leaves normal rules alone.
func TestStartStraddleBuildMuhuratLiveOnlyInSession(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{{Date: "2099-11-08", Name: "Diwali Muhurat", Open: "14:20", Close: "15:40"}})
	t.Setenv("SBUILD_RULES_PATH", t.TempDir()+"/sbuild_rules.json")
	s := &Service{}
	m, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 300, Straddles: 650, Session: sbSessionMuhurat})
	if err != nil {
		t.Fatal(err)
	}
	defer sbEng.remove(m.ID)
	if err := s.StartStraddleBuild(m.ID, true, sbLiveConfirm); err == nil || !strings.Contains(err.Error(), "special-session date") {
		t.Fatalf("LIVE Muhurat rule today: %v, want refused", err)
	}
}

// Saving a Muhurat rule while it runs keeps the session exit set at the start.
func TestSaveSBRuleMuhuratKeepsRunningExit(t *testing.T) {
	t.Setenv("SBUILD_RULES_PATH", t.TempDir()+"/sbuild_rules.json")
	s := &Service{}
	m, _, err := s.SaveSBRule(SBConfig{Symbol: "NIFTY", TargetStraddle: 300, Straddles: 650, Session: sbSessionMuhurat})
	if err != nil {
		t.Fatal(err)
	}
	defer sbEng.remove(m.ID)
	r := sbEng.get(m.ID)
	r.mu.Lock()
	r.cfg.ExitTime = "15:37:00" // as StartStraddleBuild sets it in the session
	r.mu.Unlock()
	m.TargetStraddle, m.ExitTime = 310, ""
	got, _, err := s.SaveSBRule(m)
	if err != nil || got.ExitTime != "15:37:00" || got.TargetStraddle != 310 {
		t.Fatalf("saved %+v err %v: want exit kept at 15:37:00, target 310", got, err)
	}
}
