package trading

import "testing"

// A delta hedge is never capped by the close guard (it may net through an
// older opposite hedge on its strike); exits, wings and manual legs are.
func TestCloseGuardApplies(t *testing.T) {
	if closeGuardApplies("HEDGE", "HG-1") || closeGuardApplies(" hedge ", "HG-1") {
		t.Fatal("hedge pairs must not be capped")
	}
	if !closeGuardApplies("HEDGE", "") {
		t.Fatal("a hedge order outside a hedge pair (cut-shorts fallback) stays guarded")
	}
	for _, p := range []string{"SQF", "PSQF", "WING", "MANUAL_LEG", "BUILD", ""} {
		if !closeGuardApplies(p, "HG-1") {
			t.Fatalf("%q must stay guarded", p)
		}
	}
}
