package trading

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

func TestEffectiveRunAt(t *testing.T) {
	now := time.Date(2026, 9, 22, 9, 21, 30, 0, time.Local)

	future := now.Add(time.Hour)
	got, adjusted := EffectiveRunAt(future, now)
	if adjusted || !got.Equal(future) {
		t.Fatalf("a future time must be returned unchanged: got=%v adjusted=%v", got, adjusted)
	}

	// The exact scenario reported: requested 09:21:00, processed at 09:21:30.
	past := time.Date(2026, 9, 22, 9, 21, 0, 0, time.Local)
	got, adjusted = EffectiveRunAt(past, now)
	if !adjusted || !got.Equal(now) {
		t.Fatalf("a past time must fire at now instead of being discarded: got=%v adjusted=%v", got, adjusted)
	}

	got, adjusted = EffectiveRunAt(now, now)
	if !adjusted || !got.Equal(now) {
		t.Fatalf("a time equal to now must fire immediately too: got=%v adjusted=%v", got, adjusted)
	}
}

// captureLog redirects the standard logger for the duration of fn and
// returns everything it wrote.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)
	fn()
	return buf.String()
}

// Before this, SL/TP/TIME only ever logged when they BREACHED, so an
// operator watching the logs had no way to tell "monitoring is running and
// nothing is wrong" from "monitoring silently stopped" -- confirmed live
// 2026-09-22: an open trade's logs showed only HEDGE lines every minute,
// nothing for SL, TP or the configured hard exit. runMonitorCycle must now
// log an unconditional once-a-minute status line for each, plus a
// PnL/Greeks snapshot, even when nothing is close to triggering.
func TestRunMonitorCycle_LogsSLTPTIMEStatusEveryMinuteEvenWhenNotBreached(t *testing.T) {
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("TRD_MONITOR_STATUS_LOG")
	tr.Lots = 1
	tr.CELtp, tr.PELtp = 100, 90 // entries equal to the fake chain's LTPs: flat PnL, nowhere near breach
	tr.Config.SLPointsPerLot = 30
	tr.Config.SLPnLBpsOfSpot = 14
	tr.Config.TPPnLBpsOfSpot = 14
	tr.Config.SquareOffHardTime = time.Now().Add(time.Hour)
	store.SaveTrade(tr)
	store.SaveRuntime(&RuntimeTrade{Trade: tr, StopCh: make(chan struct{}), DoneCh: make(chan struct{})})

	svc := &Service{Store: store, Snapshot: fakeChainSnapshot{}, BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}}}

	out := captureLog(t, func() { svc.runMonitorCycle(tr.TradeUID) })

	for _, want := range []string{
		"check=SL status=OK",
		"check=TP status=OK",
		"check=TIME status=OK",
		"tick=" + time.Now().Format("15:04") + ":",
		" snapshot",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q\n--- full output ---\n%s", want, out)
		}
	}

	// The trade must still be open: entries at 200 vs LTPs of 100/90 is a
	// large unrealized PROFIT on a short straddle, nowhere near any of the
	// configured thresholds -- this test would be meaningless if it had
	// actually triggered an exit.
	if got, _ := store.LoadTrade(tr.TradeUID); got.Status != "ACTIVE" {
		t.Fatalf("trade status = %s, want ACTIVE (test fixture should be nowhere near breaching)", got.Status)
	}
}

// A trade with none of SL/TP/TIME configured must say so plainly, not just
// stay silent (silence is exactly the bug this fixes).
func TestRunMonitorCycle_LogsNotConfiguredWhenNothingArmed(t *testing.T) {
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("TRD_MONITOR_STATUS_LOG_UNARMED")
	tr.Lots = 1
	store.SaveTrade(tr)
	store.SaveRuntime(&RuntimeTrade{Trade: tr, StopCh: make(chan struct{}), DoneCh: make(chan struct{})})

	svc := &Service{Store: store, Snapshot: fakeChainSnapshot{}, BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}}}

	out := captureLog(t, func() { svc.runMonitorCycle(tr.TradeUID) })

	for _, want := range []string{"check=SL status=NOT_CONFIGURED", "check=TP status=DISABLED", "check=TIME status=NOT_CONFIGURED"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q\n--- full output ---\n%s", want, out)
		}
	}
	_ = context.Background()
}

// The per-tick snapshot must fire on every runMonitorCycle call -- unlike
// SL/TP/TIME/HEDGE, which stay throttled to once a minute (rt.LastMinuteCheck)
// to avoid duplicate hedge signals. Requested live 2026-09-22 after the
// operator only ever saw one snapshot per minute, matching the reference
// system's per-tick snapshot cadence.
func TestRunMonitorCycle_LogsSnapshotEveryTickEvenWithinTheSameMinute(t *testing.T) {
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("TRD_MONITOR_TICK_SNAPSHOT")
	tr.Lots = 1
	tr.CELtp, tr.PELtp = 100, 90
	store.SaveTrade(tr)
	rt := &RuntimeTrade{Trade: tr, StopCh: make(chan struct{}), DoneCh: make(chan struct{})}
	// Simulate this tick's minute having already been claimed by an
	// earlier tick within the same minute.
	rt.LastMinuteCheck = time.Now().Truncate(time.Minute)
	store.SaveRuntime(rt)

	svc := &Service{Store: store, Snapshot: fakeChainSnapshot{}, BrokerFactory: &fakeBrokerFactory{executor: &fakeSLExecutor{}}}

	out := captureLog(t, func() { svc.runMonitorCycle(tr.TradeUID) })

	if !strings.Contains(out, " snapshot ") {
		t.Fatalf("snapshot line missing even though this tick's minute was already claimed\n--- full output ---\n%s", out)
	}
	if strings.Contains(out, "check=SL") {
		t.Fatalf("SL/TP/TIME/HEDGE status lines should stay throttled to once a minute, but printed again\n--- full output ---\n%s", out)
	}
}
