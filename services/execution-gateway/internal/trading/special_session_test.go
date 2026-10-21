package trading

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func withSpecialSessions(t *testing.T, list []SpecialSession) {
	t.Helper()
	t.Setenv("SPECIAL_SESSIONS_FILE", filepath.Join(t.TempDir(), "special_sessions.json"))
	specialSessions.Lock()
	specialSessions.byDay, specialSessions.path = nil, ""
	specialSessions.Unlock()
	if list != nil {
		if err := saveSpecialSessions(list); err != nil {
			t.Fatal(err)
		}
	}
}

var muhurat2026 = SpecialSession{Date: "2026-11-08", Name: "Diwali Muhurat", PreOpen: "14:05", Open: "14:20", Close: "15:30"}

func TestLutRawDTEMuhuratCountsOnlyItsTradingMinutes(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{muhurat2026})
	ist := lutIST()
	exp := time.Date(2026, 11, 9, 0, 0, 0, 0, ist)
	at := func(h, m int) float64 { return lutRawDTE(time.Date(2026, 11, 8, h, m, 0, 0, ist), exp) }
	for _, c := range []struct {
		h, m int
		want float64
	}{
		{13, 0, 1 + 70.0/385},  // before the open: the whole 70-minute session ahead
		{14, 20, 1 + 70.0/385}, // the open
		{15, 0, 1 + 30.0/385},
		{15, 30, 1}, // the close
		{16, 0, 1},
	} {
		if got := at(c.h, c.m); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("%02d:%02d DTE %.6f, want %.6f", c.h, c.m, got, c.want)
		}
	}
}

func TestLutRawDTENormalDayUnchanged(t *testing.T) {
	ist := lutIST()
	exp := time.Date(2026, 11, 9, 0, 0, 0, 0, ist)
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, ist)
	withSpecialSessions(t, nil)
	before := lutRawDTE(now, exp)
	withSpecialSessions(t, []SpecialSession{muhurat2026})
	if after := lutRawDTE(now, exp); after != before || math.Abs(before-(3+340.0/385)) > 1e-9 {
		t.Fatalf("a normal day must not change: %.6f vs %.6f", after, before)
	}
}

func TestSessionHHMMMapsToTheOpeningTables(t *testing.T) {
	withSpecialSessions(t, []SpecialSession{muhurat2026})
	ist := lutIST()
	for _, c := range []struct {
		h, m, want int
		ok         bool
	}{{14, 20, 915, true}, {14, 21, 916, true}, {14, 25, 920, true}, {15, 29, 1024, true}, {14, 19, 0, false}, {15, 30, 0, false}} {
		got, ok := sessionHHMM(time.Date(2026, 11, 8, c.h, c.m, 30, 0, ist))
		if got != c.want || ok != c.ok {
			t.Fatalf("%02d:%02d -> %d %v, want %d %v", c.h, c.m, got, ok, c.want, c.ok)
		}
	}
	if _, ok := sessionHHMM(time.Date(2026, 11, 9, 9, 16, 0, 0, ist)); ok {
		t.Fatal("a normal day has no special clock")
	}
}

func TestSpecialSessionValidate(t *testing.T) {
	for _, s := range []SpecialSession{
		{Date: "8-11-2026", Open: "14:20", Close: "15:30"},
		{Date: "2026-11-08", Open: "15:30", Close: "14:20"},
		{Date: "2026-11-08", Open: "14:20", Close: "15:30", PreOpen: "14:30"},
	} {
		if s.validate() == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	if err := muhurat2026.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLutBusDaysCountsAListedSession(t *testing.T) {
	sun := time.Date(2026, 11, 8, 9, 15, 0, 0, lutIST())
	withSpecialSessions(t, nil)
	if n := lutBusDays(sun, 2); n != 1 {
		t.Fatalf("no session: Sun+Mon = 1 business day, got %d", n)
	}
	withSpecialSessions(t, []SpecialSession{muhurat2026})
	if n := lutBusDays(sun, 2); n != 2 {
		t.Fatalf("Muhurat Sunday listed: Sun+Mon = 2 trading days, got %d", n)
	}
}

func withMuhuratState(t *testing.T, c LUTBuildConfig) {
	t.Helper()
	t.Setenv("LUT_DATA_DIR", t.TempDir())
	withSpecialSessions(t, []SpecialSession{muhurat2026})
	if err := saveMuhuratBuildConfig(c); err != nil {
		t.Fatal(err)
	}
	muh.Lock()
	muh.d = MuhuratDay{Day: "2026-11-08"}
	muh.Unlock()
}

func muhuratYES() (LUTEvaluation, *OptionChainSnapshot) {
	return LUTEvaluation{Time: "09:16", Stage: "09:16", Strike: 23100, Underlying: 23104, TPBps: 20, Allowed: true},
		&OptionChainSnapshot{Expiry: "09-NOV-26"}
}

func TestMuhuratBuildNotArmedNeverFires(t *testing.T) {
	withMuhuratState(t, LUTBuildConfig{BrokerName: "greeksoft", AccountID: "147", Lots: 2})
	rec, ch := muhuratYES()
	muh.Lock()
	(&Service{}).muhuratFireLocked(time.Date(2026, 11, 8, 14, 21, 1, 0, lutIST()), "14:21", rec, ch, nil)
	b := muh.d.Build
	muh.Unlock()
	if b != nil {
		t.Fatalf("not armed: nothing may fire, got %+v", b)
	}
}

func TestMuhuratBuildExitIsBeforeTheSessionClose(t *testing.T) {
	withMuhuratState(t, LUTBuildConfig{Armed: true, BrokerName: "greeksoft", AccountID: "147", Lots: 2})
	rec, ch := muhuratYES()
	// A YES after the exit time (15:27, 3 min before the 15:30 close) is refused, recorded, and never sent.
	muh.Lock()
	(&Service{}).muhuratFireLocked(time.Date(2026, 11, 8, 15, 28, 1, 0, lutIST()), "15:28", rec, ch, nil)
	b := muh.d.Build
	muh.Unlock()
	if b == nil || b.Status != "REFUSED" || b.ExitTime != "15:27:00" {
		t.Fatalf("want a REFUSED build with exit 15:27:00, got %+v", b)
	}
	if d := loadMuhuratDay("2026-11-08"); d.Build == nil || d.Build.Status != "REFUSED" {
		t.Fatalf("the refusal must be saved before anything else: %+v", d.Build)
	}
}
