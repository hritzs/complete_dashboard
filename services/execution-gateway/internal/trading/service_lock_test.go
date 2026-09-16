package trading

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLockTrade_SerializesSameTrade verifies that concurrent calls for the
// SAME trade UID are serialized, not allowed to overlap. This is the exact
// property that was missing when a live trade was found with two square-off
// attempts racing each other.
func TestLockTrade_SerializesSameTrade(t *testing.T) {
	s := &Service{}

	const n = 20
	var active int32
	var maxObservedConcurrency int32
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := s.lockTrade("TRD_SAME")
			defer unlock()

			cur := atomic.AddInt32(&active, 1)
			for {
				max := atomic.LoadInt32(&maxObservedConcurrency)
				if cur <= max || atomic.CompareAndSwapInt32(&maxObservedConcurrency, max, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxObservedConcurrency); got != 1 {
		t.Fatalf("max concurrent holders of the same trade's lock = %d, want 1 (lock did not serialize)", got)
	}
}

// TestLockTrade_DoesNotSerializeDifferentTrades verifies the lock is
// per-trade, not a single global lock that would needlessly block unrelated
// trades from being acted on concurrently.
func TestLockTrade_DoesNotSerializeDifferentTrades(t *testing.T) {
	s := &Service{}

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan time.Duration, 2)

	for _, uid := range []string{"TRD_A", "TRD_B"} {
		uid := uid
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			t0 := time.Now()
			unlock := s.lockTrade(uid)
			defer unlock()
			time.Sleep(50 * time.Millisecond)
			results <- time.Since(t0)
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	for d := range results {
		if d >= 90*time.Millisecond {
			t.Fatalf("acquiring locks for different trade UIDs took %v, expected them to run concurrently (~50ms), not serialize", d)
		}
	}
}
