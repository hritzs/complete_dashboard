package trading

import (
	"testing"
	"time"
)

// The driver owns its trade until released; a stale run is still owned
// (no fallback to the monitor's own rules) but not fresh.
func TestSimDriveOwnsAndRelease(t *testing.T) {
	simDrive.mu.Lock()
	simDrive.d = &simDriver{TradeUID: "T1", LastRun: time.Now(), seen: map[string]bool{}}
	simDrive.mu.Unlock()
	if o, f := simDriveOwns("T1"); !o || !f {
		t.Fatal("fresh driver must own and be fresh")
	}
	if o, _ := simDriveOwns("T2"); o {
		t.Fatal("other trade not owned")
	}
	simDrive.mu.Lock()
	simDrive.d.LastRun = time.Now().Add(-2 * simDriveStale)
	simDrive.mu.Unlock()
	if o, f := simDriveOwns("T1"); !o || f {
		t.Fatal("stale driver still owns, not fresh")
	}
	simDriveRelease("T1", "test")
	if o, _ := simDriveOwns("T1"); o {
		t.Fatal("released driver must not own")
	}
}
