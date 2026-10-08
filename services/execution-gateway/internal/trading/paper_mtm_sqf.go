package trading

// MTM square-off for the paper simulation (PAPER ONLY -- never sends an
// order). Its own exit type, separate from SL / TP / exit time: once the
// whole trade can be closed at EXECUTABLE prices with total MTM >= the
// level, it is closed lot by lot -- each lot only after checking that,
// filled at the price it would actually get, the trade still ends at or
// above the level; if not, it stops and re-checks next minute.
//
// Executable prices: a short option is bought back at the ask, a long one
// sold at the bid (the recorded minute's best bid/ask; LTP when the
// minute has none), the synthetic future hedge at the future. Recorded
// minutes keep only the best bid/ask, not depth quantities, so each lot is
// assumed to fill at the top of the book; the live version uses the full
// 5-level depth.

import (
	"fmt"
	"math"
)

// mtmSqfFloor converts the configured level to rupees for qty q.
func mtmSqfFloor(c PaperSimConfig, entryFuture float64, q int64) float64 {
	switch c.MTMSqfUnit {
	case "pts":
		return c.MTMSqfLevel * float64(q)
	case "bps":
		return c.MTMSqfLevel * entryFuture / 10000 * float64(q)
	}
	return c.MTMSqfLevel
}

// simExecPrice is where an option position of signed qty would close now:
// a short buys at the ask, a long sells at the bid. ok=false: no bid/ask
// recorded for it this minute (LTP used instead).
func simExecPrice(m simMinute, K float64, opt string, qty int64) (px float64, ok bool) {
	if r, found := m.chain[K]; found {
		bid, ask := r.PEBid, r.PEAsk
		if opt == "CE" {
			bid, ask = r.CEBid, r.CEAsk
		}
		if qty < 0 && ask > 0 {
			return ask, true
		}
		if qty > 0 && bid > 0 {
			return bid, true
		}
	}
	px, _, _, _ = simPrice(m, K, opt)
	return px, false
}

// execMTM is the trade's total MTM if every open leg were closed now at
// executable prices (realized + open at bid/ask + synthetic future).
func (b *simBook) execMTM(m simMinute) (mtm float64, allQuoted bool) {
	allQuoted = true
	for _, l := range b.legs {
		mtm += l.realized
		if l.qty == 0 {
			continue
		}
		px, ok := simExecPrice(m, l.K, l.opt, l.qty)
		allQuoted = allQuoted && ok
		mtm += (px - l.avg) * float64(l.qty)
	}
	mtm += b.futRealized + b.futQty*(m.Future-b.futAvg)
	return mtm, allQuoted
}

func (b *simBook) clone() *simBook {
	cp := &simBook{legs: make(map[string]*simLeg, len(b.legs)), futQty: b.futQty, futAvg: b.futAvg, futRealized: b.futRealized}
	for k, l := range b.legs {
		v := *l
		cp.legs[k] = &v
	}
	return cp
}

// buildLots is the open BUILD straddle size in lots (the larger leg).
func (b *simBook) buildLots(lot int64) int64 {
	var n int64
	for _, l := range b.legs {
		if l.role == "BUILD" && l.qty != 0 {
			n = max(n, (abs64(l.qty)+lot-1)/lot)
		}
	}
	return n
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// closeOneLot closes one lot of every BUILD leg at executable prices and
// the matching share of every hedge (the rest of them on the last lot).
func (b *simBook) closeOneLot(m simMinute, lot int64) {
	n := b.buildLots(lot)
	if n <= 0 {
		return
	}
	share := 1 / float64(n) // this lot's share of what is still open
	for _, l := range b.legs {
		if l.qty == 0 {
			continue
		}
		qn := min(lot, abs64(l.qty))
		if l.role != "BUILD" {
			qn = abs64(l.qty)
			if n > 1 {
				qn = int64(math.Round(float64(abs64(l.qty)) * share))
			}
		}
		if qn <= 0 {
			continue
		}
		px, _ := simExecPrice(m, l.K, l.opt, l.qty)
		side := "BUY"
		if l.qty > 0 {
			side = "SELL"
		}
		b.fill(l.role, l.K, l.opt, side, qn, px)
	}
	if b.futQty != 0 {
		b.fillFut(-b.futQty*share, m.Future)
	}
}

// mtmSqfStep runs the rule on one minute: if the executable MTM is at or
// above floor, close up to wantLots lots, each only if the trade still
// ends at or above floor after it. Returns the lots closed and an event.
func mtmSqfStep(b *simBook, m simMinute, lot int64, floor float64, wantLots int64) (closed int64, event string) {
	mtm, quoted := b.execMTM(m)
	if mtm < floor {
		return 0, ""
	}
	for closed < wantLots && b.buildLots(lot) > 0 {
		trial := b.clone()
		trial.closeOneLot(m, lot)
		if after, _ := trial.execMTM(m); after < floor {
			break // this lot's fills would take the trade below the level
		}
		*b = *trial
		closed++
	}
	if closed == 0 {
		return 0, ""
	}
	end, _ := b.execMTM(m)
	px := "bid/ask"
	if !quoted {
		px = "bid/ask (LTP where none recorded)"
	}
	return closed, fmt.Sprintf("MTM SQF %d lot(s) at %s: MTM %.2f >= level %.2f", closed, px, end, floor)
}
