package trading

import (
	"testing"
	"time"
)

func TestDiffMonitorConfig_ListsOnlyChangedFields(t *testing.T) {
	loc := lutIST()
	a := MonitorConfig{SLPnLBpsOfSpot: 14, TPPnLBpsOfSpot: 14, SquareOffHardTime: time.Date(2026, 10, 5, 15, 37, 0, 0, loc), HedgeDiv: 57}
	b := a
	b.SquareOffHardTime = time.Date(2026, 10, 5, 10, 25, 0, 0, loc)
	b.TPPnLBpsOfSpot = 20
	got := diffMonitorConfig(a, b)
	if len(got) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(got), got)
	}
	want := map[string][2]string{"TP (bps of spot)": {"14", "20"}, "Exit time": {"15:37:00", "10:25:00"}}
	for _, c := range got {
		w, ok := want[c.Field]
		if !ok || c.From != w[0] || c.To != w[1] {
			t.Fatalf("unexpected change %+v", c)
		}
	}
	if len(diffMonitorConfig(a, a)) != 0 {
		t.Fatal("no change must give no entries")
	}
}
