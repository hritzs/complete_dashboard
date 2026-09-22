package trading

import (
	"context"
	"errors"
	"testing"
	"time"
)

func istDate(h, m, s int) time.Time {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), h, m, s, 0, loc)
}

func TestParseClockTodayIST(t *testing.T) {
	now := time.Now()
	for in, want := range map[string][3]int{"15:15": {15, 15, 0}, "15:15:30": {15, 15, 30}, " 09:20 ": {9, 20, 0}} {
		got, err := ParseClockTodayIST(in, now)
		if err != nil || got.Hour() != want[0] || got.Minute() != want[1] || got.Second() != want[2] {
			t.Fatalf("ParseClockTodayIST(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "25:00", "abc", "15", "15:15:15:15"} {
		if _, err := ParseClockTodayIST(bad, now); err == nil {
			t.Fatalf("ParseClockTodayIST(%q) should fail", bad)
		}
	}
}

func TestBuildRiskConfig_Validate(t *testing.T) {
	entry := istDate(9, 20, 0)
	if err := (*BuildRiskConfig)(nil).Validate(entry); err != nil {
		t.Fatalf("nil config must validate: %v", err)
	}
	if err := (&BuildRiskConfig{ExitTime: "15:15"}).Validate(entry); err != nil {
		t.Fatalf("exit after entry: %v", err)
	}
	if err := (&BuildRiskConfig{ExitTime: "09:20:00"}).Validate(entry); err == nil {
		t.Fatal("exit equal to entry must be rejected (it would exit the moment it enters)")
	}
	if err := (&BuildRiskConfig{ExitTime: "09:00"}).Validate(entry); err == nil {
		t.Fatal("exit before entry must be rejected")
	}
	if err := (&BuildRiskConfig{ExitTime: "nope"}).Validate(entry); err == nil {
		t.Fatal("unparseable exit must be rejected")
	}
	if err := (&BuildRiskConfig{SlBps: -1}).Validate(entry); err == nil {
		t.Fatal("negative sl_bps must be rejected")
	}
}

func TestApplyBuildRiskConfig(t *testing.T) {
	base := MonitorConfig{BuyBuffer: 2, SellBuffer: 2, StraddleDiv: 4, HedgeDiv: 57}

	cfg := base
	if err := applyBuildRiskConfig(&cfg, &BuildRiskConfig{
		ExitTime: "15:15", SlBps: 14, BuyBuffer: 6, SellBuffer: 7, HedgeDiv: 50, StraddleDiv: 3,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if cfg.SquareOffHardTime.Hour() != 15 || cfg.SquareOffHardTime.Minute() != 15 {
		t.Fatalf("exit time not applied: %v", cfg.SquareOffHardTime)
	}
	if cfg.SLPnLBpsOfSpot != 14 || cfg.BuyBuffer != 6 || cfg.SellBuffer != 7 || cfg.HedgeDiv != 50 || cfg.StraddleDiv != 3 {
		t.Fatalf("settings not applied: %+v", cfg)
	}

	// The form sends 0 for "not set": defaults must survive, and no exit or SL is invented.
	cfg = base
	if err := applyBuildRiskConfig(&cfg, &BuildRiskConfig{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if cfg != base {
		t.Fatalf("empty risk config changed the defaults: %+v", cfg)
	}
	if err := applyBuildRiskConfig(&cfg, nil, time.Now()); err != nil || cfg != base {
		t.Fatalf("nil risk config: %v %+v", err, cfg)
	}
	if err := applyBuildRiskConfig(&cfg, &BuildRiskConfig{ExitTime: "junk"}, time.Now()); err == nil {
		t.Fatal("bad exit time must error")
	}
}

func TestNotAppliedBuildFields(t *testing.T) {
	got := notAppliedBuildFields(ConfigBuildRequest{Idv: 1, RollStraddleDiv: 0.2, HedgeStartTime: "09:20:00", SlBps: 5, ExitTime: "15:15"})
	want := map[string]bool{"idv": true, "roll_straddle_div": true, "hedge_start_time": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected not-applied field %q (sl_bps and exit_time ARE applied)", g)
		}
	}
	if len(notAppliedBuildFields(ConfigBuildRequest{})) != 0 {
		t.Fatal("an empty request has nothing to report")
	}
}

type failingBrokerFactory struct{}

func (failingBrokerFactory) GetExecutor(userID, brokerName, accountID string) (Executor, error) {
	return nil, errors.New("no broker in test")
}

func TestScheduler_ListCancelAndCleanup(t *testing.T) {
	sched := NewBuildScheduler(&Service{Store: NewMemoryStore(), BrokerFactory: failingBrokerFactory{}})
	req := DeployStraddleRequest{Symbol: "NIFTY", Lots: 1, BrokerName: "GREEKSOFT", AccountID: "147"}

	// A time already in the past fires almost immediately instead of being
	// rejected/discarded -- e.g. requested 09:21:00 but processed at
	// 09:21:30. It still gets a job id and is briefly listed as pending.
	pastJob, err := sched.Schedule(BuildSourceConfig, time.Now().Add(-time.Second), req)
	if err != nil {
		t.Fatalf("a past time must fire immediately, not be rejected: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	stillPending := true
	for time.Now().Before(deadline) {
		found := false
		for _, j := range sched.List() {
			if j.ID == pastJob.ID {
				found = true
			}
		}
		if !found {
			stillPending = false
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stillPending {
		t.Fatal("a past-time job never fired (stayed pending)")
	}

	job, err := sched.Schedule(BuildSourceConfig, time.Now().Add(time.Hour), req)
	if err != nil {
		t.Fatal(err)
	}
	if l := sched.List(); len(l) != 1 || l[0].ID != job.ID {
		t.Fatalf("List = %+v, want the one pending job", l)
	}
	if !sched.Cancel(job.ID) {
		t.Fatal("Cancel of a pending job must succeed")
	}
	if len(sched.List()) != 0 {
		t.Fatal("cancelled job still listed")
	}
	if sched.Cancel(job.ID) || sched.Cancel("nope") {
		t.Fatal("Cancel of an unknown/already-cancelled job must report false")
	}

	// A job that fires and FAILS must still leave the pending list.
	if _, err := sched.Schedule(BuildSourceConfig, time.Now().Add(150*time.Millisecond), req); err != nil {
		t.Fatal(err)
	}
	deadline2 := time.Now().Add(3 * time.Second)
	for len(sched.List()) != 0 {
		if time.Now().After(deadline2) {
			t.Fatal("a failed job stayed in the pending list")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = context.Background()
}
