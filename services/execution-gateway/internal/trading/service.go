package trading

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu        sync.RWMutex
	trades    map[string]StoredTrade
	intents   map[string][]StoredIntent
	snapshots map[string]TradeSnapshot
	runtimes  map[string]*RuntimeTrade
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		trades:    map[string]StoredTrade{},
		intents:   map[string][]StoredIntent{},
		snapshots: map[string]TradeSnapshot{},
		runtimes:  map[string]*RuntimeTrade{},
	}
}

func (s *MemoryStore) SaveTrade(tr StoredTrade) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trades[tr.TradeUID] = tr
}

func (s *MemoryStore) LoadTrade(tradeUID string) (StoredTrade, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tr, ok := s.trades[tradeUID]
	return tr, ok
}

func (s *MemoryStore) AllTrades() []StoredTrade {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]StoredTrade, 0, len(s.trades))
	for _, tr := range s.trades {
		out = append(out, tr)
	}
	return out
}

func (s *MemoryStore) UpdateTrade(tr StoredTrade) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trades[tr.TradeUID] = tr
}

func (s *MemoryStore) AppendIntent(tradeUID string, intent OrderIntent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intents[tradeUID] = append(s.intents[tradeUID], StoredIntent{
		ReceivedAt: time.Now(),
		Intent:     intent,
	})
}

func (s *MemoryStore) LoadIntents(tradeUID string) []StoredIntent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]StoredIntent(nil), s.intents[tradeUID]...)
}

func (s *MemoryStore) SaveSnapshot(snapshot TradeSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snapshot.TradeUID] = snapshot
}

func (s *MemoryStore) LoadSnapshot(tradeUID string) (TradeSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[tradeUID]
	return snap, ok
}

func (s *MemoryStore) SaveRuntime(rt *RuntimeTrade) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimes[rt.Trade.TradeUID] = rt
}

func (s *MemoryStore) LoadRuntime(tradeUID string) (*RuntimeTrade, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.runtimes[tradeUID]
	return rt, ok
}

func (s *MemoryStore) DeleteRuntime(tradeUID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runtimes, tradeUID)
}

type Service struct {
	Store           Store
	DefaultClientID string
	BrokerFactory   BrokerFactory
	Snapshot        SnapshotProvider
	LotSize         LotSizeProvider

	// tradeLocks serializes exit-side actions (SquareOff, PartialSquareOff)
	// per trade UID, in-process. Without this, two nearly-simultaneous
	// calls for the same trade (e.g. a double-click, or a retry racing an
	// still-in-flight attempt) can both pass the LoadTrade/status check
	// before either writes "SQUARING_OFF" back, and both proceed to place
	// real exit orders concurrently. Confirmed as a real (not just
	// theoretical) risk: a live trade was found with two overlapping
	// square-off attempts, one of which briefly overbought a leg before a
	// corrective order fixed it. This only protects against races within
	// this one process, not a second process instance or a manual action
	// taken directly against the broker outside this platform.
	tradeLocks sync.Map // map[string]*sync.Mutex
}

func NewService(store Store, clientID string) *Service {
	return &Service{
		Store:           store,
		DefaultClientID: clientID,
		BrokerFactory:   NewDefaultBrokerFactory(),
		Snapshot:        NewSnapshotClient(),
		LotSize:         NewLotSizeClient(),
	}
}

// lockTrade serializes exit-side actions for one trade UID. Call it first
// thing and defer the returned unlock func.
func (s *Service) lockTrade(tradeUID string) func() {
	v, _ := s.tradeLocks.LoadOrStore(tradeUID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) DeployStraddle(ctx context.Context, req DeployStraddleRequest) (*DeployStraddleResponse, error) {
	start := time.Now()

	// Normalize inputs
	req.Symbol = NormalizeSymbol(req.Symbol)
	req.UserID = strings.TrimSpace(req.UserID)
	req.BrokerName = strings.ToUpper(strings.TrimSpace(req.BrokerName))
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.ProductType = strings.ToUpper(strings.TrimSpace(req.ProductType))
	req.ExchangeSegment = strings.ToUpper(strings.TrimSpace(req.ExchangeSegment))
	req.TargetExpiry = strings.TrimSpace(req.TargetExpiry)

	// Defaults
	if req.UserID == "" {
		req.UserID = "U001"
	}
	// Broker optional → default to XTS
	if req.BrokerName == "" {
		req.BrokerName = "XTS"
	}
	// Account optional → default by broker
	if req.AccountID == "" {
		if req.BrokerName == "GREEKSOFT" {
			req.AccountID = "HRITIK"
		} else {
			req.AccountID = s.DefaultClientID
		}
	}
	if req.ProductType == "" {
		if req.BrokerName == "GREEKSOFT" {
			req.ProductType = "NRML"
		} else {
			req.ProductType = "MIS"
		}
	}
	if req.Lots <= 0 {
		req.Lots = 1
	}
	if req.Symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	log.Printf(
		"DEBUG DeployStraddle broker=%q account=%q user=%q symbol=%q lots=%d expiry=%q exchange=%q",
		req.BrokerName, req.AccountID, req.UserID, req.Symbol, req.Lots, req.TargetExpiry, req.ExchangeSegment,
	)

	// Resolve / infer exchange segment (e.g., NIFTY → NSEFO, SENSEX → BSEFO)
	exchangeSegment := ResolveExchangeSegment(req.Symbol, req.ExchangeSegment)
	if exchangeSegment == "" {
		exchangeSegment = "NSEFO"
	}

	// Get broker executor (XTS in your case)
	executor, err := s.BrokerFactory.GetExecutor(req.UserID, req.BrokerName, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get executor: %w", err)
	}

	// Option chain snapshot
	chain, err := s.Snapshot.GetOptionChain(ctx, req.Symbol, req.TargetExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch option chain: %w", err)
	}

	var ceRow, peRow *OptionChainRow
	var selectedStrike float64

	useCustom := req.CEStrikePrice > 0 || req.PEStrikePrice > 0
	if useCustom {
		if req.CEStrikePrice <= 0 || req.PEStrikePrice <= 0 {
			return nil, fmt.Errorf("both ceStrikePrice and peStrikePrice are required")
		}

		ceRow, err = FindRowByStrike(*chain, req.CEStrikePrice)
		if err != nil {
			return nil, fmt.Errorf("CE strike lookup failed: %w", err)
		}

		peRow, err = FindRowByStrike(*chain, req.PEStrikePrice)
		if err != nil {
			return nil, fmt.Errorf("PE strike lookup failed: %w", err)
		}

		selectedStrike = (ceRow.Strike + peRow.Strike) / 2
	} else {
		atmRow, err := FindATMRow(*chain)
		if err != nil {
			return nil, err
		}
		ceRow = atmRow
		peRow = atmRow
		selectedStrike = atmRow.Strike
	}

	// Tokens (override if request supplied)
	ceToken := req.CEToken
	peToken := req.PEToken
	if ceToken == 0 {
		ceToken = ceRow.CEToken
	}
	if peToken == 0 {
		peToken = peRow.PEToken
	}
	if ceToken == 0 || peToken == 0 {
		return nil, fmt.Errorf("CE/PE tokens not available")
	}

	// Lot size resolution
	lotSize := req.LotSize
	if lotSize == 0 && chain.LotSize > 0 {
		lotSize = chain.LotSize
	}
	if lotSize == 0 {
		if ls, lerr := s.LotSize.GetLotSize(ctx, req.Symbol, chain.Expiry); lerr == nil && ls > 0 {
			lotSize = ls
		}
	}
	if lotSize == 0 {
		lotSize = GetFallbackLotSize(req.Symbol)
	}
	if lotSize <= 0 {
		return nil, fmt.Errorf("invalid lot size for symbol %s", req.Symbol)
	}

	// Cross-check against known current NSE lot sizes (revised periodically by exchange circular).
	knownGoodLotSize := map[string]int{
		"NIFTY": 130, "BANKNIFTY": 30, "FINNIFTY": 60, "MIDCPNIFTY": 120, "NIFTYNXT50": 25,
	}
	if expected, ok := knownGoodLotSize[req.Symbol]; ok && lotSize != expected {
		log.Printf("🚨 LOT SIZE MISMATCH | Symbol=%s resolved=%d expected=%d (req=%d chain=%d) — check /mnt/shared CSV freshness",
			req.Symbol, lotSize, expected, req.LotSize, chain.LotSize)
	}
	log.Printf("📏 LOT SIZE RESOLVED | Symbol: %s | Expiry: %s | LotSize: %d | Source: req=%d, chain=%d, fallback=%d",
		req.Symbol, chain.Expiry, lotSize, req.LotSize, chain.LotSize, GetFallbackLotSize(req.Symbol))
	if req.Lots <= 0 {
		return nil, fmt.Errorf("invalid lots: %d", req.Lots)
	}

	ceLots := req.Lots
	peLots := req.Lots

	if req.DeltaNeutral && ceRow != nil && peRow != nil {
		ceDelta := math.Abs(ceRow.CEDelta)
		peDelta := math.Abs(peRow.PEDelta)
		totalWeight := ceDelta + peDelta

		if totalWeight > 1e-6 {
			// 1. Total pool = 2 * (Target Lots * Lot Size)
			baseLegContracts := float64(req.Lots * lotSize)
			totalPool := baseLegContracts * 2.0

			// 2. Cross-weight allocation
			// To neutralize a larger CE Delta, we must short fewer CEs and more PEs
			ceContractsRaw := totalPool * (peDelta / totalWeight)
			peContractsRaw := totalPool * (ceDelta / totalWeight)

			// 3. Convert to discrete lots and round
			calculatedCELots := int(math.Round(ceContractsRaw / float64(lotSize)))
			calculatedPELots := int(math.Round(peContractsRaw / float64(lotSize)))

			if calculatedCELots > 0 {
				ceLots = calculatedCELots
			}
			if calculatedPELots > 0 {
				peLots = calculatedPELots
			}

			log.Printf("⚖️ DELTA SIZING | Base: %d lots | CE Δ: %.4f | PE Δ: %.4f | CE Lots: %d | PE Lots: %d",
				req.Lots, ceRow.CEDelta, peRow.PEDelta, ceLots, peLots)
		}
	}

	netDelta := 0.0
	log.Printf("🔍 Computing netDelta (ceRow nil=%v, peRow nil=%v)", ceRow == nil, peRow == nil)
	if ceRow != nil && peRow != nil {
		netDelta = (float64(ceLots*lotSize) * -ceRow.CEDelta) + (float64(peLots*lotSize) * -peRow.PEDelta)
	}
	ceQty := ceLots * lotSize
	peQty := peLots * lotSize
	if ceQty <= 0 || peQty <= 0 {
		return nil, fmt.Errorf("computed invalid quantities ce=%d pe=%d", ceQty, peQty)
	}

	now := time.Now()
	tradeUID := BuildTradeUID(req.UserID, req.BrokerName, req.AccountID, req.Symbol, chain.Expiry, selectedStrike, now)

	trade := StoredTrade{
		TradeUID:        tradeUID,
		UserID:          req.UserID,
		BrokerName:      req.BrokerName,
		AccountID:       req.AccountID,
		Symbol:          req.Symbol,
		Expiry:          chain.Expiry,
		Strike:          selectedStrike,
		ProductType:     req.ProductType,
		ExchangeSegment: exchangeSegment,
		CEToken:         ceToken,
		PEToken:         peToken,
		CEQty:           ceQty,
		PEQty:           peQty,
		CELtp:           ceRow.CELtp,
		PELtp:           peRow.PELtp,
		NetDelta:        netDelta,
		Status:          "BUILDING",
		Mode:            req.BrokerName,
		Underlying:      chooseUnderlying(*chain),
		LotSize:         lotSize,
		Lots:            req.Lots,
		CreatedAt:       now,
		LastUpdateTime:  now,
		Config: MonitorConfig{
			BuyBuffer:           2.0,
			SellBuffer:          2.0,
			SLPointsPerLot:      0,
			HedgeThresholdDelta: 0,
			StraddleStopPrice:   0,
			OrderLotsPerCall:    req.OrderLotsPerCall,
			PollIntervalSec:     1,
			StraddleDiv:         4.0,  // Default: ATM Straddle / 4
			HedgeDiv:            57.0, // Default: Spot * IV / 57
		},
	}
	s.Store.SaveTrade(trade)

	// Initialize trade legs in trade_legs for proper PnL tracking
	if pgStore, ok := s.Store.(*PostgresBackedStore); ok {
		ceContractID, errCE := pgStore.ResolveContractIDByToken(ceToken, "NSEFO")
		peContractID, errPE := pgStore.ResolveContractIDByToken(peToken, "NSEFO")

		if errCE == nil && errPE == nil {
			if err := pgStore.InitStraddleLegs(
				trade.TradeUID,
				ceContractID,
				peContractID,
				int64(ceQty),
				int64(peQty),
			); err != nil {
				log.Printf("⚠️ InitStraddleLegs failed for %s: %v", trade.TradeUID, err)
			}
		}
	}

	// Build legs
	legs := []LegData{}
	if ceLots > 0 {
		legs = append(legs, LegData{
			Token:           ceToken,
			Symbol:          req.Symbol,
			OptionType:      "CE",
			Action:          "SELL",
			TotalLots:       ceLots,
			LotSize:         lotSize,
			ExpectedPrice:   ceRow.CELtp,
			ExchangeSegment: exchangeSegment,
		})
	}
	if peLots > 0 {
		legs = append(legs, LegData{
			Token:           peToken,
			Symbol:          req.Symbol,
			OptionType:      "PE",
			Action:          "SELL",
			TotalLots:       peLots,
			LotSize:         lotSize,
			ExpectedPrice:   peRow.PELtp,
			ExchangeSegment: exchangeSegment,
		})
	}

	// Chunk orders (updated version that returns error)
	log.Printf("📦 Building legs for chunking: CE lots=%d, PE lots=%d", ceLots, peLots)
	log.Printf("📦 Legs slice has %d items", len(legs))
	log.Printf("📦 Building legs for chunking: CE lots=%d, PE lots=%d", ceLots, peLots)
	log.Printf("📦 Legs slice has %d items", len(legs))
	log.Printf("📦 Building legs for chunking: CE lots=%d, PE lots=%d", ceLots, peLots)
	var chunks [][]ExecOrder

	if req.OrderLotsPerCall > 0 {
		// Explicit UI clip size: use dedicated pipeline, no seven-bucket chunker.
		clips, e := GenerateExplicitClips(
			fmt.Sprintf("BUI_%s", tradeUID),
			legs,
			req.OrderLotsPerCall,
			MaxOrderQtyForSymbol(req.Symbol),
		)
		chunks = clips
		err = e
	} else {
		// Default automatic chunking behavior.
		chunks, err = GenerateChunkedOrders(
			fmt.Sprintf("BUI_%s", tradeUID),
			legs,
			req.Lots,
			MaxOrderQtyForSymbol(req.Symbol),
			req.OrderLotsPerCall,
		)
	}
	if err != nil {
		log.Printf("❌ GenerateChunkedOrders failed for %s: %v", tradeUID, err)
		return nil, err
	}

	// Execute build. Stage 4A currently exposes the typed outcome for
	// later activation gating while preserving existing behavior.
	buildOutcome, err := s.executeBuild(ctx, executor, trade, chunks)
	if err != nil {
		trade.Status = "FAILED"
		trade.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(trade)
		return nil, err
	}

	log.Printf(
		"BUILD outcome trade=%s submitted=%d submission_errors=%d verified_ce=%d/%d verified_pe=%d/%d fully_verified=%t",
		trade.TradeUID,
		buildOutcome.SubmittedCount,
		buildOutcome.SubmissionErrors,
		buildOutcome.Summary.VerifiedCE,
		buildOutcome.Summary.RequestedCE,
		buildOutcome.Summary.VerifiedPE,
		buildOutcome.Summary.RequestedPE,
		buildOutcome.FullyVerified(),
	)

	switch {
	case buildOutcome.FullyVerified():
		trade.Status = "ACTIVE"

	case buildOutcome.HasVerifiedExposure():
		trade.Status = "PARTIAL"

	case buildOutcome.HasSubmittedOrders() ||
		buildOutcome.SubmissionErrors > 0 ||
		buildOutcome.FirstError != nil:
		trade.Status = "RECONCILIATION_REQUIRED"

	default:
		trade.Status = "FAILED"
	}

	trade.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(trade)

	if trade.Status == "ACTIVE" {

		s.startRuntime(trade)
	}

	return &DeployStraddleResponse{
		Success:    true,
		TradeUID:   tradeUID,
		Status:     trade.Status,
		Message:    fmt.Sprintf("Straddle deployment initiated via %s", req.BrokerName),
		Symbol:     req.Symbol,
		Expiry:     chain.Expiry,
		Strike:     selectedStrike,
		CEToken:    ceToken,
		PEToken:    peToken,
		CEQty:      ceQty,
		PEQty:      peQty,
		CELtp:      ceRow.CELtp,
		PELtp:      peRow.PELtp,
		NetDelta:   netDelta,
		LotSize:    lotSize,
		Lots:       req.Lots,
		DurationMS: time.Since(start).Milliseconds(),
		CreatedAt:  time.Now().Format(time.RFC3339),
	}, nil
}

func (s *Service) executeBuild(
	ctx context.Context,
	executor Executor,
	trade StoredTrade,
	chunks [][]ExecOrder,
) (BuildExecutionOutcome, error) {
	outcome := BuildExecutionOutcome{
		Summary: VerifiedExecutionSummary{
			RequestedCE: int64(trade.CEQty),
			RequestedPE: int64(trade.PEQty),
			UnfilledCE:  int64(trade.CEQty),
			UnfilledPE:  int64(trade.PEQty),
		},
	}
	sellBuffer := trade.Config.SellBuffer
	log.Printf("🚀 executeBuild starting for %s with %d chunks", trade.TradeUID, len(chunks))
	if sellBuffer <= 0 {
		sellBuffer = 2.0
	}

	// Collect successfully acknowledged broker order IDs for the
	// trade-level verified-fill reconciliation introduced in Stage 4.
	// This patch does not yet change existing execution behavior.
	submittedBrokerOrderIDs := make(map[string]struct{})

	for chunkIdx, chunk := range chunks {
		ordersToProcess := append([]ExecOrder(nil), chunk...)
		maxChunkRetries := 1

		for retryIter := 0; retryIter < maxChunkRetries && len(ordersToProcess) > 0; retryIter++ {
			if retryIter > 0 {
				time.Sleep(1 * time.Second)
			}

			var nextRetry []ExecOrder

			for i := range ordersToProcess {
				order := ordersToProcess[i]

				if order.Quantity <= 0 {
					err := fmt.Errorf(
						"invalid build quantity for token %d: %d",
						order.Token,
						order.Quantity,
					)
					outcome.SubmissionErrors++
					outcome.FirstError = err
					return outcome, err
				}

				bufferMultiplier := float64(retryIter + 1)
				limit := math.Max(0.05, order.ExpectedPrice-sellBuffer*bufferMultiplier)
				order.LimitPrice = math.Round(limit/0.05) * 0.05

				intent := OrderIntent{
					IntentID:        order.UID,
					TradeUID:        trade.TradeUID,
					Token:           order.Token,
					Symbol:          order.Symbol,
					Side:            order.Action,
					Quantity:        int64(order.Quantity),
					OrderType:       "LIMIT",
					ProductType:     trade.ProductType,
					ExchangeSegment: trade.ExchangeSegment,
					LegType:         order.OptionType,
					Phase:           "BUILD",
					OrderUID:        order.UID,
					BrokerName:      trade.BrokerName,
					AccountID:       trade.AccountID,
					ExpectedPrice:   order.ExpectedPrice,
				}
				lp := order.LimitPrice
				intent.LimitPrice = &lp

				brokerOrderID, status, submitErr := s.submitOrderIntent(
					ctx,
					executor,
					trade.TradeUID,
					intent,
				)
				if submitErr != nil {
					outcome.SubmissionErrors++

					if outcome.FirstError == nil {
						outcome.FirstError = submitErr
					}

					log.Printf(
						"BUILD submission unresolved trade=%s chunk=%d retry=%d leg=%s token=%d qty=%d err=%v",
						trade.TradeUID,
						chunkIdx+1,
						retryIter+1,
						order.OptionType,
						order.Token,
						order.Quantity,
						submitErr,
					)

					nextRetry = append(nextRetry, order)
					continue
				}

				if _, exists := submittedBrokerOrderIDs[brokerOrderID]; !exists {
					outcome.SubmittedCount++
				}

				submittedBrokerOrderIDs[brokerOrderID] = struct{}{}

				log.Printf(
					"BUILD submitted trade=%s chunk=%d retry=%d leg=%s broker_order_id=%s status=%s fill_assumed=false",
					trade.TradeUID,
					chunkIdx+1,
					retryIter+1,
					order.OptionType,
					brokerOrderID,
					status,
				)

				if status != "FILLED" &&
					status != "SUCCESS" &&
					status != "ACKED" &&
					status != "SUBMITTED" {
					nextRetry = append(nextRetry, order)
				}
			}

			ordersToProcess = nextRetry
		}

		if len(ordersToProcess) > 0 {
			err := fmt.Errorf(
				"build chunk %d has %d unresolved submission outcome(s)",
				chunkIdx+1,
				len(ordersToProcess),
			)

			if outcome.FirstError == nil {
				outcome.FirstError = err
			}

			log.Printf(
				"BUILD chunk unresolved trade=%s chunk=%d unresolved=%d automatic_retry=false",
				trade.TradeUID,
				chunkIdx+1,
				len(ordersToProcess),
			)
		}
	}

	log.Printf(
		"BUILD submission collection trade=%s broker_order_ids=%d stage4_verified_activation=false",
		trade.TradeUID,
		len(submittedBrokerOrderIDs),
	)

	if provider, ok := executor.(VerifiedFillsProvider); ok &&
		len(submittedBrokerOrderIDs) > 0 {

		summary, err := s.verifyAndPersistTradeFills(
			ctx,
			provider,
			trade.BrokerName,
			trade.AccountID,
			submittedBrokerOrderIDs,
			trade.CEToken,
			trade.PEToken,
			int64(trade.CEQty),
			int64(trade.PEQty),
			6,
			800*time.Millisecond,
		)

		outcome.Summary = summary

		if err != nil {
			if outcome.FirstError == nil {
				outcome.FirstError = err
			}
			log.Printf(
				"⚠ BUILD verification logging trade=%s err=%v",
				trade.TradeUID,
				err,
			)
		} else {
			log.Printf(
				"✅ BUILD verification summary trade=%s verified_ce=%d/%d verified_pe=%d/%d unfilled_ce=%d unfilled_pe=%d",
				trade.TradeUID,
				summary.VerifiedCE,
				summary.RequestedCE,
				summary.VerifiedPE,
				summary.RequestedPE,
				summary.UnfilledCE,
				summary.UnfilledPE,
			)
		}
	}

	return outcome, nil
}

// slThresholdForTrade computes the stop-loss lot count and rupee
// threshold for a trade. The lot count is deliberately fixed to the
// trade's ORIGINAL configured size (trade.Lots), not its current, live
// CEQty/PEQty -- matching the Python reference system's
// FIXED_ORIGINAL_POSITION_SL design, so a partial square-off, hedge, or
// roll can't drift the effective stop-loss tighter by shrinking the
// denominator. Falls back to live quantities only if Lots was never
// set (e.g. an older trade row predating this field).
func slThresholdForTrade(trade StoredTrade) (originalLotsOpen float64, threshold float64) {
	originalLotsOpen = float64(trade.Lots)
	if originalLotsOpen <= 0 {
		originalLotsOpen = float64(trade.CEQty+trade.PEQty) / (2.0 * float64(maxInt(1, trade.LotSize)))
	}
	threshold = -trade.Config.SLPointsPerLot * originalLotsOpen
	return originalLotsOpen, threshold
}

// executeAutoExit calls SquareOff for an autonomous risk trigger (SL or
// TP) and handles both outcomes consistently: on success (the trade's
// reloaded status matches wantStatus), it stops the trade's runtime,
// exactly like the old direct-status-flip code used to on every trigger
// regardless of whether an exit actually happened. On failure, it leaves
// the runtime running -- SquareOff always reverts the trade's status on
// failure (traced through every return path when this was built for
// SL), so it is not yet a terminal status, and runMonitorCycle's own
// terminal-status guard at the top of this function will therefore NOT
// skip the next tick: the exit attempt is retried on the next
// PollIntervalSec cycle instead of being lost, with no separate retry
// loop needed.
func (s *Service) executeAutoExit(tradeUID string, reason string, wantStatus string) {
	if err := s.SquareOff(tradeUID, reason); err != nil {
		log.Printf("[RISK] %s_TRIGGER SquareOff failed trade=%s err=%v", reason, tradeUID, err)
		return
	}
	if closedTrade, ok := s.Store.LoadTrade(tradeUID); ok && closedTrade.Status == wantStatus {
		if rt, ok := s.Store.LoadRuntime(tradeUID); ok {
			close(rt.StopCh)
			s.Store.DeleteRuntime(tradeUID)
		}
	}
}

// bpsOfSpotThreshold converts a basis-points-of-spot config value into a
// PnL-per-straddle points threshold: spot * bps / 10,000. Used by both
// the SL (SLPnLBpsOfSpot) and TP (TPPnLBpsOfSpot) bps-based triggers in
// runMonitorCycle, compared against pnlPerStraddle -- e.g. 1bps on a
// 24,400 spot is a 2.44-point threshold. Always returns a non-negative
// number; callers negate it for the SL (loss) side.
func bpsOfSpotThreshold(spot float64, bps float64) float64 {
	return spot * bps / 10000.0
}

func (s *Service) runMonitorCycle(tradeUID string) {
	trade, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return
	}
	switch trade.Status {
	case "CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MANUAL", "FAILED":
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	chain, err := s.Snapshot.GetOptionChain(ctx, trade.Symbol, trade.Expiry)
	if err != nil {
		log.Printf("⚠ snapshot fetch failed for %s: %v", tradeUID, err)
		return
	}

	var ceRow, peRow *OptionChainRow
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if row.CEToken == trade.CEToken {
			ceRow = row
		}
		if row.PEToken == trade.PEToken {
			peRow = row
		}
	}
	if ceRow == nil || peRow == nil {
		return
	}

	// Short CE: entry > current LTP is profit
	ceEntry := trade.CELtp
	cePNL := (ceEntry - ceRow.CELtp) * float64(trade.CEQty)

	// Short PE: entry > current LTP is profit
	peEntry := trade.PELtp
	pePNL := (peEntry - peRow.PELtp) * float64(trade.PEQty)
	totalPNL := cePNL + pePNL

	netDelta := ceRow.CEDelta*float64(trade.CEQty) + peRow.PEDelta*float64(trade.PEQty)
	netGamma := ceRow.CEGamma*float64(trade.CEQty) + peRow.PEGamma*float64(trade.PEQty)
	netTheta := ceRow.CETheta*float64(trade.CEQty) + peRow.PETheta*float64(trade.PEQty)
	netVega := ceRow.CEVega*float64(trade.CEQty) + peRow.PEVega*float64(trade.PEQty)

	// Calculate PointsOut and PointsAllowed
	atmStraddle := ceRow.CELtp + peRow.PELtp
	spot := chooseUnderlying(*chain)
	// IV from broker is in % (e.g., 10.36 = 10.36%), convert to decimal (0.1036)
	avgIVPercent := (ceRow.CEIV + peRow.PEIV) / 2.0
	avgIVDecimal := avgIVPercent / 100.0

	// Use config divisors (defaults: straddle_div=4, hedge_div=57)
	straddleDiv := trade.Config.StraddleDiv
	hedgeDiv := trade.Config.HedgeDiv
	if straddleDiv <= 0 {
		straddleDiv = 4.0
	}
	if hedgeDiv <= 0 {
		hedgeDiv = 57.0
	}

	// PointsOut = |Net Delta / Net Gamma| (but handle gamma near zero)
	pointsOut := 0.0
	if netGamma != 0 {
		pointsOut = math.Abs(netDelta / netGamma)
	}

	// PointsAllowed = min(ATM Straddle / straddle_div, Spot * IV_decimal / hedge_div)
	term1 := atmStraddle / straddleDiv
	term2 := spot * avgIVDecimal / hedgeDiv
	pointsAllowed := term1
	if term2 < term1 {
		pointsAllowed = term2
	}

	// PnL per original straddle unit: submitted lots × lot size.
	// Prefer the original configured lot count rather than live CE/PE
	// quantities, which can become unequal after hedges, rolls, or exits.
	straddleQuantity := int64(trade.Lots) * int64(trade.LotSize)
	if straddleQuantity <= 0 {
		straddleQuantity = int64(minInt(trade.CEQty, trade.PEQty))
	}

	pnlPerStraddle := 0.0
	if straddleQuantity > 0 {
		pnlPerStraddle = totalPNL / float64(straddleQuantity)
	}

	snapshot := TradeSnapshot{
		TradeUID:         trade.TradeUID,
		Timestamp:        time.Now(),
		Status:           trade.Status,
		Symbol:           trade.Symbol,
		Expiry:           trade.Expiry,
		Strike:           trade.Strike,
		Underlying:       chooseUnderlying(*chain),
		TotalPNL:         totalPNL,
		PnLPerStraddle:   pnlPerStraddle,
		StraddleQuantity: straddleQuantity,
		PointsOut:        pointsOut,
		PointsAllowed:    pointsAllowed,
		RealizedPNL: func() float64 {
			switch trade.Status {
			case "CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MANUAL", "FAILED":
				return totalPNL
			default:
				return 0
			}
		}(),
		UnrealizedPNL: func() float64 {
			switch trade.Status {
			case "CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MANUAL", "FAILED":
				return 0
			default:
				return totalPNL
			}
		}(),
		NetDelta: netDelta,
		NetGamma: netGamma,
		NetTheta: netTheta,
		NetVega:  netVega,
		LivePositions: []TradeLegSnapshot{
			{
				Token:      trade.CEToken,
				Strike:     ceRow.Strike,
				OptionType: "CE",
				Action:     "SELL",
				Quantity:   int64(trade.CEQty),
				EntryPrice: ceEntry,
				LTP:        ceRow.CELtp,
				PNL:        cePNL,
				IV:         ceRow.CEIV,
				Delta:      ceRow.CEDelta,
				Gamma:      ceRow.CEGamma,
				Theta:      ceRow.CETheta,
				Vega:       ceRow.CEVega,
			},
			{
				Token:      trade.PEToken,
				Strike:     peRow.Strike,
				OptionType: "PE",
				Action:     "SELL",
				Quantity:   int64(trade.PEQty),
				EntryPrice: peEntry,
				LTP:        peRow.PELtp,
				PNL:        pePNL,
				IV:         peRow.PEIV,
				Delta:      peRow.PEDelta,
				Gamma:      peRow.PEGamma,
				Theta:      peRow.PETheta,
				Vega:       peRow.PEVega,
			},
		},
	}

	s.Store.SaveSnapshot(snapshot)

	if s.Snapshot != nil {
		if pushErr := s.Snapshot.PushSnapshot(ctx, snapshot); pushErr != nil {
			log.Printf("[SNAPSHOT] push failed trade=%s err=%v", snapshot.TradeUID, pushErr)
		}
	}

	// Stop-loss check. This executes on every monitor cycle. Two
	// independent SL mechanisms can each trigger it: SLPointsPerLot
	// (fixed points-per-original-lot, see slThresholdForTrade) or
	// SLPnLBpsOfSpot (loss as a fraction of live synthetic spot,
	// compared against pnlPerStraddle -- e.g. 1bps on a 24,400 spot is a
	// 2.44-point-per-straddle threshold). Checked as one unified
	// condition, not two separate ifs, so a tick where both happen to be
	// configured and breached can't call SquareOff twice.
	slBreached, slThreshold, slSource := false, 0.0, ""
	if trade.Config.SLPointsPerLot > 0 {
		lots, threshold := slThresholdForTrade(trade)
		if totalPNL <= threshold {
			slBreached, slThreshold = true, threshold
			slSource = fmt.Sprintf("points_per_lot lots=%.2f", lots)
		}
	}
	if !slBreached && trade.Config.SLPnLBpsOfSpot > 0 {
		threshold := -bpsOfSpotThreshold(spot, trade.Config.SLPnLBpsOfSpot)
		if pnlPerStraddle <= threshold {
			slBreached, slThreshold = true, threshold
			slSource = fmt.Sprintf("bps_of_spot spot=%.2f bps=%.2f", spot, trade.Config.SLPnLBpsOfSpot)
		}
	}
	if slBreached {
		log.Printf(
			"[RISK] SL_TRIGGER trade=%s source=%s pnl=%.2f pnl_per_straddle=%.2f threshold=%.2f",
			tradeUID, slSource, totalPNL, pnlPerStraddle, slThreshold,
		)
		s.executeAutoExit(tradeUID, "SL", "CLOSED_SL")
	}

	// Take-profit check: symmetric to the SL bps mechanism above.
	// TPPnLBpsOfSpot > 0 enables it; TPPnLTarget (rupee-based) remains
	// alert-only in tickRuntime, unchanged.
	if trade.Config.TPPnLBpsOfSpot > 0 {
		tpThreshold := bpsOfSpotThreshold(spot, trade.Config.TPPnLBpsOfSpot)
		if pnlPerStraddle >= tpThreshold {
			log.Printf(
				"[RISK] TP_TRIGGER trade=%s pnl_per_straddle=%.2f threshold=%.2f spot=%.2f bps=%.2f",
				tradeUID, pnlPerStraddle, tpThreshold, spot, trade.Config.TPPnLBpsOfSpot,
			)
			s.executeAutoExit(tradeUID, "TP", "CLOSED_TP")
		}
	}

	// Time-based hard exit: unlike tickRuntime's separate SquareOffTime
	// check (still alert-only, [RISK][EXIT_TIME_ALERT] -- a different,
	// pre-existing config field), SquareOffHardTime actually exits via a
	// real, verified SquareOff once reached. Normal (non-aggressive)
	// chunking -- a scheduled close isn't an emergency.
	if !trade.Config.SquareOffHardTime.IsZero() && !time.Now().Before(trade.Config.SquareOffHardTime) {
		log.Printf(
			"[RISK] TIME_TRIGGER trade=%s now=%s target=%s",
			tradeUID, time.Now().Format(time.RFC3339), trade.Config.SquareOffHardTime.Format(time.RFC3339),
		)
		s.executeAutoExit(tradeUID, "TIME", "CLOSED_TIME")
	}

	// Minute-end hedge eligibility check
	// Evaluate hedge only at minute boundaries to avoid duplicate signals
	now := time.Now()
	currentMinute := now.Truncate(time.Minute)

	if rt, ok := s.Store.LoadRuntime(tradeUID); ok {
		if rt.LastMinuteCheck.IsZero() || !rt.LastMinuteCheck.Equal(currentMinute) {
			rt.LastMinuteCheck = currentMinute

			lotSize := int64(trade.LotSize)
			if lotSize <= 0 {
				lotSize = 1
			}

			hedgeAction := "OK"
			hedgeReason := ""

			minimumHedgePoints := 19.5
			forceOneLotTest := trade.Config.ForceOneLotHedgeTest

			if forceOneLotTest {
				minimumHedgePoints = trade.Config.HedgePointsFloor
			}

			pointsCrossed := pointsOut > pointsAllowed ||
				trade.Config.ForceHedgeRegardlessOfPoints
			minimumPointsReached := pointsOut >= minimumHedgePoints
			deltaHasOneLot := math.Abs(netDelta) >= float64(lotSize)

			shouldHedge := pointsCrossed &&
				minimumPointsReached &&
				(deltaHasOneLot || forceOneLotTest)

			if shouldHedge {
				hedgeReason = fmt.Sprintf(
					"points_out=%.2f points_allowed=%.2f net_delta=%.4f lot_size=%d force_one_lot_test=%t",
					pointsOut,
					pointsAllowed,
					netDelta,
					lotSize,
					forceOneLotTest,
				)

				if forceOneLotTest && trade.Config.HedgeTestExecuted {
					hedgeAction = "HEDGE_TEST_SKIPPED_ALREADY_EXECUTED"
					hedgeReason += " reason=already_executed"
				} else {
					hedgeAction = "HEDGE_TRIGGERED"

					log.Printf(
						"[MONITOR][%s] invoking trade-scoped hedge action=%s %s",
						tradeUID,
						hedgeAction,
						hedgeReason,
					)

					if err := s.ManualHedge(context.Background(), tradeUID); err != nil {
						hedgeAction = "HEDGE_FAILED"
						hedgeReason += " error=" + err.Error()
					} else if forceOneLotTest {
						trade.Config.HedgeTestExecuted = true
						trade.LastUpdateTime = time.Now()
						s.Store.UpdateTrade(trade)
						hedgeReason += " test_hedge_marked_executed=true"
					}
				}
			}

			log.Printf(
				"[MONITOR][%s] minute=%s action=%s points_out=%.4f points_allowed=%.4f min_points=%.4f net_delta=%.4f abs_delta=%.4f lot_size=%d points_crossed=%t min_points_reached=%t delta_has_one_lot=%t force_one_lot_test=%t hedge_test_executed=%t reason=%s",
				tradeUID,
				currentMinute.Format("15:04"),
				hedgeAction,
				pointsOut,
				pointsAllowed,
				minimumHedgePoints,
				netDelta,
				math.Abs(netDelta),
				lotSize,
				pointsCrossed,
				minimumPointsReached,
				deltaHasOneLot,
				forceOneLotTest,
				trade.Config.HedgeTestExecuted,
				hedgeReason,
			)
		}
	}

}

// persistSQFProgress durably checkpoints a square-off's progress mid-flight:
// persists any verified fills so far (idempotent -- fills.fill_id/order_id
// is UNIQUE with ON CONFLICT DO NOTHING, so calling this repeatedly with
// the full accumulated fill list is safe, not a duplicate-insert risk) and
// updates the trade's remaining CE/PE quantity. Call this after every
// chunk, not just once at the end -- see SquareOff's call site comment for
// why that mattered in practice, not just in theory.
func (s *Service) persistSQFProgress(tradeUID string, tr StoredTrade, verifiedFills []BrokerFill, remainingCE, remainingPE int64, inProgressStatus string) {
	if len(verifiedFills) > 0 {
		if persister, ok := s.Store.(VerifiedFillPersistence); ok {
			if _, err := persister.PersistVerifiedFills(context.Background(), tr.BrokerName, tr.AccountID, verifiedFills); err != nil {
				log.Printf("⚠️ SQF verified-fill checkpoint persistence failed for %s: %v", tradeUID, err)
			}
		}
	}

	tr.CEQty = int(remainingCE)
	tr.PEQty = int(remainingPE)
	tr.LastUpdateTime = time.Now()
	tr.Status = inProgressStatus
	s.Store.UpdateTrade(tr)
}

func (s *Service) SquareOff(tradeUID string, reason string) error {
	defer s.lockTrade(tradeUID)()

	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return fmt.Errorf("trade not found")
	}

	switch tr.Status {
	case "CLOSEDSQF", "CLOSED", "CLOSED_SQF", "CLOSED_MANUAL":
		return fmt.Errorf("square-off not allowed for status %s", tr.Status)
	case "SQUARING_OFF":
		return fmt.Errorf("square-off already in progress for %s", tradeUID)
	}

	if tr.CEToken <= 0 && tr.PEToken <= 0 {
		return fmt.Errorf("cannot square off %s: missing CE/PE tokens", tradeUID)
	}
	if tr.CEQty <= 0 && tr.PEQty <= 0 {
		return fmt.Errorf("cannot square off %s: no open CE/PE quantity found", tradeUID)
	}

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return err
	}

	provider, ok := executor.(VerifiedFillsProvider)
	if !ok {
		return fmt.Errorf("square-off requires VerifiedFillsProvider for %s", tradeUID)
	}

	prevStatus := tr.Status
	targetCE := int64(tr.CEQty)
	targetPE := int64(tr.PEQty)
	lotSize := int64(tr.LotSize)

	if lotSize <= 0 {
		return fmt.Errorf("invalid lot size %d", tr.LotSize)
	}
	if targetCE > 0 && targetCE%lotSize != 0 {
		return fmt.Errorf("CE quantity %d is not a lot-size multiple of %d", targetCE, lotSize)
	}
	if targetPE > 0 && targetPE%lotSize != 0 {
		return fmt.Errorf("PE quantity %d is not a lot-size multiple of %d", targetPE, lotSize)
	}

	tr.Status = "SQUARING_OFF"
	tr.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(tr)

	revert := func(cause error) error {
		tr.Status = prevStatus
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		return cause
	}

	maxOrderQty := 1755
	baseLots := int(targetCE / lotSize)
	if peLots := int(targetPE / lotSize); peLots > baseLots {
		baseLots = peLots
	}

	remainingCE := targetCE
	remainingPE := targetPE
	var allVerifiedFills []BrokerFill

	const maxExecutionAttempts = 4

	for executionAttempt := 1; executionAttempt <= maxExecutionAttempts; executionAttempt++ {
		if remainingCE <= 0 && remainingPE <= 0 {
			break
		}

		ceLots := int(remainingCE / lotSize)
		peLots := int(remainingPE / lotSize)

		legs := make([]LegData, 0, 2)
		if ceLots > 0 {
			legs = append(legs, LegData{
				Token:           tr.CEToken,
				Symbol:          tr.Symbol,
				OptionType:      "CE",
				Action:          "BUY",
				TotalLots:       ceLots,
				LotSize:         int(lotSize),
				ExpectedPrice:   0.0,
				ExchangeSegment: tr.ExchangeSegment,
			})
		}
		if peLots > 0 {
			legs = append(legs, LegData{
				Token:           tr.PEToken,
				Symbol:          tr.Symbol,
				OptionType:      "PE",
				Action:          "BUY",
				TotalLots:       peLots,
				LotSize:         int(lotSize),
				ExpectedPrice:   0.0,
				ExchangeSegment: tr.ExchangeSegment,
			})
		}

		// Use aggressive chunking for SL-triggered square-off (max lots/order for fastest exit)
		var chunks [][]ExecOrder
		var err error
		if reason == "SL" {
			chunks, err = GenerateAggressiveChunkedOrders(
				fmt.Sprintf("S%sA%d", tradeUID, executionAttempt),
				legs,
				baseLots,
				maxOrderQty,
			)
		} else {
			chunks, err = GenerateChunkedOrders(
				fmt.Sprintf("S%sA%d", tradeUID, executionAttempt),
				legs,
				baseLots,
				maxOrderQty,
				0,
			)
		}
		if err != nil {
			return revert(err)
		}

		if len(chunks) == 0 {
			break
		}

		progressBefore := remainingCE + remainingPE

		for chunkIndex, chunk := range chunks {
			submitted := make(map[string]struct{})
			requestedCE := int64(0)
			requestedPE := int64(0)

			for orderIndex, order := range chunk {
				intent := OrderIntent{
					IntentID: BuildShortOrderUID(
						tr.Symbol,
						fmt.Sprintf("SQF%dC%dO%d", executionAttempt, chunkIndex, orderIndex),
						time.Now(),
						executionAttempt+orderIndex,
					),
					TradeUID:        tradeUID,
					Token:           order.Token,
					Symbol:          order.Symbol,
					ExchangeSegment: tr.ExchangeSegment,
					Side:            order.Action,
					Quantity:        int64(order.Quantity),
					OrderType:       "MARKET",
					ProductType:     tr.ProductType,
					LegType:         order.OptionType,
					Phase:           "SQF",
					OrderUID:        order.UID,
					BrokerName:      tr.BrokerName,
					AccountID:       tr.AccountID,
					ExpectedPrice:   order.ExpectedPrice,
				}

				// Persist order to DB before execution (required for fill tracking)
				log.Printf("📝 Persisting SQF order to DB before execution: trade=%s intent_id=%s side=%s qty=%d", tradeUID, intent.IntentID, intent.Side, intent.Quantity)
				s.Store.AppendIntent(tradeUID, intent)

				res, execErr := executor.ExecuteOrderIntent(context.Background(), intent)
				if execErr != nil {
					log.Printf(
						"❌ SQF placement failed trade=%s leg=%s qty=%d: %v",
						tradeUID,
						order.OptionType,
						order.Quantity,
						execErr,
					)
					continue
				}
				if res == nil || res.BrokerOrderID == "" {
					log.Printf("❌ SQF placement returned no broker order ID for %s", tradeUID)
					continue
				}

				submitted[res.BrokerOrderID] = struct{}{}
				if order.OptionType == "CE" {
					requestedCE += int64(order.Quantity)
				} else if order.OptionType == "PE" {
					requestedPE += int64(order.Quantity)
				}

				if updater, ok := s.Store.(interface {
					MarkOrderSubmitted(string, string, string, string)
				}); ok {
					updater.MarkOrderSubmitted(
						intent.IntentID,
						res.BrokerOrderID,
						res.Status,
						res.RawResponse,
					)
				}
			}

			if len(submitted) == 0 {
				continue
			}

			verifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			summary := waitForVerifiedFills(
				verifyCtx,
				provider,
				submitted,
				tr.CEToken,
				tr.PEToken,
				requestedCE,
				requestedPE,
				6,
				800*time.Millisecond,
			)
			cancel()

			if len(summary.Fills) > 0 {
				log.Printf("🔍 DEBUG SQF summary.Fills=%d allVerifiedFills=%d before append", len(summary.Fills), len(allVerifiedFills))
				allVerifiedFills = append(allVerifiedFills, summary.Fills...)
			}
			log.Printf("🔍 DEBUG SQF allVerifiedFills=%d after append", len(allVerifiedFills))

			remainingCE = targetCE - verifiedQuantityForToken(allVerifiedFills, tr.CEToken)
			remainingPE = targetPE - verifiedQuantityForToken(allVerifiedFills, tr.PEToken)

			if remainingCE < 0 {
				remainingCE = 0
			}
			if remainingPE < 0 {
				remainingPE = 0
			}

			log.Printf(
				"📊 SQF reconciliation trade=%s attempt=%d chunk=%d verifiedCE=%d/%d verifiedPE=%d/%d remainingCE=%d remainingPE=%d",
				tradeUID,
				executionAttempt,
				chunkIndex+1,
				targetCE-remainingCE,
				targetCE,
				targetPE-remainingPE,
				targetPE,
				remainingCE,
				remainingPE,
			)

			// Persist this chunk's verified fills and checkpoint the
			// trade's remaining quantity IMMEDIATELY, not after the whole
			// multi-chunk/multi-attempt loop finishes. Previously this
			// only happened once at the very end, so a process
			// interruption mid-square-off (confirmed to happen live: a
			// process restart) lost every already-broker-confirmed
			// chunk's progress -- the orders had genuinely executed and
			// moved the real position, but the DB never found out, so a
			// later retry would have started from the stale original
			// quantity instead of the true remainder.
			s.persistSQFProgress(tradeUID, tr, allVerifiedFills, remainingCE, remainingPE, "SQUARING_OFF")

			if remainingCE == 0 && remainingPE == 0 {
				break
			}
		}

		progressAfter := remainingCE + remainingPE
		if progressAfter >= progressBefore {
			log.Printf(
				"⚠️ SQF made no verified progress on attempt %d for %s",
				executionAttempt,
				tradeUID,
			)
			break
		}
	}

	verifiedCE := targetCE - remainingCE
	verifiedPE := targetPE - remainingPE

	tr.CEQty = int(remainingCE)
	tr.PEQty = int(remainingPE)
	tr.LastUpdateTime = time.Now()

	if remainingCE == 0 && remainingPE == 0 {
		switch reason {
		case "SL":
			// Preserves the audit distinction between a real
			// stop-loss-triggered close and a manual/other square-off --
			// CLOSED_SL is already a recognized terminal status
			// elsewhere in this file (see the status-guard switches).
			tr.Status = "CLOSED_SL"
		case "TP":
			tr.Status = "CLOSED_TP"
		case "TIME":
			tr.Status = "CLOSED_TIME"
		default:
			tr.Status = "CLOSEDSQF"
		}
		tr.ClosedAt = time.Now()
	} else {
		tr.Status = prevStatus
	}

	s.Store.UpdateTrade(tr)

	if realizedPnL, ok := s.computeVerifiedRealizedPnL(context.Background(), tr); ok {
		tr.RealizedPnL = realizedPnL
		s.Store.UpdateTrade(tr)
	}

	// The actual success condition is remainingCE/PE == 0 (that's what
	// decided the status above) -- checked directly here, not re-derived
	// from tr.Status, so adding a new terminal status (e.g. CLOSED_SL)
	// for a still-fully-verified exit can never be misread as a failure.
	if remainingCE != 0 || remainingPE != 0 {
		log.Printf(
			"⚠️ Partial square-off %s: verified CE=%d/%d PE=%d/%d",
			tradeUID,
			verifiedCE,
			targetCE,
			verifiedPE,
			targetPE,
		)
		return fmt.Errorf(
			"partial square-off: remaining CE=%d PE=%d",
			remainingCE,
			remainingPE,
		)
	}

	log.Printf("✅ Square-off completed for %s reason=%s status=%s", tradeUID, reason, tr.Status)
	return nil
}

func verifiedQuantityForToken(fills []BrokerFill, token int64) int64 {
	var total int64
	for _, fill := range fills {
		if !fill.Verified || fill.Token != token || fill.FilledQty <= 0 {
			continue
		}
		total += fill.FilledQty
	}
	return total
}

func (s *Service) computeVerifiedRealizedPnL(ctx context.Context, tr StoredTrade) (float64, bool) {
	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		log.Printf("⚠️ computeVerifiedRealizedPnL: no executor for %s: %v", tr.TradeUID, err)
		return 0, false
	}

	provider, ok := executor.(VerifiedFillsProvider)
	if !ok {
		log.Printf("⚠️ computeVerifiedRealizedPnL: executor for %s does not support VerifiedFillsProvider", tr.TradeUID)
		return 0, false
	}

	allFills, err := provider.GetVerifiedFills(ctx)
	if err != nil {
		log.Printf("⚠️ computeVerifiedRealizedPnL: fetch failed for %s: %v", tr.TradeUID, err)
		return 0, false
	}

	// Filter fills by StrategyKey (short UID, last 10 chars) + token.
	// This correctly groups fills by individual trade, even when multiple trades
	// exist on the same strike.
	shortUID := tr.TradeUID
	if len(shortUID) > 10 {
		shortUID = shortUID[len(shortUID)-10:]
	}

	strategyFilteredFills := make([]BrokerFill, 0, len(allFills))
	for _, f := range allFills {
		if strings.EqualFold(f.StrategyKey, shortUID) &&
			(f.Token == tr.CEToken || f.Token == tr.PEToken) {
			strategyFilteredFills = append(strategyFilteredFills, f)
		}
	}

	if len(strategyFilteredFills) == 0 {
		log.Printf("⚠️ computeVerifiedRealizedPnL: no fills found matching strategy %q for %s", tr.TradeUID, tr.TradeUID)
		return 0, false
	}

	timeFilteredFills := strategyFilteredFills

	strikeStr := strconv.FormatFloat(float64(tr.Strike), 'f', -1, 64)

	ceFills := filterFillsForLeg(timeFilteredFills, tr.CEToken, strikeStr, "CE")
	peFills := filterFillsForLeg(timeFilteredFills, tr.PEToken, strikeStr, "PE")

	ceLeg := AggregateLegFromFills(ceFills, tr.CEToken)
	peLeg := AggregateLegFromFills(peFills, tr.PEToken)

	total := ceLeg.RealizedPnL + peLeg.RealizedPnL

	log.Printf(
		"✅ Broker-verified realized P&L for %s: CE=%.2f (fills=%d) PE=%.2f (fills=%d) total=%.2f",
		tr.TradeUID, ceLeg.RealizedPnL, len(ceFills), peLeg.RealizedPnL, len(peFills), total,
	)

	return total, true
}
func (s *Service) PartialSquareOff(tradeUID string, percentage float64) error {
	defer s.lockTrade(tradeUID)()

	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return fmt.Errorf("trade not found")
	}
	if percentage <= 0 || percentage > 100 {
		return fmt.Errorf("invalid percentage")
	}

	switch tr.Status {
	case "CLOSEDSQF", "CLOSED", "CLOSED_SQF", "CLOSED_MANUAL":
		return fmt.Errorf("partial square-off not allowed for status %s", tr.Status)
	case "SQUARING_OFF", "PARTIAL-SQF":
		return fmt.Errorf("an exit is already in progress for %s", tradeUID)
	}

	if tr.CEToken <= 0 && tr.PEToken <= 0 {
		return fmt.Errorf("cannot partially square off %s: missing CE/PE tokens", tradeUID)
	}
	if tr.CEQty <= 0 && tr.PEQty <= 0 {
		return fmt.Errorf("cannot partially square off %s: no open CE/PE quantity found", tradeUID)
	}

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return err
	}

	// Unlike SquareOff, this previously had NO verification step at all --
	// it trusted the synchronous order-placement response's status field
	// (treating even a bare "SUBMITTED" ack as good enough) and then
	// unconditionally subtracted the REQUESTED quantity from the trade's
	// remaining CE/PE, regardless of whether anything actually filled.
	// Bringing this up to the same standard as SquareOff.
	provider, ok := executor.(VerifiedFillsProvider)
	if !ok {
		return fmt.Errorf("partial square-off requires VerifiedFillsProvider for %s", tradeUID)
	}

	prevStatus := tr.Status
	tr.Status = "PARTIAL-SQF"
	tr.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(tr)

	// Calculate partial quantities aligned with LotSize multiples
	lotSize := int64(65)
	if tr.LotSize > 0 {
		lotSize = int64(tr.LotSize)
	}

	rawCeQty := float64(tr.CEQty) * (percentage / 100.0)
	rawPeQty := float64(tr.PEQty) * (percentage / 100.0)

	ceLots := int64(math.Round(rawCeQty / float64(lotSize)))
	if ceLots == 0 && rawCeQty > 0 {
		ceLots = 1
	}
	ceQty := ceLots * lotSize

	peLots := int64(math.Round(rawPeQty / float64(lotSize)))
	if peLots == 0 && rawPeQty > 0 {
		peLots = 1
	}
	peQty := peLots * lotSize

	if ceQty <= 0 && peQty <= 0 {
		tr.Status = prevStatus
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		return fmt.Errorf("percentage too low to generate at least 1 contract for trade %s", tradeUID)
	}

	// Build legs for chunked execution
	var legs []LegData
	if ceQty > 0 && tr.CEToken > 0 {
		legs = append(legs, LegData{
			Token:           tr.CEToken,
			Symbol:          tr.Symbol,
			OptionType:      "CE",
			Action:          "BUY",
			TotalLots:       int(ceLots),
			LotSize:         int(lotSize),
			ExpectedPrice:   0,
			ExchangeSegment: tr.ExchangeSegment,
		})
	}
	if peQty > 0 && tr.PEToken > 0 {
		legs = append(legs, LegData{
			Token:           tr.PEToken,
			Symbol:          tr.Symbol,
			OptionType:      "PE",
			Action:          "BUY",
			TotalLots:       int(peLots),
			LotSize:         int(lotSize),
			ExpectedPrice:   0,
			ExchangeSegment: tr.ExchangeSegment,
		})
	}

	if len(legs) == 0 {
		tr.Status = prevStatus
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		return fmt.Errorf("no valid legs for partial square-off")
	}

	// Generate seven chunks
	maxOrderQty := 1800 // Broker max
	chunks, err := GenerateChunkedOrders(
		fmt.Sprintf("PSQF_%s", tradeUID),
		legs,
		int(ceLots+peLots),
		maxOrderQty,
		0,
	)
	if err != nil {
		tr.Status = prevStatus
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		return fmt.Errorf("chunk generation failed: %w", err)
	}

	// Execute chunks, verifying and durably checkpointing progress after
	// each one -- an interrupted partial exit must never lose track of
	// chunks that already executed on the broker, for the same reason
	// documented on SquareOff/persistSQFProgress above.
	remainingCE := ceQty
	remainingPE := peQty
	var allVerifiedFills []BrokerFill

	for chunkIdx, chunk := range chunks {
		submitted := make(map[string]struct{})
		requestedCE := int64(0)
		requestedPE := int64(0)

		for _, order := range chunk {
			if order.Quantity <= 0 {
				continue
			}

			intent := OrderIntent{
				IntentID:        order.UID,
				TradeUID:        tradeUID,
				Token:           order.Token,
				Symbol:          order.Symbol,
				Side:            order.Action,
				Quantity:        int64(order.Quantity),
				OrderType:       "MARKET",
				ProductType:     tr.ProductType,
				ExchangeSegment: tr.ExchangeSegment,
				LegType:         order.OptionType,
				Phase:           "PSQF",
				OrderUID:        order.UID,
				BrokerName:      tr.BrokerName,
				AccountID:       tr.AccountID,
			}

			s.Store.AppendIntent(tradeUID, intent)

			res, execErr := executor.ExecuteOrderIntent(context.Background(), intent)
			if execErr != nil {
				log.Printf("❌ PSQF chunk=%d leg=%s qty=%d err=%v", chunkIdx+1, order.OptionType, order.Quantity, execErr)
				continue
			}
			if res == nil || res.BrokerOrderID == "" {
				log.Printf("❌ PSQF chunk=%d leg=%s: placement returned no broker order ID", chunkIdx+1, order.OptionType)
				continue
			}

			submitted[res.BrokerOrderID] = struct{}{}
			if order.OptionType == "CE" {
				requestedCE += int64(order.Quantity)
			} else if order.OptionType == "PE" {
				requestedPE += int64(order.Quantity)
			}

			if updater, ok := s.Store.(interface {
				MarkOrderSubmitted(intentID string, brokerOrderID string, status string, rawResponse string)
			}); ok {
				updater.MarkOrderSubmitted(intent.IntentID, res.BrokerOrderID, res.Status, res.RawResponse)
			}
		}

		if len(submitted) == 0 {
			continue
		}

		verifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		summary := waitForVerifiedFills(
			verifyCtx, provider, submitted,
			tr.CEToken, tr.PEToken, requestedCE, requestedPE,
			6, 800*time.Millisecond,
		)
		cancel()

		if len(summary.Fills) > 0 {
			allVerifiedFills = append(allVerifiedFills, summary.Fills...)
		}

		remainingCE = ceQty - verifiedQuantityForToken(allVerifiedFills, tr.CEToken)
		remainingPE = peQty - verifiedQuantityForToken(allVerifiedFills, tr.PEToken)
		if remainingCE < 0 {
			remainingCE = 0
		}
		if remainingPE < 0 {
			remainingPE = 0
		}

		log.Printf(
			"📊 PSQF reconciliation trade=%s chunk=%d verifiedCE=%d/%d verifiedPE=%d/%d remainingCE=%d remainingPE=%d",
			tradeUID, chunkIdx+1, ceQty-remainingCE, ceQty, peQty-remainingPE, peQty, remainingCE, remainingPE,
		)

		// Reuses SquareOff's checkpoint helper: persists verified fills so
		// far (idempotent) and durably records the trade's new overall
		// remaining CE/PE (its full open quantity minus what this partial
		// exit has verified so far), not just this partial exit's own
		// target -- persistSQFProgress writes the trade's total remaining
		// position, so pass tr.CEQty/PEQty reduced by verified progress.
		s.persistSQFProgress(
			tradeUID, tr, allVerifiedFills,
			int64(tr.CEQty)-(ceQty-remainingCE),
			int64(tr.PEQty)-(peQty-remainingPE),
			"PARTIAL-SQF",
		)
	}

	verifiedCE := ceQty - remainingCE
	verifiedPE := peQty - remainingPE

	tr.CEQty = int(math.Max(0, float64(tr.CEQty)-float64(verifiedCE)))
	tr.PEQty = int(math.Max(0, float64(tr.PEQty)-float64(verifiedPE)))
	tr.LastUpdateTime = time.Now()

	if tr.CEQty == 0 && tr.PEQty == 0 {
		tr.Status = "CLOSEDSQF"
		tr.ClosedAt = time.Now()
	} else {
		tr.Status = prevStatus
	}
	s.Store.UpdateTrade(tr)

	if remainingCE > 0 || remainingPE > 0 {
		log.Printf(
			"⚠️ Partial square-off %s incomplete: verified CE=%d/%d PE=%d/%d",
			tradeUID, verifiedCE, ceQty, verifiedPE, peQty,
		)
		return fmt.Errorf("partial square-off incomplete: verified CE=%d/%d PE=%d/%d", verifiedCE, ceQty, verifiedPE, peQty)
	}

	log.Printf("✅ Partial Square Off (%v%%) completed for Trade %s (CE: %d, PE: %d)", percentage, tradeUID, verifiedCE, verifiedPE)
	return nil
}

func (s *Service) ManualHedge(ctx context.Context, tradeUID string) error {
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return fmt.Errorf("trade not found")
	}

	snap, ok := s.Store.LoadSnapshot(tradeUID)
	if !ok {
		return fmt.Errorf("snapshot not found")
	}
	if math.Abs(snap.NetDelta) < 1 {
		return nil
	}

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return err
	}

	chain, err := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
	if err != nil {
		return err
	}

	atmRow, err := FindATMRow(*chain)
	if err != nil {
		return err
	}

	qty := tr.LotSize
	if qty <= 0 {
		qty = GetFallbackLotSize(tr.Symbol)
	}
	if qty <= 0 {
		return fmt.Errorf("invalid hedge quantity for symbol %s", tr.Symbol)
	}

	// Determine hedge direction
	var ceSide, peSide string
	if snap.NetDelta < 0 {
		ceSide = "BUY"
		peSide = "SELL"
	} else {
		ceSide = "SELL"
		peSide = "BUY"
	}

	// Build legs for chunked execution
	legs := []LegData{
		{
			Token:           atmRow.CEToken,
			Symbol:          strings.TrimSpace(tr.Symbol),
			OptionType:      "CE",
			Action:          ceSide,
			TotalLots:       1,
			LotSize:         qty,
			ExpectedPrice:   0,
			ExchangeSegment: tr.ExchangeSegment,
		},
		{
			Token:           atmRow.PEToken,
			Symbol:          strings.TrimSpace(tr.Symbol),
			OptionType:      "PE",
			Action:          peSide,
			TotalLots:       1,
			LotSize:         qty,
			ExpectedPrice:   0,
			ExchangeSegment: tr.ExchangeSegment,
		},
	}

	// Generate seven chunks
	maxOrderQty := 1800
	chunks, err := GenerateChunkedOrders(
		fmt.Sprintf("HEDGE_%s", tradeUID),
		legs,
		1,
		maxOrderQty,
		0,
	)
	if err != nil {
		return fmt.Errorf("hedge chunk generation failed: %w", err)
	}

	// Execute chunks with verification
	for chunkIdx, chunk := range chunks {
		ordersToProcess := append([]ExecOrder(nil), chunk...)
		maxChunkRetries := 3

		for retryIter := 0; retryIter < maxChunkRetries && len(ordersToProcess) > 0; retryIter++ {
			if retryIter > 0 {
				time.Sleep(1 * time.Second)
			}

			var nextRetry []ExecOrder
			for i := range ordersToProcess {
				order := ordersToProcess[i]
				if order.Quantity <= 0 {
					continue
				}

				intent := OrderIntent{
					IntentID:        order.UID,
					TradeUID:        tradeUID,
					Token:           order.Token,
					Symbol:          order.Symbol,
					Side:            order.Action,
					Quantity:        int64(order.Quantity),
					OrderType:       "LIMIT",
					ProductType:     tr.ProductType,
					ExchangeSegment: tr.ExchangeSegment,
					LegType:         order.OptionType,
					Phase:           "HEDGE",
					OrderUID:        order.UID,
					BrokerName:      tr.BrokerName,
					AccountID:       tr.AccountID,
				}

				s.Store.AppendIntent(tradeUID, intent)

				res, err := executor.ExecuteOrderIntent(ctx, intent)
				if err != nil {
					log.Printf("❌ HEDGE chunk=%d retry=%d leg=%s err=%v", chunkIdx+1, retryIter+1, order.OptionType, err)
					nextRetry = append(nextRetry, order)
					continue
				}

				if updater, ok := s.Store.(interface {
					MarkOrderSubmitted(intentID string, brokerOrderID string, status string, rawResponse string)
				}); ok {
					updater.MarkOrderSubmitted(intent.IntentID, res.BrokerOrderID, res.Status, res.RawResponse)
				}

				if res.Status != "FILLED" && res.Status != "SUCCESS" && res.Status != "ACKED" && res.Status != "SUBMITTED" {
					nextRetry = append(nextRetry, order)
				}
			}

			ordersToProcess = nextRetry
		}
	}

	log.Printf("✅ Manual Hedge completed for Trade %s", tradeUID)
	return nil
}

func chooseUnderlying(chain OptionChainSnapshot) float64 {
	if chain.SyntheticSpot != 0 {
		return chain.SyntheticSpot
	}
	if chain.SyntheticFuture != 0 {
		return chain.SyntheticFuture
	}
	if chain.FutureLtp != 0 {
		return chain.FutureLtp
	}
	return 0
}
