package main

import "testing"

func TestLatencyTracker_RecordAndSnapshot(t *testing.T) {
	lt := &latencyTracker{}
	for _, ms := range []int64{50, 10, 30, 90, 20} {
		lt.record(ms)
	}

	count, avg, min, max := lt.snapshotAndReset()
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}
	if min != 10 {
		t.Fatalf("min = %d, want 10", min)
	}
	if max != 90 {
		t.Fatalf("max = %d, want 90", max)
	}
	wantAvg := int64((50 + 10 + 30 + 90 + 20) / 5)
	if avg != wantAvg {
		t.Fatalf("avg = %d, want %d", avg, wantAvg)
	}
}

func TestLatencyTracker_SnapshotResetsForTheNextWindow(t *testing.T) {
	lt := &latencyTracker{}
	lt.record(100)
	lt.snapshotAndReset()

	count, avg, min, max := lt.snapshotAndReset()
	if count != 0 || avg != 0 || min != 0 || max != 0 {
		t.Fatalf("second window not empty: count=%d avg=%d min=%d max=%d", count, avg, min, max)
	}
}

func TestLatencyTracker_EmptyWindowReportsZero(t *testing.T) {
	lt := &latencyTracker{}
	count, avg, min, max := lt.snapshotAndReset()
	if count != 0 || avg != 0 || min != 0 || max != 0 {
		t.Fatalf("empty window: count=%d avg=%d min=%d max=%d, want all 0", count, avg, min, max)
	}
}
