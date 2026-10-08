package trading

import (
	"strings"
	"testing"
	"time"
)

// These cover only the paths that stop BEFORE any order code (not armed /
// refused): a test must never reach ExecuteFinalBuild.

func lutBuildTestEngine(t *testing.T) (*Service, LUTEvaluation, *LUTEntry) {
	t.Helper()
	t.Setenv("LUT_DATA_DIR", t.TempDir())
	lutEng.mu.Lock()
	lutEng.build, lutEng.entry, lutEng.day = nil, nil, "2026-10-08"
	lutEng.mu.Unlock()
	t.Cleanup(func() {
		lutEng.mu.Lock()
		lutEng.build, lutEng.entry = nil, nil
		lutEng.mu.Unlock()
	})
	rec := LUTEvaluation{Time: "09:17", Allowed: true, TPBps: 11.4918, Underlying: 22607.3, Strike: 22600}
	entry := &LUTEntry{Time: "09:17", Expiry: "13-OCT-26", Strike: 22600, Evaluation: rec}
	return &Service{}, rec, entry
}

func TestLUTBuild_NotArmedNeverFires(t *testing.T) {
	s, rec, entry := lutBuildTestEngine(t)
	lutEng.mu.Lock()
	s.lutFireBuildLocked("2026-10-08", rec, entry)
	b := lutEng.build
	lutEng.mu.Unlock()
	if b != nil {
		t.Fatalf("not armed: must not record or fire a build, got %+v", b)
	}
}

// Armed with a configuration blocker (exit inside the scan window): the
// build is REFUSED and recorded (saved) with the exact LUT parameters.
func TestLUTBuild_RefusedIsRecorded(t *testing.T) {
	s, rec, entry := lutBuildTestEngine(t)
	if err := saveLUTBuildConfig(lutBuildDefaults(LUTBuildConfig{Armed: true, Lots: 99, ExitTime: "13:00"})); err != nil { // exit inside the scan window: refused before any order code
		t.Fatal(err)
	}
	lutEng.mu.Lock()
	s.lutFireBuildLocked("2026-10-08", rec, entry)
	first := lutEng.build
	lutEng.mu.Unlock()
	if first == nil || first.Status != "REFUSED" || first.Error == "" {
		t.Fatalf("want REFUSED with a reason, got %+v", first)
	}
	if _, st := lutLoadDay("2026-10-08"); st.Build == nil || st.Build.Status != "REFUSED" {
		t.Fatalf("the day's build must be saved before anything is sent, got %+v", st.Build)
	}
	if first.TPBps != rec.TPBps || first.SLBps != lutSLBps || first.Strike != 22600 || first.Expiry != "13-OCT-26" {
		t.Fatalf("exact LUT params expected: %+v", first)
	}
}

func TestLUTBuild_RiskOverrides(t *testing.T) {
	rec := LUTEvaluation{TPBps: 11.4918}
	r := lutBuildRisk(lutBuildDefaults(LUTBuildConfig{}), rec)
	if r.SlBps != lutSLBps || r.TpBps != 11.4918 || r.ExitTime != "15:37:00" || r.HedgeDiv != 57 || r.StraddleDiv != 4 {
		t.Fatalf("LUT's own risk expected: %+v", r)
	}
	r = lutBuildRisk(lutBuildDefaults(LUTBuildConfig{SLBps: 20, TPBps: 9}), rec)
	if r.SlBps != 20 || r.TpBps != 9 {
		t.Fatalf("overrides not applied: %+v", r)
	}
}

func TestLUTBuild_Checks(t *testing.T) {
	s := &Service{}
	now := time.Date(2026, 10, 8, 9, 17, 0, 0, lutIST())
	why := s.lutBuildChecks(lutBuildDefaults(LUTBuildConfig{Armed: true, Lots: 15, ExitTime: "13:00"}), now)
	want := map[string]bool{"exit": false}
	for _, w := range why {
		switch {
		case strings.Contains(w, "lots"):
			t.Fatalf("no lot cap on the real build: %v", why)
		case strings.Contains(w, "scan window"):
			want["exit"] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Fatalf("missing check %q in %v", k, why)
		}
	}
	for _, w := range why {
		if strings.Contains(w, "order confirmations") {
			t.Fatal("order confirmations must be a warning, never a blocker")
		}
	}
	if len(s.lutBuildWarnings()) == 0 {
		t.Fatal("unhealthy order confirmations must show as a warning")
	}
}

// A refused test fire (unparseable exit time) records nothing and never
// touches the day's real build.
func TestLUTBuild_TestFireRefusedAndSeparate(t *testing.T) {
	s, _, _ := lutBuildTestEngine(t)
	if err := saveLUTBuildConfig(lutBuildDefaults(LUTBuildConfig{Lots: 99, ExitTime: "bad"})); err != nil { // bad exit time: refused before any order code
		t.Fatal(err)
	}
	lutEng.mu.Lock()
	lutEng.live.Eval = &LUTEvaluation{Strike: 22600, TPBps: 11.2}
	lutEng.expiry = "13-OCT-26"
	run, err := s.lutTestFireLocked()
	build, tests := lutEng.build, len(lutEng.tests)
	lutEng.live.Eval = nil
	lutEng.mu.Unlock()
	if err == nil || run != nil {
		t.Fatalf("must be refused (no order confirmations / or outside session): run %+v", run)
	}
	if build != nil || tests != 0 {
		t.Fatal("a refused test must record nothing and never touch the real build")
	}
}

// The FIRST YES is the entry: even if it was refused, a later YES never fires.
func TestLUTBuild_FirstYESOnly(t *testing.T) {
	s, rec, entry := lutBuildTestEngine(t)
	if err := saveLUTBuildConfig(lutBuildDefaults(LUTBuildConfig{Armed: true, Lots: 99, ExitTime: "13:00"})); err != nil { // exit inside the scan window -> refused
		t.Fatal(err)
	}
	lutEng.mu.Lock()
	s.lutFireBuildLocked("2026-10-08", rec, entry)
	first := lutEng.build
	rec2 := rec
	rec2.Time = "09:25"
	s.lutFireBuildLocked("2026-10-08", rec2, entry)
	second := lutEng.build
	lutEng.mu.Unlock()
	if first == nil || first.Status != "REFUSED" || second != first {
		t.Fatalf("first YES only: first %+v second %+v", first, second)
	}
}

// Once actually sent, later YES minutes never fire again.
func TestLUTBuild_SentBuildBlocksLaterYES(t *testing.T) {
	s, rec, entry := lutBuildTestEngine(t)
	if err := saveLUTBuildConfig(lutBuildDefaults(LUTBuildConfig{Armed: true, Lots: 1})); err != nil {
		t.Fatal(err)
	}
	lutEng.mu.Lock()
	sent := &LUTBuildRun{Status: "BUILT", Minute: "09:17"}
	lutEng.build = sent
	s.lutFireBuildLocked("2026-10-08", rec, entry)
	after := lutEng.build
	lutEng.mu.Unlock()
	if after != sent {
		t.Fatal("a later YES must not fire once the day's build was sent")
	}
}
