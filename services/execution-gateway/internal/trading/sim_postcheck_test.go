package trading

import (
	"math"
	"testing"
)

func TestSimPostCheckCompare(t *testing.T) {
	// 27 Oct 09:23: sim hedged to -0.039 per straddle at 22,717.15; 12,480 units.
	// Two seconds later live reads -480 at 22,716.30 with book gamma -43.
	want, diff, ok := simPostCheckCompare(-480, -43, 22716.30, -0.039, 22717.15, 12480, 65)
	if math.Abs(want-(-0.039*12480+(-43)*(22716.30-22717.15))) > 1e-6 {
		t.Fatalf("want %.2f", want)
	}
	if !ok || math.Abs(diff) > 65 {
		t.Fatalf("expected a match, diff %.1f", diff)
	}
	// A 23-lot shortfall (1,495) must be flagged.
	if _, _, ok := simPostCheckCompare(-2000, -96, 22718.55, -0.039, 22718.55, 12480, 65); ok {
		t.Fatal("a 23-lot hedge shortfall must be a mismatch")
	}
}
