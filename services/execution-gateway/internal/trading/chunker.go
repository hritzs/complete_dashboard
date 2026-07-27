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

	raw := 1
	if orderLotsPerCall > 0 {
		raw = orderLotsPerCall
	} else if baseLots > 0 {
		raw = int(math.Ceil(float64(baseLots) / 100.0))
	}

	minLotsPerOrder := minInt(raw, maxLotsPerOrder)
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

		lotsBase := leg.TotalLots / chunkDivisor
		lotsRem := leg.TotalLots % chunkDivisor

		for c := 0; c < chunkDivisor; c++ {
			lotsThisChunk := lotsBase
			if c < lotsRem {
				lotsThisChunk++
			}
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
		var interleaved []ExecOrder
		maxOrders := 0

		for _, lc := range legChunks {
			if len(lc[c]) > maxOrders {
				maxOrders = len(lc[c])
			}
		}

		for i := 0; i < maxOrders; i++ {
			for _, lc := range legChunks {
				if i < len(lc[c]) {
					interleaved = append(interleaved, lc[c][i])
				}
			}
		}

		if len(interleaved) > 0 {
			allChunks = append(allChunks, interleaved)
		}
	}

	return allChunks, nil
}
