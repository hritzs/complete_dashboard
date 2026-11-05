package trading

import (
	"regexp"
	"testing"
	"time"
)

func TestSnapshotTradeTime(t *testing.T) {
	// 2026-11-04 10:25:23 IST, the CE 187.55 print of the 4-Nov comparison
	ltt := time.Date(2026, 11, 4, 10, 25, 23, 0, istLoc()).Unix()
	if got := snapshotTradeTime(ltt); got != "10:25:23" {
		t.Fatalf("trade time %q, want 10:25:23", got)
	}
	if got := snapshotTradeTime(0); got != "-" {
		t.Fatalf("absent trade time %q, want -", got)
	}
}

func TestSnapshotSynLTP(t *testing.T) {
	ce := &OptionChainRow{Strike: 23800, CELtp: 185.25}
	pe := &OptionChainRow{Strike: 23800, PELtp: 183.15}
	if got := snapshotSynLTP(ce, pe); got < 23802.0999 || got > 23802.1001 {
		t.Fatalf("syn %.4f, want 23802.10", got)
	}
	if got := snapshotSynLTP(ce, &OptionChainRow{Strike: 23850, PELtp: 183.15}); got != 0 {
		t.Fatalf("different strikes must give 0, got %.2f", got)
	}
	if got := snapshotSynLTP(nil, pe); got != 0 {
		t.Fatalf("missing row must give 0, got %.2f", got)
	}
}

// watch_logs.sh keys on "] tick=HH:MM:SS snapshot "; the new fields go after it.
func TestSnapshotLineKeepsWatchLogsPrefix(t *testing.T) {
	line := "[MONITOR][TRD_X] tick=10:25:24 snapshot spot=23804.70 syn_fut=23804.70 fut_ltp=23985.50 total_pnl=1 pnl_per_straddle=1 delta=1 gamma=1 theta=1 vega=1 ce_ltp=185.25 pe_ltp=183.15 strike=23800 ce_qty=1 pe_qty=1 lot_size=65 at=10:25:24.012 ce_trade=10:25:23 pe_trade=10:25:23 syn_ltp=23802.10"
	if !regexp.MustCompile(`\] tick=[0-9:]+ snapshot `).MatchString(line) {
		t.Fatal("watch_logs.sh prefix no longer matches")
	}
}
