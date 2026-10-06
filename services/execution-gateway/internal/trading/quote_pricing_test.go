package trading

import "testing"

func TestBufferForAttempt_MatchesRequestedSchedule(t *testing.T) {
	cases := []struct {
		attempt int
		want    float64
	}{
		{1, 1}, // initial order
		{2, 2}, // first chase
		{3, 4}, // second chase
		{4, 8}, // beyond the schedule: keeps doubling
		{5, 16},
		{0, 1}, // clamps up to attempt 1
	}
	for _, c := range cases {
		if got := bufferForAttempt(c.attempt); got != c.want {
			t.Fatalf("bufferForAttempt(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func nearlyEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.001
}

func TestBidAskLimitPrice_SellUsesBidMinusBuffer(t *testing.T) {
	price, ok := bidAskLimitPrice("SELL", 120.30, 120.80, 1)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !nearlyEqual(price, 119.30) {
		t.Fatalf("price = %v, want 119.30", price)
	}
}

func TestBidAskLimitPrice_BuyUsesAskPlusBuffer(t *testing.T) {
	price, ok := bidAskLimitPrice("BUY", 120.30, 120.80, 1)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !nearlyEqual(price, 121.80) {
		t.Fatalf("price = %v, want 121.80", price)
	}
}

func TestBidAskLimitPrice_MissingQuoteSideFalls(t *testing.T) {
	if _, ok := bidAskLimitPrice("SELL", 0, 120.80, 1); ok {
		t.Fatalf("SELL with no bid should not be ok")
	}
	if _, ok := bidAskLimitPrice("BUY", 120.30, 0, 1); ok {
		t.Fatalf("BUY with no ask should not be ok")
	}
	if _, ok := bidAskLimitPrice("HOLD", 120.30, 120.80, 1); ok {
		t.Fatalf("unsupported side should not be ok")
	}
}

func TestBidAskLimitPrice_FloorsAtFiveCentsAndRoundsToTick(t *testing.T) {
	price, ok := bidAskLimitPrice("SELL", 0.10, 0.20, 1)
	if !ok || !nearlyEqual(price, 0.05) {
		t.Fatalf("price = %v ok=%v, want 0.05 true", price, ok)
	}
	// 119.27 - 1 = 118.27 -> rounds to nearest 0.05 = 118.25
	price, ok = bidAskLimitPrice("SELL", 119.27, 119.60, 1)
	if !ok || !nearlyEqual(price, 118.25) {
		t.Fatalf("price = %v ok=%v, want 118.25 true", price, ok)
	}
}

func TestBidAskForToken_FindsCEOrPELegByToken(t *testing.T) {
	chain := &OptionChainSnapshot{
		Chain: []OptionChainRow{
			{CEToken: 111, PEToken: 222, CEBid: 100, CEAsk: 101, PEBid: 50, PEAsk: 51},
		},
	}
	bid, ask := bidAskForToken(chain, 111)
	if bid != 100 || ask != 101 {
		t.Fatalf("CE leg: bid=%v ask=%v, want 100 101", bid, ask)
	}
	bid, ask = bidAskForToken(chain, 222)
	if bid != 50 || ask != 51 {
		t.Fatalf("PE leg: bid=%v ask=%v, want 50 51", bid, ask)
	}
	bid, ask = bidAskForToken(chain, 999)
	if bid != 0 || ask != 0 {
		t.Fatalf("unknown token: bid=%v ask=%v, want 0 0", bid, ask)
	}
	bid, ask = bidAskForToken(nil, 111)
	if bid != 0 || ask != 0 {
		t.Fatalf("nil chain: bid=%v ask=%v, want 0 0", bid, ask)
	}
}

func TestChaseBidPrice_SubtractsBufferAndRespectsFloorAndDiscountCap(t *testing.T) {
	// 100 - 2 = 98, well within the 10% discount cap (floor 90).
	if p := chaseBidPrice(100, 2); !nearlyEqual(p, 98) {
		t.Fatalf("chaseBidPrice(100,2) = %v, want 98", p)
	}
	// 100 - 50 would breach the 10% cap (floor 90) -> clamped to 90.
	if p := chaseBidPrice(100, 50); !nearlyEqual(p, 90) {
		t.Fatalf("chaseBidPrice(100,50) = %v, want 90 (10%% discount floor)", p)
	}
}
