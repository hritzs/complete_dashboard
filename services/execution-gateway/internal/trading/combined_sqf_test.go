package trading

import (
	"sync/atomic"
	"testing"
	"time"
)

// 10 lots each leg = 1300 straddle contracts (CE 650 + PE 650), lot 65.
const csLot, csOrig = 65, 1300

// MTM 50% then the straddle rule at 100%: the second closes the remaining
// 50% -- never another 100% (no new long).
func TestCombined_SecondRuleClosesOnlyWhatIsLeft(t *testing.T) {
	first := ruleRemaining(50, csOrig, 0, csOrig, csLot)
	if first != 650 {
		t.Fatalf("MTM 50%% closes %d, want 650", first)
	}
	open := int64(csOrig - first)
	if got := ruleRemaining(100, csOrig, 0, open, csLot); got != open {
		t.Fatalf("straddle 100%% closes %d, want the remaining %d", got, open)
	}
	if got := ruleRemaining(60, csOrig, 0, open, csLot); got != 650 { // 60% of orig = 780 > 650 open
		t.Fatalf("a 60%% rule after 50%% closed: %d, want capped at the open 650", got)
	}
}

// A rule's own progress counts: 50% fired, half done, resumes for the rest only.
func TestCombined_RuleProgressIsRemembered(t *testing.T) {
	if got := ruleRemaining(50, csOrig, 390, csOrig-390, csLot); got != 260 {
		t.Fatalf("remaining share %d, want 650-390=260", got)
	}
	if got := ruleRemaining(50, csOrig, 650, csOrig-650, csLot); got != 0 {
		t.Fatalf("share done: %d, want 0", got)
	}
	if got := ruleRemaining(50, csOrig, 0, 0, csLot); got != 0 {
		t.Fatal("nothing open: nothing to close")
	}
}

// Rounded to whole lots of the original.
func TestCombined_RuleRoundsToLots(t *testing.T) {
	if got := ruleRemaining(33, csOrig, 0, csOrig, csLot); got%csLot != 0 || got != 455 { // 429 -> 7 lots
		t.Fatalf("33%%: %d, want 455 (7 lots)", got)
	}
}

// The share is split pro rata over every open non-wing leg, never beyond a
// leg's open quantity; complete = everything.
func TestCombined_LegTargets(t *testing.T) {
	legs := []mtmLeg{{Token: 1, NetShort: 650}, {Token: 2, NetShort: 650}, {Token: 3, NetShort: -130}}
	tg := legCloseTargets(legs, 650, 1300, csLot)
	if tg[1] != 325 || tg[2] != 325 || tg[3] != 65 {
		t.Fatalf("50%% targets %v, want 325/325/65", tg)
	}
	tg = legCloseTargets(legs, 1300, 1300, csLot)
	if tg[1] != 650 || tg[2] != 650 || tg[3] != 130 {
		t.Fatalf("complete targets %v", tg)
	}
	if len(legCloseTargets(legs, 0, 1300, csLot)) != 0 {
		t.Fatal("nothing to close: no targets")
	}
}

func TestCombined_OpenOrigFromFills(t *testing.T) {
	tr := StoredTrade{CEToken: 1, PEToken: 2}
	legs := []mtmLeg{{Token: 1, NetShort: 390, BuildSold: 650}, {Token: 2, NetShort: 390, BuildSold: 650}, {Token: 9, NetShort: 65, BuildSold: 0}}
	open, orig := straddleOpenOrig(legs, tr)
	if open != 780 || orig != 1300 {
		t.Fatalf("open %d orig %d, want 780 / 1300 (hedge legs not counted)", open, orig)
	}
}

// One background exit per trade at a time.
func TestCombined_OneExitAtATime(t *testing.T) {
	s := &Service{}
	release := make(chan struct{})
	var ran int32
	if !s.runExitAsync("T1", "MTM", func() { atomic.AddInt32(&ran, 1); <-release }) {
		t.Fatal("first exit must start")
	}
	time.Sleep(20 * time.Millisecond)
	if s.runExitAsync("T1", "SL", func() { atomic.AddInt32(&ran, 1) }) {
		t.Fatal("a second exit on the same trade must not start while one runs")
	}
	if !s.exitRunning("T1") {
		t.Fatal("exitRunning must report the running exit")
	}
	close(release)
	time.Sleep(20 * time.Millisecond)
	if s.exitRunning("T1") || atomic.LoadInt32(&ran) != 1 {
		t.Fatalf("after it ends: running=%v ran=%d", s.exitRunning("T1"), ran)
	}
}
