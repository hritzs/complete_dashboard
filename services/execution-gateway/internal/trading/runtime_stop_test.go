package trading

import (
	"testing"
	"time"
)

func TestIsTerminalTradeStatus(t *testing.T) {
	for _, st := range []string{"CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MANUAL", "FAILED"} {
		if !isTerminalTradeStatus(st) {
			t.Fatalf("%s should be terminal", st)
		}
	}
	for _, st := range []string{"ACTIVE", "BUILDING", "PARTIAL", "SQUARING_OFF", "RECONCILIATION_REQUIRED", ""} {
		if isTerminalTradeStatus(st) {
			t.Fatalf("%s must not be terminal", st)
		}
	}
}

// A manually squared-off trade used to leave its monitor goroutine running
// (and logging) forever; the loop must now end itself and drop its runtime.
func TestRunMonitorStopsOnceTradeIsClosed(t *testing.T) {
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("TRD_RUNTIME_STOP")
	tr.Status = "CLOSEDSQF"
	tr.Config.PollIntervalSec = 1
	store.SaveTrade(tr)

	svc := &Service{Store: store}
	svc.startRuntime(tr)
	rt, ok := store.LoadRuntime(tr.TradeUID)
	if !ok {
		t.Fatal("runtime was not registered")
	}

	select {
	case <-rt.DoneCh:
	case <-time.After(4 * time.Second):
		t.Fatal("monitor kept running for a closed trade")
	}
	if _, still := store.LoadRuntime(tr.TradeUID); still {
		t.Fatal("runtime was not removed after the monitor stopped")
	}
}
