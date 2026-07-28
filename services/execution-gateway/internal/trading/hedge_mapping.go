package trading

import "math"

func hedgeSidesFromNetDelta(netDelta float64) (ceSide string, peSide string, shouldHedge bool) {
	if math.Abs(netDelta) < 1 {
		return "", "", false
	}

	if netDelta < 0 {
		return "BUY", "SELL", true
	}

	return "SELL", "BUY", true
}
