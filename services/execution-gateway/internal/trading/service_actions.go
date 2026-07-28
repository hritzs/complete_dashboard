package trading

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// This file would contain action methods for the Service struct.
// Assumes a Service struct exists like:
/*
type Service struct {
	store         Store
	BrokerFactory *BrokerFactory
	// ... other fields
}
*/

// ManualSquareOff creates and executes closing orders for a trade based on its
// initial parameters, not its current reconciled position. This provides a direct
// way to exit a trade.
func (s *Service) ManualSquareOff(ctx context.Context, tradeUID string) ([]*ExecutionResult, error) {
	log.Printf("[SQF] Starting MANUAL square-off for trade_uid=%s", tradeUID)

	// 1. Load the trade from the store to get its original parameters.
	trade, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return nil, fmt.Errorf("trade %s not found for manual square-off", tradeUID)
	}

	// 2. Get the appropriate broker executor.
	executor, err := s.BrokerFactory.GetExecutor(trade.UserID, trade.BrokerName, trade.AccountID)
	if err != nil {
		return nil, fmt.Errorf("could not get executor for broker %s: %w", trade.BrokerName, err)
	}

	// 3. Fetch live market data to get a reasonable price for the closing orders.
	chain, err := s.Snapshot.GetOptionChain(ctx, trade.Symbol, trade.Expiry)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch option chain for square-off pricing: %w", err)
	}

	var ceRow, peRow *OptionChainRow
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if row.Strike == trade.Strike {
			ceRow = row
			peRow = row
			break
		}
	}

	if ceRow == nil || peRow == nil {
		return nil, fmt.Errorf("could not find strike %.2f in live option chain for %s", trade.Strike, trade.Symbol)
	}

	// For a BUY order, we use the Ask price to increase fill probability.
	// A small buffer is added to cross the spread if necessary.
	ceClosePrice := ceRow.CEAsk + 0.05
	peClosePrice := peRow.PEAsk + 0.05

	log.Printf("[SQF] Determined closing prices: CE Ask=%.2f -> Limit=%.2f, PE Ask=%.2f -> Limit=%.2f",
		ceRow.CEAsk, ceClosePrice, peRow.PEAsk, peClosePrice)

	// 4. Create closing intents as LIMIT orders.
	ceIntent := OrderIntent{
		IntentID:        fmt.Sprintf("INT_SQF_%s_CE_%d", tradeUID, time.Now().UnixMilli()),
		TradeUID:        tradeUID,
		Token:           trade.CEToken,
		Symbol:          trade.Symbol,
		ExchangeSegment: trade.ExchangeSegment,
		Side:            "BUY",
		Quantity:        int64(trade.CEQty),
		OrderType:       "LIMIT",
		LimitPrice:      &ceClosePrice,
		ProductType:     trade.ProductType,
		LegType:         "CE",
		Phase:           "MANUAL_SQF",
		BrokerName:      trade.BrokerName,
		AccountID:       trade.AccountID,
	}

	peIntent := OrderIntent{
		IntentID:        fmt.Sprintf("INT_SQF_%s_PE_%d", tradeUID, time.Now().UnixMilli()),
		TradeUID:        tradeUID,
		Token:           trade.PEToken,
		Symbol:          trade.Symbol,
		ExchangeSegment: trade.ExchangeSegment,
		Side:            "BUY",
		Quantity:        int64(trade.PEQty),
		OrderType:       "LIMIT",
		LimitPrice:      &peClosePrice,
		ProductType:     trade.ProductType,
		LegType:         "PE",
		Phase:           "MANUAL_SQF",
		BrokerName:      trade.BrokerName,
		AccountID:       trade.AccountID,
	}

	intents := []OrderIntent{ceIntent, peIntent}
	results := make(chan *ExecutionResult, len(intents))
	var wg sync.WaitGroup

	// 5. Execute both intents concurrently.
	for _, intent := range intents {
		wg.Add(1)
		go func(i OrderIntent) {
			defer wg.Done()
			log.Printf("[SQF] Executing manual square-off intent: leg=%s, qty=%d, token=%d", i.LegType, i.Quantity, i.Token)
			res, err := executor.ExecuteOrderIntent(ctx, i)
			if err != nil {
				log.Printf("[SQF] ERROR executing intent %s: %v", i.IntentID, err)
				// Create a synthetic failure result
				res = &ExecutionResult{IntentID: i.IntentID, Status: "FAILED", EventReason: err.Error()}
			}
			results <- res
		}(intent)
	}

	wg.Wait()
	close(results)

	var finalResults []*ExecutionResult
	for res := range results {
		finalResults = append(finalResults, res)
	}

	log.Printf("[SQF] Finished MANUAL square-off for trade_uid=%s with %d results.", tradeUID, len(finalResults))
	return finalResults, nil
}
