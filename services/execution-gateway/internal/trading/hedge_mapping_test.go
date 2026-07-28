package trading

import "testing"

func TestHedgeSidesFromNegativeDelta(t *testing.T) {
	ceSide, peSide, shouldHedge := hedgeSidesFromNetDelta(-25.0)

	if !shouldHedge {
		t.Fatalf("expected hedge for negative delta")
	}

	if ceSide != "BUY" || peSide != "SELL" {
		t.Fatalf("expected CE BUY / PE SELL, got CE %s / PE %s", ceSide, peSide)
	}
}

func TestHedgeSidesFromPositiveDelta(t *testing.T) {
	ceSide, peSide, shouldHedge := hedgeSidesFromNetDelta(25.0)

	if !shouldHedge {
		t.Fatalf("expected hedge for positive delta")
	}

	if ceSide != "SELL" || peSide != "BUY" {
		t.Fatalf("expected CE SELL / PE BUY, got CE %s / PE %s", ceSide, peSide)
	}
}

func TestHedgeSidesFromSmallDeltaDoesNotHedge(t *testing.T) {
	ceSide, peSide, shouldHedge := hedgeSidesFromNetDelta(0.75)

	if shouldHedge {
		t.Fatalf("did not expect hedge for small delta")
	}

	if ceSide != "" || peSide != "" {
		t.Fatalf("expected empty sides for no hedge, got CE %s / PE %s", ceSide, peSide)
	}
}
