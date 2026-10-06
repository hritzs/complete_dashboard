package trading

import (
	"testing"
	"time"
)

// Two trades exiting in the same second must never get the same order UID
// (live 2026-09-30: identical SQF intent IDs across two trades made each
// trade's exit fills unmatched, and the square-off retry over-bought).
func TestBuildShortOrderUID_UniqueAcrossTradesSameSecond(t *testing.T) {
	ts := time.Date(2026, 9, 30, 15, 38, 32, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		for _, leg := range []string{"SQF1C6O12", "SQF1C6O12"} { // same leg text from two trades
			uid := BuildShortOrderUID("NIFTY", leg, ts, 13)
			if len(uid) > 32 {
				t.Fatalf("uid %q longer than 32", uid)
			}
			if seen[uid] {
				t.Fatalf("duplicate uid %q after %d", uid, len(seen))
			}
			seen[uid] = true
		}
	}
	// A long leg that gets truncated must still stay unique.
	a := BuildShortOrderUID("NIFTY", "WBCE40751021XXXXXXXXXXXXXXXX", ts, 99)
	b := BuildShortOrderUID("NIFTY", "WBCE40751021XXXXXXXXXXXXXXXX", ts, 99)
	if a == b {
		t.Fatalf("truncated uids collide: %q", a)
	}
}
