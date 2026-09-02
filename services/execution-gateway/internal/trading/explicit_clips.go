package trading

import (
	"fmt"
	"time"
)

// GenerateExplicitClips creates sequential broker clips when the UI
// explicitly chooses lots per order. It does NOT use the seven-bucket
// chunker at all.
//
// Example: NIFTY TotalLots=2, lotsPerClip=2, LotSize=65
// -> one ExecOrder with Quantity=130.
func GenerateExplicitClips(
	tradeUIDPrefix string,
	legs []LegData,
	lotsPerClip int,
	maxOrderQty int,
) ([][]ExecOrder, error) {
	if len(legs) == 0 {
		return nil, nil
	}

	if lotsPerClip <= 0 {
		return nil, fmt.Errorf("lotsPerClip must be > 0, got %d", lotsPerClip)
	}

	lotSizeForCalc := legs[0].LotSize
	if lotSizeForCalc <= 0 {
		return nil, fmt.Errorf("invalid lot size for clipping: %d", lotSizeForCalc)
	}

	if maxOrderQty <= 0 {
		return nil, fmt.Errorf("invalid max order qty: %d", maxOrderQty)
	}

	maxLotsPerClip := maxOrderQty / lotSizeForCalc
	if maxLotsPerClip <= 0 {
		maxLotsPerClip = 1
	}

	lotsPerClip = minInt(lotsPerClip, maxLotsPerClip)

	tsNow := time.Now().UnixMicro()
	orderCounter := 0

	var allClips [][]ExecOrder

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

		if leg.TotalLots == 0 {
			continue
		}

		lotsRemaining := leg.TotalLots

		for lotsRemaining > 0 {
			lotsThisClip := minInt(lotsRemaining, lotsPerClip)
			qty := lotsThisClip * leg.LotSize

			if qty <= 0 {
				return nil, fmt.Errorf("invalid explicit quantity for token %d", leg.Token)
			}

			uid := fmt.Sprintf("%s_%d", tradeUIDPrefix, tsNow+int64(orderCounter))

			clip := []ExecOrder{
				{
					UID:             uid,
					Token:           leg.Token,
					Symbol:          leg.Symbol,
					OptionType:      leg.OptionType,
					Action:          leg.Action,
					Quantity:        qty,
					ExpectedPrice:   leg.ExpectedPrice,
					ExchangeSegment: leg.ExchangeSegment,
				},
			}

			allClips = append(allClips, clip)

			orderCounter++
			lotsRemaining -= lotsThisClip
		}
	}

	return allClips, nil
}
