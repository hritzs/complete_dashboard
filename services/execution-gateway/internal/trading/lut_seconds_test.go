package trading

import (
	"testing"
	"time"
)

// A minute whose intra-minute hedge fired keeps its 100 ms ticks across a
// restart (they come back from the ticks file); other minutes are not
// written -- their compacted ticks equal the seconds file.
func TestLutTicksSurviveRestart(t *testing.T) {
	t.Setenv("LUT_DATA_DIR", t.TempDir())
	lutTicks.mu.Lock()
	lutTicks.day, lutTicks.m, lutTicks.done = "", nil, nil
	lutTicks.mu.Unlock()

	day := "2026-10-16"
	at := func(s string) time.Time {
		tm, _ := time.Parse("2006-01-02 15:04:05.000", day+" "+s)
		return tm
	}
	for _, tk := range []struct {
		t string
		f float64
	}{{"10:19:12.389", 23122.20}, {"10:19:12.489", 23128.10}, {"10:19:12.589", 23128.40}, {"10:20:01.100", 23126.00}} {
		lutNoteTick(day, at(tk.t), tk.f)
	}
	lutTicksCompact(day, 1021, map[int]string{1019: "10:19:12.489"})

	// Restart: memory gone; before the first tick the file is read as is.
	lutTicks.mu.Lock()
	lutTicks.day, lutTicks.m, lutTicks.done = "", nil, nil
	lutTicks.mu.Unlock()
	got := lutTicksFrom(day, 1000)
	if len(got[1019]) != 2 || got[1019][1].T != "10:19:12.489" || got[1019][1].F != 23128.10 {
		t.Fatalf("10:19 after restart = %+v, want the two ticks up to the hedge tick", got[1019])
	}
	if len(got[1020]) != 0 {
		t.Fatalf("10:20 (no intra hedge) was persisted: %+v", got[1020])
	}

	// First live tick after the restart seeds memory from the file.
	lutNoteTick(day, at("10:33:05.000"), 23150)
	got = lutTicksFrom(day, 1000)
	if len(got[1019]) != 2 || len(got[1033]) != 1 {
		t.Fatalf("after first tick: 10:19=%d ticks 10:33=%d, want 2 and 1", len(got[1019]), len(got[1033]))
	}
	lutTicks.mu.Lock()
	lutTicks.day, lutTicks.m, lutTicks.done = "", nil, nil
	lutTicks.mu.Unlock()
}
