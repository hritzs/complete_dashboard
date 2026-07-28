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

	// 3. Create closing intents. Since the original trade was a SELL straddle, we BUY to close.
	// The quantities are taken directly from the StoredTrade struct.
	ceIntent := OrderIntent{
		IntentID:        fmt.Sprintf("INT_SQF_%s_CE_%d", tradeUID, time.Now().UnixMilli()),
		TradeUID:        tradeUID,
		Token:           trade.CEToken,
		Symbol:          trade.Symbol,
		ExchangeSegment: trade.ExchangeSegment,
		Side:            "BUY", // Closing a SELL
		Quantity:        int64(trade.CEQty),
		OrderType:       "MARKET", // Using MARKET for simplicity to ensure exit
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
		Side:            "BUY", // Closing a SELL
		Quantity:        int64(trade.PEQty),
		OrderType:       "MARKET",
		ProductType:     trade.ProductType,
		LegType:         "PE",
		Phase:           "MANUAL_SQF",
		BrokerName:      trade.BrokerName,
		AccountID:       trade.AccountID,
	}

	intents := []OrderIntent{ceIntent, peIntent}
	results := make(chan *ExecutionResult, len(intents))
	var wg sync.WaitGroup

	// 4. Execute both intents concurrently.
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
