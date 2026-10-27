package trading

import (
	"context"
	"testing"
)

// Stop wings: no automatic wing order (hedge / manual leg follow, exit
// in-step sells) while stopped; the flag is read from the trade as stored
// now, and toggling it sends nothing.
func TestWingsStopped_NoAutomaticWingOrders(t *testing.T) {
	store := NewMemoryStore()
	tr := newTestSquareOffTrade("TRD_WINGS_STOP")
	tr.Config.WingPct = 2
	store.SaveTrade(tr)
	exec := &fakeSLExecutor{}
	svc := &Service{Store: store, BrokerFactory: &fakeBrokerFactory{executor: exec}}

	if _, err := svc.SetWingsStopped(tr.TradeUID, true); err != nil {
		t.Fatal(err)
	}
	stale := tr // the caller's copy from before the click
	if err := svc.adjustWings(context.Background(), exec, stale, 650, -650, 22650, 22650, "HEDGE test"); err != nil {
		t.Fatal(err)
	}
	var f wingFollower
	f.follow(svc, exec, stale, -650, -650, "SQF-test")
	if len(exec.submitted) != 0 {
		t.Fatalf("stopped wings sent %d order(s)", len(exec.submitted))
	}
	cur, _ := store.LoadTrade(tr.TradeUID)
	if !cur.Config.WingsStopped || len(cur.Config.Modifications) != 1 {
		t.Fatalf("flag / history not saved: %+v", cur.Config)
	}
	if _, err := svc.SetWingsStopped(tr.TradeUID, false); err != nil {
		t.Fatal(err)
	}
	if cur, _ = store.LoadTrade(tr.TradeUID); cur.Config.WingsStopped {
		t.Fatal("resume not saved")
	}
}
