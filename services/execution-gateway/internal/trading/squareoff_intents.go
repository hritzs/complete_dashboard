package trading

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type sqfNetPosition struct {
	Symbol     string
	LegType    string
	Token      int64
	NetQty     int64
	AvgPrice   float64
	AbsQtySeen int64
}

func (s *Service) createSQFIntentsFromFilledNetPosition(tr StoredTrade) (int, error) {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok || pg == nil || pg.DB() == nil {
		return 0, fmt.Errorf("postgres-backed store is required for square-off intent creation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := pg.DB().QueryContext(ctx, `
		SELECT
			COALESCE(intent_id, ''),
			COALESCE(order_uid, ''),
			COALESCE(side, ''),
			COALESCE(filled_qty, 0),
			COALESCE(avg_fill_price, 0),
			COALESCE(raw_broker_request::text, '')
		FROM orders
		WHERE trade_uid = $1
		  AND COALESCE(filled_qty, 0) > 0
		  AND COALESCE(status, '') IN ('FILLED', 'SUCCESS')
		ORDER BY created_at ASC, id ASC
	`, tr.TradeUID)
	if err != nil {
		return 0, fmt.Errorf("query filled orders for square-off: %w", err)
	}
	defer rows.Close()

	positions := map[string]*sqfNetPosition{}

	for rows.Next() {
		var intentID string
		var orderUID string
		var side string
		var filledQty int64
		var avgPrice float64
		var rawReq string

		if err := rows.Scan(
			&intentID,
			&orderUID,
			&side,
			&filledQty,
			&avgPrice,
			&rawReq,
		); err != nil {
			return 0, fmt.Errorf("scan filled order for square-off: %w", err)
		}

		if filledQty <= 0 {
			continue
		}

		o := ExecutionOrderSummary{
			IntentID:     intentID,
			OrderUID:     orderUID,
			Side:         side,
			FilledQty:    filledQty,
			AvgFillPrice: avgPrice,
			Symbol:       tr.Symbol,
		}
		enrichOrderFromRawIntent(&o, rawReq)

		if o.Symbol == "" {
			o.Symbol = tr.Symbol
		}

		if o.Token <= 0 {
			continue
		}

		key := fmt.Sprintf("%s|%s|%d", o.Symbol, o.LegType, o.Token)

		pos := positions[key]
		if pos == nil {
			pos = &sqfNetPosition{
				Symbol:  o.Symbol,
				LegType: o.LegType,
				Token:   o.Token,
			}
			positions[key] = pos
		}

		signedQty := filledQty
		if strings.ToUpper(strings.TrimSpace(side)) == "SELL" {
			signedQty = -filledQty
		}

		pos.NetQty += signedQty
		pos.AvgPrice += avgPrice * float64(absInt64(signedQty))
		pos.AbsQtySeen += absInt64(signedQty)
	}

	count := 0
	now := time.Now()

	for _, pos := range positions {
		if pos.NetQty == 0 {
			continue
		}

		closeQty := absInt64(pos.NetQty)
		if closeQty <= 0 {
			continue
		}

		closeSide := "SELL"
		if pos.NetQty < 0 {
			closeSide = "BUY"
		}

		expectedPrice := 0.05
		if pos.AbsQtySeen > 0 {
			expectedPrice = pos.AvgPrice / float64(pos.AbsQtySeen)
			if expectedPrice <= 0 {
				expectedPrice = 0.05
			}
		}

		legCode := "SQF"
		if strings.EqualFold(pos.LegType, "CE") {
			legCode = "SCE"
		} else if strings.EqualFold(pos.LegType, "PE") {
			legCode = "SPE"
		}

		limitPrice := expectedPrice
		if limitPrice <= 0 {
			limitPrice = 0.05
		}

		intent := OrderIntent{
			IntentID:        BuildShortOrderUID(tr.Symbol, legCode, now, count),
			TradeUID:        tr.TradeUID,
			Token:           pos.Token,
			Symbol:          tr.Symbol,
			ExchangeSegment: tr.ExchangeSegment,
			Side:            closeSide,
			Quantity:        closeQty,
			OrderType:       "LIMIT",
			LimitPrice:      &limitPrice,
			ProductType:     tr.ProductType,
			LegType:         pos.LegType,
			Phase:           "SQF",
			OrderUID:        BuildShortOrderUID(tr.Symbol, legCode, now, count+100),
			BrokerName:      tr.BrokerName,
			AccountID:       tr.AccountID,
			ExpectedPrice:   expectedPrice,
		}

		s.Store.AppendIntent(tr.TradeUID, intent)
		count++
	}

	if count == 0 {
		return 0, fmt.Errorf("no filled net position found for square-off")
	}

	return count, nil
}
