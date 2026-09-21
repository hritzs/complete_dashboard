package irisstats

import (
	"testing"
	"time"
)

func TestSnapshotLivenessAndCounts(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s := New("ws://x", t0)

	if s.Snapshot(t0).Connected {
		t.Fatal("no frames yet must not report connected")
	}

	s.Frame(KindHeartbeat, t0.Add(1*time.Second))
	s.Frame(KindOrder, t0.Add(2*time.Second))
	s.Frame(KindTrade, t0.Add(2*time.Second))
	s.Applied()
	s.Unmatched()

	snap := s.Snapshot(t0.Add(10 * time.Second))
	if !snap.Connected || snap.Frames != 3 || snap.Heartbeats != 1 || snap.OrderPushes != 1 || snap.TradePushes != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.Applied != 1 || snap.UnmatchedGaveUp != 1 {
		t.Fatalf("applied/unmatched = %d/%d, want 1/1", snap.Applied, snap.UnmatchedGaveUp)
	}
	if snap.LastOrderPushAt == nil || snap.LastFrameAgeSec == nil || *snap.LastFrameAgeSec != 8 {
		t.Fatalf("last-frame age wrong: %+v", snap)
	}

	if s.Snapshot(t0.Add(2*time.Second + LiveWindow + time.Second)).Connected {
		t.Fatal("a feed silent past the live window must report not connected")
	}
}
