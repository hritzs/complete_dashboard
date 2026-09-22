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

	// Build each leg's clips separately first, then interleave them below --
	// do NOT append straight into one shared slice leg by leg. Concatenating
	// leg by leg (the old behavior) builds one leg to completion before the
	// other leg's first order is even submitted, so a margin/RMS rejection
	// partway through the first leg (confirmed live 2026-09-22: a 40-lot
	// NIFTY build filled ~24 lots of CE, then every remaining order --
	// including all of PE, which had not even started yet -- was rejected
	// for insufficient margin) leaves a fully naked, unhedged position
	// instead of a small, roughly balanced one.
	perLegClips := make([][][]ExecOrder, 0, len(legs))

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

		var legClips [][]ExecOrder
		lotsRemaining := leg.TotalLots

		for lotsRemaining > 0 {
			lotsThisClip := minInt(lotsRemaining, lotsPerClip)
			qty := lotsThisClip * leg.LotSize

			if qty <= 0 {
				return nil, fmt.Errorf("invalid explicit quantity for token %d", leg.Token)
			}

			uid := fmt.Sprintf("%s_%d", tradeUIDPrefix, tsNow+int64(orderCounter))

			legClips = append(legClips, []ExecOrder{
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
			})

			orderCounter++
			lotsRemaining -= lotsThisClip
		}

		perLegClips = append(perLegClips, legClips)
	}

	return interleaveClips(perLegClips), nil
}

// interleaveClips round-robins clips across legs (CE clip 1, PE clip 1, CE
// clip 2, PE clip 2, ...) instead of exhausting one leg before starting the
// next. A leg with more clips than the others contributes its remaining
// clips at the end, once every other leg is exhausted.
func interleaveClips(perLegClips [][][]ExecOrder) [][]ExecOrder {
	total := 0
	for _, clips := range perLegClips {
		total += len(clips)
	}
	merged := make([][]ExecOrder, 0, total)

	for round := 0; ; round++ {
		addedThisRound := false
		for _, clips := range perLegClips {
			if round < len(clips) {
				merged = append(merged, clips[round])
				addedThisRound = true
			}
		}
		if !addedThisRound {
			break
		}
	}

	return merged
}
