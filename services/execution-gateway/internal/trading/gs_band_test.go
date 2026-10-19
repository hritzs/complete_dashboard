package trading

import "testing"

func TestATMBandTokens(t *testing.T) {
	c := &OptionChainSnapshot{ATM: 23000, SyntheticFuture: 23004, Chain: []OptionChainRow{}}
	for i, k := range []float64{22850, 22900, 22950, 23000, 23050, 23100, 23150} {
		c.Chain = append(c.Chain, OptionChainRow{Strike: k, CEToken: int64(100 + i), PEToken: int64(200 + i)})
	}
	if b := atmBandTokens(c, 2); len(b) != 10 || b[100] || b[106] || !b[101] || !b[105] {
		t.Fatalf("band 2 around 23000 = 22900..23100 (10 tokens), got %v", b)
	}
	if b := atmBandTokens(c, 0); len(b) != 2 || !b[103] || !b[203] {
		t.Fatalf("band 0 = the ATM pair only, got %v", b)
	}
	c.SyntheticFuture = 23022 // within 25+10 of 23050: the ATM may flip there
	if b := atmBandTokens(c, 0); len(b) != 4 || !b[104] {
		t.Fatalf("band 0 near the middle adds the flip strike, got %v", b)
	}
}

func TestGSMinuteBand(t *testing.T) {
	simDrive.mu.Lock()
	prev := simDrive.d
	simDrive.d = &simDriver{TradeUID: "TRD_LUT_SIM"}
	simDrive.mu.Unlock()
	defer func() { simDrive.mu.Lock(); simDrive.d = prev; simDrive.mu.Unlock() }()
	if b := gsMinuteBand("TRD_NOT_SIM_DRIVEN"); b != 2 {
		t.Fatalf("normal trade band %d, want 2 (ATM +/- 2 and every held leg)", b)
	}
	if b := gsMinuteBand("TRD_LUT_SIM"); b != 0 {
		t.Fatalf("sim-driven LUT trade band %d, want 0 (ATM pair + own strike only)", b)
	}
}

func TestGSRankOrder(t *testing.T) {
	toks := map[int64]bool{1: true, 2: true, 10: true, 11: true, 20: true, 21: true, 30: true, 31: true, 99: true}
	own := map[int64]bool{10: true, 11: true}
	legs := map[int64]int64{10: 12480, 11: 12480, 20: 3510, 21: 3510, 30: 650, 31: 650}
	r := gsRank(1, 2, toks, own, legs)
	if r[1] != 0 || r[2] != 0 {
		t.Fatalf("ATM pair must be first: %v", r)
	}
	if r[10] != 1 || r[11] != 1 {
		t.Fatalf("own straddle strike second: %v", r)
	}
	if !(r[20] < r[30] && r[21] < r[31]) {
		t.Fatalf("hedge legs largest first: %v", r)
	}
	if r[99] <= r[31] {
		t.Fatalf("band-only token after every held leg: %v", r)
	}
}

func TestPaperSameExpiry(t *testing.T) {
	if !paperSameExpiry("27-OCT-26", "27-OCT-26", false) || paperSameExpiry("27-OCT-26", "19-OCT-26", true) {
		t.Fatalf("a next-week trade belongs to the next-week view only")
	}
	if !paperSameExpiry("2026-10-19", "19-OCT-26", true) {
		t.Fatalf("formats differ, same date")
	}
	if !paperSameExpiry("", "19-OCT-26", true) || paperSameExpiry("", "27-OCT-26", false) {
		t.Fatalf("no expiry: current view only")
	}
}
