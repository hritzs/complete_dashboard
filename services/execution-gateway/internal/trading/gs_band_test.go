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
	if b := gsMinuteBand("TRD_NOT_SIM_DRIVEN"); b != 2 {
		t.Fatalf("normal trade band %d, want 2", b)
	}
}
