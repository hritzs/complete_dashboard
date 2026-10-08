package trading

import (
	"testing"
	"time"
)

func TestLUTCarryLTPPrevSecond(t *testing.T) {
	e := &lutEngine{}
	t0 := time.Date(2026, 10, 7, 9, 16, 59, 900e6, time.UTC)
	e.noteLTPs(t0, &OptionChainSnapshot{Chain: []OptionChainRow{{Strike: 22600, CEToken: 1, PEToken: 2, CELtp: 162.9, PELtp: 157.45}}})

	// Next tick the PE reads 0: the CE stays live, the PE takes the previous second's LTP.
	t1 := t0.Add(100 * time.Millisecond)
	e.noteLTPs(t1, &OptionChainSnapshot{Chain: []OptionChainRow{{Strike: 22600, CEToken: 1, PEToken: 2, CELtp: 163.1}}})
	if px, c := e.carryLTP(t1, 1, 163.1); px != 163.1 || c {
		t.Fatalf("live CE = %v carried=%v", px, c)
	}
	if px, c := e.carryLTP(t1, 2, 0); px != 157.45 || !c {
		t.Fatalf("PE = %v carried=%v, want 157.45 from prev second", px, c)
	}
	// Older than a second: not carried, the minute stays "LTP missing".
	if px, c := e.carryLTP(t0.Add(1500*time.Millisecond), 2, 0); px != 0 || c {
		t.Fatalf("stale PE = %v carried=%v, want 0", px, c)
	}
}
