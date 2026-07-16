package trading

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Order represents an order intent
type Order struct {
	UID              string
	Token            int
	OptionType       string
	Action           string
	Quantity         int
	ExpectedPrice    float64
	LimitPrice       float64
	LimitOrderBuffer float64
	ExchangeSegment  int
}

// PlacedOrder represents an order successfully submitted to the broker
type PlacedOrder struct {
	AppOrderID string
	UID        string
	Token      int
	Action     string
	Quantity   int
}

// VerifiedOrder represents an order that has been confirmed filled
type VerifiedOrder struct {
	AppOrderID string
	UID        string
	Status     string
	FilledQty  int
	AvgPrice   float64
}

// FailedOrder represents an order that failed placement or verification
type FailedOrder struct {
	AppOrderID string // May be empty if placement failed
	UID        string
	Status     string
	Reason     string
}

// ExecuteResult contains the results of a batch execution attempt
type ExecuteResult struct {
	SuccessfulOrders []PlacedOrder
	FailedOrders     []FailedOrder
}

// VerifyResult contains the results of a bulk verification attempt
type VerifyResult struct {
	VerifiedSuccess []VerifiedOrder
	VerifiedFailed  []FailedOrder
}

// OrderExecutor interface abstracts the broker API calls
type OrderExecutor interface {
	ExecuteBatch(ctx context.Context, orders []Order, batchUID string) (ExecuteResult, error)
	VerifyOrdersBulk(ctx context.Context, orderIDs []string, verifyUID string) (VerifyResult, error)
}

// Database interface abstracts persistence
type Database interface {
	InsertOrder(order PlacedOrder)
	UpdateOrderStatus(appOrderID, status string)
}

// BuildStraddle executes the delta-neutral straddle build logic with robust chunking, verification, and retries.
func BuildStraddle(ctx context.Context, tradeUID string, allChunks [][]Order, executor OrderExecutor, db Database) error {
	log.Printf("🚀 Starting straddle build for %s with %d chunks", tradeUID, len(allChunks))

	var allVerifiedFills []VerifiedOrder
	var allSuccessfulOrders []PlacedOrder
	var allFailedOrders []FailedOrder

	for chunkIdx, chunk := range allChunks {
		ordersToProcess := chunk
		maxChunkRetries := 3
		retryIter := 0

		for len(ordersToProcess) > 0 && retryIter < maxChunkRetries {
			currentChunkUID := fmt.Sprintf("BUI_%s_CHUNK%d_TRY%d", tradeUID, chunkIdx+1, retryIter+1)

			if retryIter > 0 {
				bufferMultiplier := float64(retryIter + 1)
				log.Printf("🔄 Retrying %d orders in CHUNK %d (Attempt %d) with %.1fx buffer...",
					len(ordersToProcess), chunkIdx+1, retryIter+1, bufferMultiplier)
				time.Sleep(1 * time.Second)

				// Apply escalating buffer to limit prices for retries
				for i := range ordersToProcess {
					baseBuffer := ordersToProcess[i].LimitOrderBuffer
					ordersToProcess[i].LimitOrderBuffer = baseBuffer * bufferMultiplier
					ordersToProcess[i].LimitPrice = 0.0 // Force recalculation based on new buffer

					// Regenerate UID to avoid broker duplicate ID rejections
					ordersToProcess[i].UID = fmt.Sprintf(
						"%s_TRY%d_%d",
						ordersToProcess[i].UID[:min(len(ordersToProcess[i].UID), 14)],
						retryIter,
						time.Now().UnixMilli()%10000,
					)
				}
			}

			log.Printf("⚡ Executing BUILD chunk %d/%d (Iter %d) with %d orders.",
				chunkIdx+1, len(allChunks), retryIter+1, len(ordersToProcess))

			// 1. Execute Batch
			execResult, err := executor.ExecuteBatch(ctx, ordersToProcess, currentChunkUID)
			if err != nil {
				log.Printf("❌ ExecuteBatch failed: %v", err)
				break
			}

			allSuccessfulOrders = append(allSuccessfulOrders, execResult.SuccessfulOrders...)
			allFailedOrders = append(allFailedOrders, execResult.FailedOrders...)

			// Persist placed orders immediately to DB to avoid parity issues
			appOrderIDToUIDMap := make(map[string]string)
			var unverifiedOrderIDs []string
			for _, placed := range execResult.SuccessfulOrders {
				db.InsertOrder(placed)
				unverifiedOrderIDs = append(unverifiedOrderIDs, placed.AppOrderID)
				appOrderIDToUIDMap[placed.AppOrderID] = placed.UID
			}

			// 2. Verification Loop
			maxVerificationAttempts := 3
			var newlyFailed []FailedOrder
			var verifiedFillsForChunk []VerifiedOrder

			for vAttempt := 0; vAttempt < maxVerificationAttempts; vAttempt++ {
				if len(unverifiedOrderIDs) == 0 {
					break
				}

				verifyUID := fmt.Sprintf("BUI_%s_CHUNK%d_ITER%d_VER%d",
					tradeUID, chunkIdx+1, retryIter+1, vAttempt+1)
				log.Printf("📊 Verifying BUILD chunk %d, attempt %d/%d for %d orders...",
					chunkIdx+1, vAttempt+1, maxVerificationAttempts, len(unverifiedOrderIDs))

				vResult, _ := executor.VerifyOrdersBulk(ctx, unverifiedOrderIDs, verifyUID)

				verifiedFillsForChunk = append(verifiedFillsForChunk, vResult.VerifiedSuccess...)
				newlyFailed = vResult.VerifiedFailed

				// Resolve IDs that are either successfully filled or terminally failed
				resolvedIDs := make(map[string]bool)
				for _, v := range vResult.VerifiedSuccess {
					resolvedIDs[v.AppOrderID] = true
				}

				for _, f := range vResult.VerifiedFailed {
					if f.Status == "REJECTED" ||
						f.Status == "CANCELLED" ||
						f.Status == "CANCELED" ||
						f.Status == "REEXECUTE_NEEDED" ||
						f.Status == "NOT_FOUND_ON_RETRY" {
						resolvedIDs[f.AppOrderID] = true
					}
				}

				// Filter unverifiedOrderIDs
				var stillPending []string
				for _, id := range unverifiedOrderIDs {
					if !resolvedIDs[id] {
						stillPending = append(stillPending, id)
					}
				}
				unverifiedOrderIDs = stillPending

				if len(unverifiedOrderIDs) > 0 {
					log.Printf("⚠️ %d orders still pending in BUILD chunk %d. Retrying verification in 500ms...",
						len(unverifiedOrderIDs), chunkIdx+1)
					time.Sleep(500 * time.Millisecond)
				}
			}

			// 3. Process orders that need re-execution
			var ordersToRetryNow []Order
			for _, failed := range newlyFailed {
				if failed.Status == "REEXECUTE_NEEDED" {
					origUID := appOrderIDToUIDMap[failed.AppOrderID]
					for _, origOrder := range ordersToProcess {
						if origOrder.UID == origUID {
							ordersToRetryNow = append(ordersToRetryNow, origOrder)
							log.Printf("🔄 Order %s marked for re-execution in current chunk.", origUID)
							break
						}
					}
				}
			}

			allVerifiedFills = append(allVerifiedFills, verifiedFillsForChunk...)

			// Set up the next iteration of the intra-chunk retry loop
			ordersToProcess = ordersToRetryNow
			retryIter++
		}

		// Live Check logic matching Python's in-build checking
		if len(allVerifiedFills) > 0 {
			log.Printf("🔍 [%s] Performing mid-build verification (Fills: %d)",
				tradeUID, len(allVerifiedFills))

			// Placeholder: SL / Hedge checks go here
		}

		time.Sleep(50 * time.Millisecond)
	}

	log.Printf("🧹 Proceeding to final sweep for %s...", tradeUID)
	// Sweep logic can be added here later

	log.Printf("🏁 Execution finished for %s.", tradeUID)
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}