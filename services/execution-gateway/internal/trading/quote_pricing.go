package trading

import "math"

// bidAskBufferSchedule is the rupee buffer applied at each live-quote
// pricing attempt for a build leg: 1 on the initial order, 2 on the first
// chase, 4 on the second chase. Each attempt re-prices off a FRESH quote
// fetched at that moment, never the previous attempt's price.
var bidAskBufferSchedule = []float64{1, 2, 4}

// bufferForAttempt returns the schedule's buffer for a 1-indexed pricing
// attempt (1 = the initial order, 2 = first chase, 3 = second chase, ...).
// Attempts beyond the schedule keep doubling from its last value, so a
// build that somehow needs more rounds than specified still gets more
// aggressive each time rather than reusing a stale buffer forever.
func bufferForAttempt(attempt int) float64 {
	if attempt < 1 {
		attempt = 1
	}
	if attempt <= len(bidAskBufferSchedule) {
		return bidAskBufferSchedule[attempt-1]
	}
	buf := bidAskBufferSchedule[len(bidAskBufferSchedule)-1]
	for i := len(bidAskBufferSchedule); i < attempt; i++ {
		buf *= 2
	}
	return buf
}

// bidAskLimitPrice prices a SELL off the live bid minus buffer, or a BUY off
// the live ask plus buffer, floored at 0.05 and rounded to the tick. ok is
// false when the quote side this order needs is missing (<= 0), so the
// caller can fall back to LTP-based pricing rather than submit a bogus
// price.
func bidAskLimitPrice(side string, bid, ask, buffer float64) (price float64, ok bool) {
	var p float64
	switch side {
	case "SELL":
		if bid <= 0 {
			return 0, false
		}
		p = bid - buffer
	case "BUY":
		if ask <= 0 {
			return 0, false
		}
		p = ask + buffer
	default:
		return 0, false
	}
	if p < 0.05 {
		p = 0.05
	}
	return math.Round(p/0.05) * 0.05, true
}

// bidAskForToken finds the live bid/ask for a token's leg (CE or PE) in a
// chain snapshot. Returns (0, 0) if the token isn't in the chain.
func bidAskForToken(chain *OptionChainSnapshot, token int64) (bid, ask float64) {
	if chain == nil {
		return 0, 0
	}
	for _, row := range chain.Chain {
		if row.CEToken == token {
			return row.CEBid, row.CEAsk
		}
		if row.PEToken == token {
			return row.PEBid, row.PEAsk
		}
	}
	return 0, 0
}
