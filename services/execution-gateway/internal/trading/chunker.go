package trading

import (
	"fmt"
	"math"
	"time"
)

type LegData struct {
	Token           int64
	Symbol          string
	OptionType      string
	Action          string
	TotalLots       int
	LotSize         int
	ExpectedPrice   float64
	ExchangeSegment string
}

type ExecOrder struct {
	UID             string
	Token           int64
	Symbol          string
	OptionType      string
	Action          string
	Quantity        int
	ExpectedPrice   float64
	LimitPrice      float64
	ExchangeSegment string
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func GenerateChunkedOrders(
	tradeUIDPrefix string,
	legs []LegData,
	baseLots int,
	maxOrderQty int,
	orderLotsPerCall int,
) ([][]ExecOrder, error) {
	// Call internal with aggressive=false
	return generateChunkedOrdersInternal(tradeUIDPrefix, legs, baseLots, maxOrderQty, orderLotsPerCall, false)
}

// GenerateAggressiveChunkedOrders generates orders with maximum lots per order.
// Used for SL-triggered square-off to exit positions as fast as possible.
func GenerateAggressiveChunkedOrders(
	tradeUIDPrefix string,
	legs []LegData,
	baseLots int,
	maxOrderQty int,
) ([][]ExecOrder, error) {
	// Call internal with aggressive=true
	return generateChunkedOrdersInternal(tradeUIDPrefix, legs, baseLots, maxOrderQty, 0, true)
}

// generateChunkedOrdersInternal is the shared implementation.
// aggressive=true → use max lots/order (SL SQF)
// aggressive=false → use orderLotsPerCall or range-based auto (BUILD, SQF, PSQF, Hedge)
func generateChunkedOrdersInternal(
	tradeUIDPrefix string,
	legs []LegData,
	baseLots int,
	maxOrderQty int,
	orderLotsPerCall int,
	aggressive bool,
) ([][]ExecOrder, error) {
	if len(legs) == 0 {
		return nil, nil
	}

	chunkDivisor := 7
	lotSizeForCalc := legs[0].LotSize
	if lotSizeForCalc <= 0 {
		return nil, fmt.Errorf("invalid lot size for chunking: %d", lotSizeForCalc)
	}
	if maxOrderQty <= 0 {
		return nil, fmt.Errorf("invalid max order qty: %d", maxOrderQty)
	}

	maxLotsPerOrder := maxInt(1, maxOrderQty/lotSizeForCalc)

	var minLotsPerOrder int
	if aggressive {
		// SL SQF — use maximum possible order size to exit fastest
		minLotsPerOrder = maxLotsPerOrder
	} else if orderLotsPerCall > 0 {
		// Manual UI override
		minLotsPerOrder = minInt(orderLotsPerCall, maxLotsPerOrder)
	} else {
		// Range-based auto (BUILD default, SQF, PSQF, Hedge)
		raw := 1
		if baseLots > 0 {
			raw = int(math.Ceil(float64(baseLots) / 100.0))
		}
		minLotsPerOrder = minInt(raw, maxLotsPerOrder)
	}

	if minLotsPerOrder <= 0 {
		minLotsPerOrder = 1
	}

	tsNow := time.Now().UnixMicro()
	orderCounter := 0

	var legChunks [][][]ExecOrder

	for _, leg := range legs {
		if leg.Token <= 0 {
			return nil, fmt.Errorf("invalid leg token: %d", leg.Token)
		}
		if leg.LotSize <= 0 {
			return nil, fmt.Errorf("invalid leg lot size for token %d: %d", leg.Token, leg.LotSize)
		}
		if leg.TotalLots < 0 {
			return nil, fmt.Errorf("invalid total lots for token %d: %d", leg.Token, leg.TotalLots)
		}

		chunks := make([][]ExecOrder, chunkDivisor)
		for i := range chunks {
			chunks[i] = []ExecOrder{}
		}

		if leg.TotalLots == 0 {
			legChunks = append(legChunks, chunks)
			continue
		}

		// Every leg's cumulative lots after chunk c are round(total*(c+1)/7),
		// so all legs reach the same fraction together and keep the
		// position's own ratio (37 / 40 lots: 5/6, 11/11, 16/17 ... instead of
		// the remainders front-loaded into the first chunks).
		for c := 0; c < chunkDivisor; c++ {
			lotsThisChunk := int(math.Round(float64(leg.TotalLots)*float64(c+1)/float64(chunkDivisor))) -
				int(math.Round(float64(leg.TotalLots)*float64(c)/float64(chunkDivisor)))
			if lotsThisChunk == 0 {
				continue
			}

			nFull := lotsThisChunk / minLotsPerOrder
			remLots := lotsThisChunk % minLotsPerOrder

			for i := 0; i < nFull; i++ {
				qty := minLotsPerOrder * leg.LotSize
				if qty <= 0 {
					return nil, fmt.Errorf("invalid generated quantity for token %d", leg.Token)
				}

				uid := fmt.Sprintf("%s_%d", tradeUIDPrefix, tsNow+int64(orderCounter))
				chunks[c] = append(chunks[c], ExecOrder{
					UID:             uid,
					Token:           leg.Token,
					Symbol:          leg.Symbol,
					OptionType:      leg.OptionType,
					Action:          leg.Action,
					Quantity:        qty,
					ExpectedPrice:   leg.ExpectedPrice,
					ExchangeSegment: leg.ExchangeSegment,
				})
				orderCounter++
			}

			if remLots > 0 {
				qty := remLots * leg.LotSize
				if qty <= 0 {
					return nil, fmt.Errorf("invalid remainder quantity for token %d", leg.Token)
				}

				uid := fmt.Sprintf("%s_%d", tradeUIDPrefix, tsNow+int64(orderCounter))
				chunks[c] = append(chunks[c], ExecOrder{
					UID:             uid,
					Token:           leg.Token,
					Symbol:          leg.Symbol,
					OptionType:      leg.OptionType,
					Action:          leg.Action,
					Quantity:        qty,
					ExpectedPrice:   leg.ExpectedPrice,
					ExchangeSegment: leg.ExchangeSegment,
				})
				orderCounter++
			}
		}

		legChunks = append(legChunks, chunks)
	}

	var allChunks [][]ExecOrder
	for c := 0; c < chunkDivisor; c++ {
		// Inside the chunk each leg goes out in proportion to its size
		// (furthest behind its own share next), not strictly alternating.
		perLeg := make([][][]ExecOrder, 0, len(legChunks))
		for _, lc := range legChunks {
			one := make([][]ExecOrder, 0, len(lc[c]))
			for _, o := range lc[c] {
				one = append(one, []ExecOrder{o})
			}
			perLeg = append(perLeg, one)
		}
		var interleaved []ExecOrder
		for _, o := range interleaveClips(perLeg) {
			interleaved = append(interleaved, o...)
		}

		if len(interleaved) > 0 {
			allChunks = append(allChunks, interleaved)
		}
	}

	return allChunks, nil
}

// interleaveClips merges each leg's orders so every leg goes out in
// proportion to its own size (see the body).
func interleaveClips(perLegClips [][][]ExecOrder) [][]ExecOrder {
	// Each leg in proportion to its own size: the leg furthest behind its
	// share goes next (ties: first leg), so CE 400 / PE 370 clips stay in
	// ratio all the way through instead of ending with 30 CE clips alone.
	total := 0
	for _, clips := range perLegClips {
		total += len(clips)
	}
	merged := make([][]ExecOrder, 0, total)
	emitted := make([]int, len(perLegClips))
	for len(merged) < total {
		best, bestKey := -1, 0.0
		for li, clips := range perLegClips {
			if emitted[li] >= len(clips) {
				continue
			}
			k := (float64(emitted[li]) + 0.5) / float64(len(clips))
			if best < 0 || k < bestKey-1e-12 {
				best, bestKey = li, k
			}
		}
		merged = append(merged, perLegClips[best][emitted[best]])
		emitted[best]++
	}
	return merged
}
