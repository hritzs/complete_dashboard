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

func (s *MemoryStore) DeleteTrade(tradeUID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.trades, tradeUID)
	delete(s.intents, tradeUID)
	delete(s.snapshots, tradeUID)
	delete(s.runtimes, tradeUID)
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
	OrderEvents     *OrderEventRegistry

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

	// buildTiming paces build verification and the leftover chase; the zero
	// value means production defaults.
	buildTiming buildTiming

	// freezeQtyBySymbol is the live broker freeze quantity per underlying
	// (normalized symbol), loaded ONCE at startup by PreloadFreezeQty. The
	// order path only reads it -- it never calls the broker, so a slow or
	// failing quote API can't delay an order.
	freezeQtyMu       sync.RWMutex
	freezeQtyBySymbol map[string]int64
}

func NewService(store Store, clientID string) *Service {
	return &Service{
		Store:             store,
		DefaultClientID:   clientID,
		BrokerFactory:     NewDefaultBrokerFactory(),
		Snapshot:          NewSnapshotClient(),
		LotSize:           NewLotSizeClient(),
		OrderEvents:       NewOrderEventRegistry(),
		freezeQtyBySymbol: make(map[string]int64),
	}
}

// resolveMaxOrderQty is the largest single-order quantity for symbol: the
// live, lot-aligned broker value loaded by PreloadFreezeQty (e.g. NIFTY
// freezQty 1801 -> 27 x 65 = 1755). No hardcoded per-symbol table: if the
// live value isn't loaded for this symbol it returns ONE LOT -- always
// under any freeze limit, so an order (above all an exit) is never
// rejected or blocked by a guessed number; it just goes in more pieces.
// Never calls the broker, so it adds no latency to an order.
func (s *Service) resolveMaxOrderQty(symbol string, lotSize int64) int64 {
	s.freezeQtyMu.RLock()
	qty, ok := s.freezeQtyBySymbol[NormalizeSymbol(symbol)]
	s.freezeQtyMu.RUnlock()
	if ok && qty > 0 {
		return qty
	}
	if lotSize <= 0 {
		lotSize = 1
	}
	log.Printf("⚠️ FREEZE QTY %s not loaded -- sizing orders at 1 lot (%d) each", NormalizeSymbol(symbol), lotSize)
	return lotSize
}

// PreloadFreezeQty fetches the live broker freeze quantity once per
// underlying (it's set per underlying, not per strike, so any live option
// token of that symbol works -- the nearest-expiry ATM CE is used) and
// stores it for resolveMaxOrderQty. Called at startup, after the broker
// login, so the order path never waits on it. Symbols that fail are
// retried in the background every minute until they load; until then
// orders for them go one lot at a time.
func (s *Service) PreloadFreezeQty(ctx context.Context, executor Executor, symbols []string) {
	provider, ok := executor.(FreezeQtyProvider)
	if !ok {
		log.Printf("[BOOT] FREEZE QTY preload skipped: executor has no freeze-qty lookup; orders go 1 lot at a time")
		return
	}
	if s.Snapshot == nil {
		log.Printf("[BOOT] FREEZE QTY preload skipped: no snapshot client; orders go 1 lot at a time")
		return
	}

	pending := s.loadFreezeQty(ctx, provider, symbols)
	if len(pending) == 0 {
		return
	}
	go func() {
		for len(pending) > 0 {
			time.Sleep(time.Minute)
			retryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			pending = s.loadFreezeQty(retryCtx, provider, pending)
			cancel()
		}
	}()
}

// loadFreezeQty loads each symbol's live freeze qty and returns the ones
// that failed.
func (s *Service) loadFreezeQty(ctx context.Context, provider FreezeQtyProvider, symbols []string) []string {
	var failed []string
	for _, symbol := range symbols {
		sym := NormalizeSymbol(symbol)

		chain, err := s.Snapshot.GetOptionChain(ctx, sym, "")
		if err != nil {
			log.Printf("[BOOT] ⚠️ FREEZE QTY %s: option chain unavailable: %v -- 1 lot per order until loaded (retrying)", sym, err)
			failed = append(failed, sym)
			continue
		}
		atm, err := FindATMRow(*chain)
		if err != nil || atm.CEToken <= 0 {
			log.Printf("[BOOT] ⚠️ FREEZE QTY %s: no ATM token in chain (err=%v) -- 1 lot per order until loaded (retrying)", sym, err)
			failed = append(failed, sym)
			continue
		}

		info, err := provider.GetFreezeQty(ctx, atm.CEToken)
		if err != nil || info.MaxOrderQty <= 0 {
			if freezeWarnDue(sym) { // retried every minute; logged every 15 min
				log.Printf("[BOOT] ⚠️ FREEZE QTY %s: live fetch failed token=%d err=%v -- 1 lot per order until loaded (retrying every minute; next note in 15 min)", sym, atm.CEToken, err)
			}
			failed = append(failed, sym)
			continue
		}

		s.freezeQtyMu.Lock()
		s.freezeQtyBySymbol[sym] = info.MaxOrderQty
		s.freezeQtyMu.Unlock()
		log.Printf(
			"[BOOT] 📏 FREEZE QTY %-10s broker freezQty=%d  lot=%d  -> max per order %d (%d lots)  [live contract, token=%d]",
			sym, info.FreezeQty, info.LotSize, info.MaxOrderQty, info.MaxOrderQty/info.LotSize, atm.CEToken,
		)
	}
	return failed
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

	// Cross-check against known current NSE/BSE lot sizes (revised
	// periodically by exchange circular). Verified live against
	// GreekSoft's own getAllContract scrip master (2026-09-23, via
	// libs/broker-greeksoft/cmd/lotsizeprobe). NIFTY was wrong here (130,
	// should be 65) -- this cross-check had been silently firing on every
	// NIFTY build (logging a MISMATCH) without anyone noticing, since it's
	// only a log line, not an error.
	knownGoodLotSize := map[string]int{
		"NIFTY": 65, "BANKNIFTY": 30, "FINNIFTY": 60, "MIDCPNIFTY": 120,
		"SENSEX": 20, "BANKEX": 30, "NIFTYNXT50": 25,
	}
	if expected, ok := knownGoodLotSize[req.Symbol]; ok && lotSize != expected {
		log.Printf("🚨 LOT SIZE MISMATCH | Symbol=%s resolved=%d expected=%d (req=%d chain=%d) — check /mnt/shared CSV freshness",
			req.Symbol, lotSize, expected, req.LotSize, chain.LotSize)
	}
	log.Printf("📏 LOT SIZE RESOLVED | Symbol: %s | Expiry: %s | LotSize: %d | Source: req=%d, chain=%d",
		req.Symbol, chain.Expiry, lotSize, req.LotSize, chain.LotSize)
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
	// The UID has second resolution: two builds in the same second (e.g. two
	// scheduled jobs firing together) got the SAME trade and the second
	// overwrote the first while both positions filled. Every build gets its
	// own trade.
	tradeUID = s.reserveTradeUID(tradeUID)
	defer releaseTradeUID(tradeUID)

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
	// A caller of this endpoint directly (the Testing tab's manual deploy,
	// which never goes through ConfigBuild) can still arm SL/TP/exit time
	// via the raw fields below, since req.Risk itself is json:"-" and can
	// only be set by ConfigBuild's own internal call.
	risk := req.Risk
	if risk == nil && (strings.TrimSpace(req.ExitTime) != "" || req.SlBps > 0 || req.TpBps > 0 || req.WingPct > 0) {
		risk = &BuildRiskConfig{ExitTime: req.ExitTime, SlBps: req.SlBps, TpBps: req.TpBps, WingPct: req.WingPct}
	}
	// Abort before anything is persisted or sent if the requested risk config
	// is invalid.
	if err := risk.Validate(now); err != nil {
		return nil, err
	}
	if err := applyBuildRiskConfig(&trade.Config, risk, now); err != nil {
		return nil, err
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

	maxOrderQty := int(s.resolveMaxOrderQty(req.Symbol, int64(lotSize)))

	if req.OrderLotsPerCall > 0 {
		// Explicit UI clip size: use dedicated pipeline, no seven-bucket chunker.
		clips, e := GenerateExplicitClips(
			fmt.Sprintf("BUI_%s", tradeUID),
			legs,
			req.OrderLotsPerCall,
			maxOrderQty,
		)
		chunks = clips
		err = e
	} else {
		// Default automatic chunking behavior.
		chunks, err = GenerateChunkedOrders(
			fmt.Sprintf("BUI_%s", tradeUID),
			legs,
			req.Lots,
			maxOrderQty,
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
		if strings.Contains(err.Error(), "build ratio mismatch") {
			// Lopsided fill (e.g. PE hit margin rejection after CE filled):
			// still a real position -- track it as ACTIVE (partial) at what
			// really filled, one leg or both. Only if the fills can't be
			// validated does it stay RECONCILIATION_REQUIRED for a human.
			if stored, ok := s.Store.LoadTrade(tradeUID); ok {
				stored.Config.PartialFill = true
				stored.Config.RequestedCEQty = ceQty
				stored.Config.RequestedPEQty = peQty
				if promoted, ok := s.PromotePartialTrade(ctx, stored); ok {
					s.startRuntime(promoted)
					log.Printf("[PARTIAL] trade=%s lopsided build now tracked: %v", tradeUID, err)
				}
			}
			return nil, err
		}
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
		// Part of the build filled (e.g. the rest hit a margin rejection).
		// What filled is a real position: track it as ACTIVE (partial) at
		// the quantity that REALLY filled -- monitors, hedge and exits all
		// run on it -- instead of parking it in an unmonitored PARTIAL
		// status (live 2026-09-30: a 73/72-lot position sat unmonitored).
		trade.Config.PartialFill = true
		trade.Config.RequestedCEQty = trade.CEQty
		trade.Config.RequestedPEQty = trade.PEQty
		trade.CEQty = int(buildOutcome.Summary.VerifiedCE)
		trade.PEQty = int(buildOutcome.Summary.VerifiedPE)
		promoted, ok := s.PromotePartialTrade(ctx, trade)
		trade = promoted
		if !ok {
			trade.Status = "RECONCILIATION_REQUIRED"
		}

	case buildOutcome.HasSubmittedOrders() ||
		buildOutcome.SubmissionErrors > 0 ||
		buildOutcome.FirstError != nil:
		// Verification saw no fills (e.g. it timed out), but orders went
		// out: if the exchange fills are in the DB, track them as ACTIVE
		// (partial); otherwise leave it for a human.
		trade.Config.PartialFill = true
		trade.Config.RequestedCEQty = trade.CEQty
		trade.Config.RequestedPEQty = trade.PEQty
		promoted, ok := s.PromotePartialTrade(ctx, trade)
		trade = promoted
		if !ok {
			trade.Status = "RECONCILIATION_REQUIRED"
			trade.Config.PartialFill = false
		}

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

	totalBuildOrders := 0
	for _, chunk := range chunks {
		totalBuildOrders += len(chunk)
	}

	// Live "Building X/Y orders placed" progress, pushed over the same
	// websocket channel the runtime monitor already uses for PnL/greeks
	// (straddle_update) so the UI reflects real-time progress during a
	// build instead of only learning the outcome once it's done. Best
	// effort: a push failure never blocks or fails the build itself.
	pushBuildProgress := func(submitted int) {
		if s.Snapshot == nil {
			return
		}
		_ = s.Snapshot.PushSnapshot(context.Background(), TradeSnapshot{
			TradeUID:             trade.TradeUID,
			Timestamp:            time.Now(),
			Status:               "BUILDING",
			Symbol:               trade.Symbol,
			Expiry:               trade.Expiry,
			Strike:               trade.Strike,
			Underlying:           trade.Underlying,
			NetDelta:             trade.NetDelta,
			BuildOrdersSubmitted: submitted,
			BuildOrdersTotal:     totalBuildOrders,
		})
	}
	pushBuildProgress(0)

	// Collect successfully acknowledged broker order IDs for the
	// trade-level verified-fill reconciliation introduced in Stage 4.
	// This patch does not yet change existing execution behavior.
	submittedBrokerOrderIDs := make(map[string]struct{})
	var builtOrders []chaseOrder

	// The circuit breaker checks real-time confirmation for the PREVIOUS
	// order (one submission behind), never the one just sent -- submission
	// never pauses to wait on any single order's confirmation. By the time
	// the next order is submitted, the previous one's real Iris/NATS event
	// has almost always already arrived (live confirm latency is ~10-30ms;
	// see logs/4_execution.log IRIS-WS lines), so this is a free, real-time
	// check, not a blocking one.
	// Wings (MonitorConfig.WingPct > 0): a wing BUY goes out right after
	// each short, same quantity, and is confirmed/trued up once the build's
	// fills are verified (settleBuildWings).
	wingPlan := s.planBuildWings(ctx, trade)
	var pendingWings []pendingWing
	wingsSettled := false
	settleWings := func(verifiedCE, verifiedPE int64, trueUp bool) {
		if wingsSettled {
			return
		}
		wingsSettled = true
		s.settleBuildWings(ctx, executor, trade, wingPlan, pendingWings, verifiedCE, verifiedPE, trueUp)
	}
	defer settleWings(0, 0, false) // any exit path not settled below

	// "Stop building" (POST /api/build/stop): no new build order goes out
	// once set; what already filled becomes the (partial) position.
	stopFlag := registerBuildStop(trade.TradeUID)
	defer buildStops.Delete(trade.TradeUID)

	var pendingConfirmOrderID string
	consecutiveRejections := 0
	circuitBreakerTripped := false

	checkPreviousOrder := func() {
		if pendingConfirmOrderID == "" {
			return
		}
		upd, ok := s.OrderEvents.Latest(trade.TradeUID, pendingConfirmOrderID)
		if !ok {
			// Not confirmed yet -- genuinely unknown, don't guess either way.
			return
		}
		status := strings.ToUpper(strings.TrimSpace(upd.Status))
		if status == "REJECTED" || status == "CANCELLED" || status == "CANCELED" {
			consecutiveRejections++
			log.Printf(
				"BUILD confirmed trade=%s broker_order_id=%s status=%s reason=%q consecutive_rejections=%d",
				trade.TradeUID, pendingConfirmOrderID, status, upd.ReasonText, consecutiveRejections,
			)
		} else if status == "FILLED" || status == "ACKED" || status == "PARTIAL_FILL" || status == "PARTIALLY_FILLED" {
			consecutiveRejections = 0
		}
		pendingConfirmOrderID = ""
		if consecutiveRejections >= buildCircuitBreakerRejectionThreshold {
			err := fmt.Errorf("build aborted after %d consecutive order rejections", consecutiveRejections)
			if outcome.FirstError == nil {
				outcome.FirstError = err
			}
			log.Printf("[BUILD-CIRCUIT-BREAKER] trade=%s %s", trade.TradeUID, err.Error())
			circuitBreakerTripped = true
		}
	}

chunkLoop:
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

				if stopFlag.Load() {
					log.Printf("[BUILD] trade=%s STOPPED by user: no more build orders (%d submitted); the filled part becomes the position", trade.TradeUID, outcome.SubmittedCount)
					break chunkLoop
				}

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

				// Price off the LIVE bid/ask (attempt 1 -> buffer 1) when a
				// quote is available; the stale LTP snapshotted at deploy
				// time (ExpectedPrice) is only a fallback for when the
				// chain has no live quote yet.
				limit := 0.0
				if s.Snapshot != nil {
					if chain, chainErr := s.Snapshot.GetOptionChain(ctx, trade.Symbol, trade.Expiry); chainErr == nil {
						bid, ask := bidAskForToken(chain, order.Token)
						if p, ok := bidAskLimitPrice(order.Action, bid, ask, bufferForAttempt(1)); ok {
							limit = p
						}
					}
				}
				if limit <= 0 {
					bufferMultiplier := float64(retryIter + 1)
					limit = math.Max(0.05, order.ExpectedPrice-sellBuffer*bufferMultiplier)
				}
				order.LimitPrice = math.Round(limit/0.05) * 0.05

				intent := OrderIntent{
					IntentID:        order.UID,
					TradeUID:        trade.TradeUID,
					Token:           order.Token,
					Symbol:          order.Symbol,
					Side:            order.Action,
					Quantity:        int64(order.Quantity),
					LotSize:         int64(trade.LotSize),
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
				builtOrders = append(builtOrders, chaseOrder{
					BrokerOrderID: brokerOrderID,
					Token:         order.Token,
					Leg:           order.OptionType,
					Side:          order.Action,
					Quantity:      int64(order.Quantity),
				})
				pushBuildProgress(outcome.SubmittedCount)

				if tok := wingPlan.token[order.OptionType]; tok > 0 && strings.EqualFold(order.Action, "SELL") {
					if pw, wErr := s.submitWingOrder(ctx, executor, trade, tok, order.OptionType, "BUY", int64(order.Quantity)); wErr != nil {
						log.Printf("[WINGS] ⚠ trade=%s build wing BUY %s %d not sent (true-up retries after the build): %v", trade.TradeUID, order.OptionType, order.Quantity, wErr)
					} else {
						pendingWings = append(pendingWings, pw)
					}
				}

				log.Printf(
					"BUILD submitted trade=%s chunk=%d retry=%d leg=%s broker_order_id=%s status=%s fill_assumed=false",
					trade.TradeUID,
					chunkIdx+1,
					retryIter+1,
					order.OptionType,
					brokerOrderID,
					status,
				)

				// Check the PREVIOUS order's real terminal status (a
				// non-blocking cache lookup) before submitting further --
				// never this one, and never a wait. See checkPreviousOrder's
				// comment above for why this is free, real-time information
				// by the time we get here.
				checkPreviousOrder()
				pendingConfirmOrderID = brokerOrderID

				if status != "FILLED" &&
					status != "SUCCESS" &&
					status != "ACKED" &&
					status != "SUBMITTED" {
					nextRetry = append(nextRetry, order)
				}

				if circuitBreakerTripped {
					break
				}
			}

			ordersToProcess = nextRetry
			if circuitBreakerTripped {
				break
			}
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

		if circuitBreakerTripped {
			break chunkLoop
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
			s.buildTiming.withDefaults().verifyAttempts,
			s.buildTiming.withDefaults().verifyDelay,
		)

		outcome.Summary = summary

		// Work the leftover quantity (re-price, then cancel if it will not
		// fill) instead of stopping at PARTIAL with an order resting.
		if summary.VerificationError == nil && !summary.Complete() && s.Snapshot != nil {
			outcome.Summary = s.chaseUnfilledBuild(
				ctx, executor, provider, trade, builtOrders,
				submittedBrokerOrderIDs, summary, s.buildTiming,
			)
			summary = outcome.Summary
		}

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
			settleWings(summary.VerifiedCE, summary.VerifiedPE, summary.VerificationError == nil)
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
			if (summary.VerifiedCE > 0 || summary.VerifiedPE > 0) && !buildLegRatioSatisfied(summary.RequestedCE, summary.RequestedPE, summary.VerifiedCE, summary.VerifiedPE) {
				err := fmt.Errorf("build ratio mismatch: requested CE=%d PE=%d, verified CE=%d PE=%d; leaving trade visible as RECONCILIATION_REQUIRED for manual square-off", summary.RequestedCE, summary.RequestedPE, summary.VerifiedCE, summary.VerifiedPE)
				outcome.FirstError = err
				outcome.Summary.VerificationError = err
				// A lopsided build (e.g. CE filled, PE rejected outright) is
				// real money at risk: some quantity genuinely filled at the
				// broker. This used to delete the trade record entirely,
				// which erased the only evidence that position existed --
				// found live 2026-09-22 (a 40-lot NIFTY 23350 CE build hit
				// RMS margin rejection after ~24 lots, PE never filled, and
				// the naked CE sat open and untracked for over two hours).
				// Record what actually filled and keep the trade visible
				// instead, the same way HasVerifiedExposure() already does
				// a few lines below in DeployStraddle for a plain partial.
				trade.CEQty = int(summary.VerifiedCE)
				trade.PEQty = int(summary.VerifiedPE)
				trade.Status = "RECONCILIATION_REQUIRED"
				trade.LastUpdateTime = time.Now()
				s.Store.UpdateTrade(trade)
				log.Printf("[BUILD-SYMMETRY] %s", err.Error())
				return outcome, err
			}
		}
	}

	return outcome, nil
}

// PromotePartialTrade turns a partially-filled build into an ACTIVE
// (partial) trade sized to what REALLY filled, per trade (never merged with
// another trade on the same strike): legs are re-derived from exchange fills,
// the trade is marked ACTIVE with Config.PartialFill, then validated (both
// legs genuinely short). Quantities, entry prices and Lots come from the
// validated fills. Returns ok=false (trade left for a human, not monitored)
// if the fills don't form a valid two-legged short position.
func (s *Service) PromotePartialTrade(ctx context.Context, trade StoredTrade) (StoredTrade, bool) {
	pg, isPG := s.Store.(*PostgresBackedStore)
	if !isPG {
		return trade, false
	}
	if !trade.Config.PartialFill {
		trade.Config.PartialFill = true
		if trade.Config.RequestedCEQty == 0 && trade.Config.RequestedPEQty == 0 && trade.Lots > 0 && trade.LotSize > 0 {
			trade.Config.RequestedCEQty = trade.CEQty
			trade.Config.RequestedPEQty = trade.PEQty
		}
	}
	if err := pg.RecomputeTradeLegs(ctx, trade.TradeUID); err != nil {
		log.Printf("[PARTIAL] trade=%s leg recompute failed, NOT tracked automatically: %v", trade.TradeUID, err)
		return trade, false
	}

	prevStatus := trade.Status
	trade.Status = "ACTIVE"
	trade.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(trade)

	elig, err := pg.ValidatePartialTradeForRuntime(ctx, trade.TradeUID)
	if err != nil {
		log.Printf("[PARTIAL] trade=%s filled legs are not a valid short position (%v) -- left as RECONCILIATION_REQUIRED for manual review (was %s)",
			trade.TradeUID, err, prevStatus)
		trade.Status = "RECONCILIATION_REQUIRED"
		trade.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(trade)
		return trade, false
	}

	trade.CEQty = int(elig.CEQty)
	trade.PEQty = int(elig.PEQty)
	trade.CELtp = elig.CEEntry
	trade.PELtp = elig.PEEntry
	// SL/TP normalise PnL per original straddle (Lots x LotSize); for a
	// partial build the real "original" size is what filled. The smaller
	// leg keeps it conservative (SL fires no later than it should).
	if trade.LotSize > 0 {
		// Both legs: the smaller one. One leg only: that leg.
		filledQty := minInt(trade.CEQty, trade.PEQty)
		if filledQty == 0 {
			filledQty = maxInt(trade.CEQty, trade.PEQty)
		}
		filledLots := filledQty / trade.LotSize
		if filledLots > 0 && (trade.Lots <= 0 || filledLots < trade.Lots) {
			trade.Lots = filledLots
		}
	}
	trade.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(trade)
	log.Printf("[PARTIAL] trade=%s tracked as ACTIVE (partial): ce=%d pe=%d (requested ce=%d pe=%d) entry ce=%.2f pe=%.2f lots=%d -- monitors/hedge/exits run on the filled quantity",
		trade.TradeUID, trade.CEQty, trade.PEQty, trade.Config.RequestedCEQty, trade.Config.RequestedPEQty, trade.CELtp, trade.PELtp, trade.Lots)
	return trade, true
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
	// In the background, one exit per trade at a time: the monitor keeps
	// ticking and pushing the trade's snapshot (positions, PnL, delta) to
	// the UI while lots close (combined_sqf.go: runExitAsync).
	s.runExitAsync(tradeUID, reason, func() { s.executeAutoExitNow(tradeUID, reason, wantStatus) })
}

func (s *Service) executeAutoExitNow(tradeUID string, reason string, wantStatus string) {
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
// minuteCloseWait: how long after hh:mm:00 the minute-end check waits so
// the chain it reads carries at least one decoder publish (fixed 100 ms
// schedule) from after the boundary.
const minuteCloseWait = 120 * time.Millisecond

// minuteCloseChain returns the chain priced at the minute's candle closes
// when this tick runs the minute-end checks (nil otherwise). It waits until
// minuteCloseWait past the boundary (re-reading the chain), then takes every
// leg's last trade before the boundary (lutCloseChain, no grace wait: at
// that point the last trade so far IS the close) -- decision ~120-150 ms
// after the minute, inside the same second. Strikes without close data
// (beyond the two nearest expiries) keep their LTP.
func (s *Service) minuteCloseChain(ctx context.Context, tradeUID string, trade StoredTrade, chain *OptionChainSnapshot) *OptionChainSnapshot {
	rt, ok := s.Store.LoadRuntime(tradeUID)
	if !ok || chain == nil {
		return nil
	}
	now := time.Now()
	boundary := now.Truncate(time.Minute)
	if (!rt.LastMinuteCheck.IsZero() && rt.LastMinuteCheck.Equal(boundary)) || now.Sub(boundary) > 2*time.Second {
		return nil // not the minute-end tick (or a check postponed by an exit: live prices)
	}
	if wait := minuteCloseWait - now.Sub(boundary); wait > 0 {
		time.Sleep(wait)
		if c, err := s.Snapshot.GetOptionChain(ctx, trade.Symbol, trade.Expiry); err == nil && c != nil {
			chain = c
		}
	}
	closed, _ := lutCloseChain(chain, boundary, time.Now())
	if closed == nil || closed == chain {
		return nil // feed carries no trade times: LTPs as they are
	}
	if closed.SyntheticFuture > 0 {
		closed.SyntheticSpot = closed.SyntheticFuture
	}
	// GreekSoft's candle closes (built from every trade) for the trade's
	// own legs and the ATM pair (synthetic future); shared with the LUT and
	// other trades, ~0.3-0.5 s after the minute; feed closes if not in by
	// gsCloseDeadline.
	toks := atmTokens(closed)
	toks[trade.CEToken], toks[trade.PEToken] = true, true
	if gsClosesOn() { // every other leg the trade holds in this expiry (hedges, wings)
		if extra, xerr := s.extraOpenLegs(tradeUID, trade); xerr == nil {
			for _, l := range extra {
				if strings.EqualFold(strings.TrimSpace(l.Expiry), "") || strings.EqualFold(strings.TrimSpace(l.Expiry), strings.TrimSpace(trade.Expiry)) {
					toks[l.Token] = true
				}
			}
		}
	}
	nGS, feedPx := s.gsApplyCloses(closed, boundary, toks)
	mc := minuteCloseOf(closed, chain, boundary)
	mc.Source = "feed"
	if nGS > 0 {
		mc.Source = "GreekSoft"
		if r := gsRowAt(closed, closed.ATM); r != nil { // the final ATM (it may have moved)
			mc.FeedCE, mc.FeedPE = feedPx[r.CEToken], feedPx[r.PEToken]
		}
	}
	legs := ""
	for _, r := range closed.Chain {
		if r.CEToken == trade.CEToken {
			legs += fmt.Sprintf(" | own CE %.0f close %.2f", r.Strike, r.CELtp)
		}
		if r.PEToken == trade.PEToken {
			legs += fmt.Sprintf(" | own PE %.0f close %.2f", r.Strike, r.PELtp)
		}
	}
	log.Printf("[MONITOR][%s] minute-end on candle closes (%d leg(s) from GreekSoft): %s%s", tradeUID, nGS, mc, legs)
	return closed
}

func bpsOfSpotThreshold(spot float64, bps float64) float64 {
	return spot * bps / 10000.0
}

func (s *Service) runMonitorCycle(tradeUID string) {
	trade, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return
	}
	if isTerminalTradeStatus(trade.Status) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	chain, err := s.Snapshot.GetOptionChain(ctx, trade.Symbol, trade.Expiry)
	if err != nil {
		log.Printf("⚠ snapshot fetch failed for %s: %v", tradeUID, err)
		return
	}
	// The minute-end tick (hedge / SL / TP / TIME below) decides on the
	// exact 1-minute candle closes -- each leg's last trade before hh:mm:00
	// by exchange trade time, and the synthetic future from the ATM closes
	// -- not on whatever LTP the latest snapshot happened to carry.
	var minuteB time.Time // set on the minute-end tick: every leg is priced at its minute close
	if minuteChain := s.minuteCloseChain(ctx, tradeUID, trade, chain); minuteChain != nil {
		chain, minuteB = minuteChain, time.Now().Truncate(time.Minute)
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

	netDelta, netGamma, netTheta, netVega := shortLegGreeks(ceRow, peRow, trade.CEQty, trade.PEQty)

	// Fold in any additional leg (e.g. a hedge placed at a different,
	// live-ATM strike -- see ManualHedgeLots) so its delta/greeks/PnL are
	// part of the SAME totals used for PointsOut, SL/TP, and everything
	// the UI shows -- a hedge is part of the real position, not a
	// separate thing tracked nowhere. Confirmed live 2026-09-25: before
	// this, a hedge leg's real delta/PnL contribution was invisible here
	// entirely, so PointsOut/SL/TP kept reacting as if the hedge did
	// nothing.
	var extraLegSnapshots []TradeLegSnapshot
	// Net signed CE/PE across ALL strikes (straddle + hedges + wings);
	// both 0 when wings exactly cover the shorts.
	wingNetCE, wingNetPE := -int64(trade.CEQty), -int64(trade.PEQty)
	wingPNL := 0.0
	if extraLegs, err := s.extraOpenLegs(tradeUID, trade); err != nil {
		log.Printf("⚠️ extraOpenLegs failed for %s (continuing without them): %v", tradeUID, err)
	} else {
		// A leg in another expiry (manual leg across expiry) is valued off
		// ITS expiry's chain, fetched once per cycle.
		chains := map[string]*OptionChainSnapshot{strings.ToUpper(chain.Expiry): chain}
		chainFor := func(expiry string) *OptionChainSnapshot {
			key := strings.ToUpper(strings.TrimSpace(expiry))
			if key == "" {
				return chain
			}
			if c, ok := chains[key]; ok {
				return c
			}
			cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
			c, cerr := s.Snapshot.GetOptionChain(cctx, trade.Symbol, key)
			ccancel()
			if cerr != nil || c == nil {
				chains[key] = nil
				return nil
			}
			// Minute-end tick: a leg in another expiry is priced at ITS
			// minute close too (GreekSoft's when on, else the feed's).
			if !minuteB.IsZero() {
				if cc, _ := lutCloseChain(c, minuteB, time.Now()); cc != nil {
					toks := map[int64]bool{}
					for _, l := range extraLegs {
						if strings.EqualFold(strings.TrimSpace(l.Expiry), key) {
							toks[l.Token] = true
						}
					}
					if gsClosesOn() {
						s.gsApplyCloses(cc, minuteB, toks)
					}
					c = cc
				}
			}
			chains[key] = c
			return c
		}
		excluded := map[int64]bool{}
		for _, t := range trade.Config.RiskExcludedTokens {
			excluded[t] = true
		}
		for _, leg := range extraLegs {
			var row *OptionChainRow
			if legChain := chainFor(leg.Expiry); legChain != nil {
				for i := range legChain.Chain {
					r := &legChain.Chain[i]
					if (leg.OptionType == "CE" && r.CEToken == leg.Token) ||
						(leg.OptionType == "PE" && r.PEToken == leg.Token) {
						row = r
						break
					}
				}
			}
			if row == nil {
				log.Printf("⚠️ extra leg trade=%s token=%d optionType=%s not found in live chain -- skipped from delta/PnL this tick", tradeUID, leg.Token, leg.OptionType)
				continue
			}

			var rawDelta, rawGamma, rawTheta, rawVega, ltp, iv float64
			if leg.OptionType == "CE" {
				rawDelta, rawGamma, rawTheta, rawVega, ltp, iv = row.CEDelta, row.CEGamma, row.CETheta, row.CEVega, row.CELtp, row.CEIV
			} else {
				rawDelta, rawGamma, rawTheta, rawVega, ltp, iv = row.PEDelta, row.PEGamma, row.PETheta, row.PEVega, row.PELtp, row.PEIV
			}

			// Wing quantity is margin-only: shown, but never in delta,
			// PnL, PointsOut, SL/TP or hedge sizing. A token can in rare
			// cases carry both (a hedge landing on an old wing strike), so
			// split it rather than classify the whole leg.
			for _, part := range []struct {
				qty  int64
				wing bool
			}{{leg.Qty - leg.WingQty, false}, {leg.WingQty, true}} {
				if part.qty == 0 {
					continue
				}
				if leg.OptionType == "CE" {
					wingNetCE += part.qty
				} else {
					wingNetPE += part.qty
				}
				d, g, th, v, pnl := signedLegGreeksAndPNL(rawDelta, rawGamma, rawTheta, rawVega, ltp, leg.EntryPrice, part.qty)
				if part.wing {
					wingPNL += pnl
				} else if excluded[leg.Token] {
					// shown below, kept out of every total
				} else {
					netDelta += d
					netGamma += g
					netTheta += th
					netVega += v
					totalPNL += pnl
				}

				// Same display convention as the straddle legs: positive
				// quantity, direction in Action.
				action, absQty := "SELL", -part.qty
				if part.qty > 0 {
					action, absQty = "BUY", part.qty
				}
				extraLegSnapshots = append(extraLegSnapshots, TradeLegSnapshot{
					Token: leg.Token, Strike: row.Strike, OptionType: leg.OptionType,
					Action: action, Quantity: absQty, EntryPrice: leg.EntryPrice, LTP: ltp, PNL: pnl,
					IV: iv, Delta: rawDelta, Gamma: rawGamma, Theta: rawTheta, Vega: rawVega,
					Wing: part.wing, Excluded: !part.wing && excluded[leg.Token], Expiry: leg.Expiry,
				})
			}
		}
	}
	if trade.Config.WingPct > 0 && (wingNetCE != 0 || wingNetPE != 0) && trade.Status == "ACTIVE" && time.Now().Second() == 0 {
		// Only reported: never auto-corrected with an order (a lagging
		// fill or an in-flight action looks the same for a moment).
		log.Printf("[WINGS] ⚠ trade=%s net across all strikes CE=%+d PE=%+d (want 0/0) -- check wings", tradeUID, wingNetCE, wingNetPE)
	}

	// Calculate PointsOut and PointsAllowed.
	//
	// Both PointsAllowed inputs (ATM straddle premium and IV) come from the
	// LIVE ATM strike, not the trade's own strike: once spot moves away,
	// the trade's strike is ITM/OTM and its straddle overstates the ATM
	// straddle (confirmed live 2026-09-28: 23000 straddle 208.30 -> 52.08
	// allowed while the live ATM 22900 straddle was ~187 -> ~46.9, delaying
	// hedges by ~5 points). Falls back to the trade's own rows only if the
	// chain has no ATM row.
	allowedRow := ceRow
	atmStraddle := ceRow.CELtp + peRow.PELtp
	avgIVPercent := (ceRow.CEIV + peRow.PEIV) / 2.0
	if atm, atmErr := FindATMRow(*chain); atmErr == nil && atm.CELtp > 0 && atm.PELtp > 0 {
		allowedRow = atm
		atmStraddle = atm.CELtp + atm.PELtp
		avgIVPercent = (atm.CEIV + atm.PEIV) / 2.0
	}
	spot := chooseUnderlying(*chain)
	// IV from broker is in % (e.g., 10.36 = 10.36%), convert to decimal (0.1036)
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

	// MTM exit: checked on EVERY tick (not only at the minute end), on
	// executable depth prices -- see mtm_exit.go. nil level = infinity.
	var mtmFloorPtr, mtmExecPtr *float64
	if trade.Config.MTMExitLevel != nil {
		ex, fl, priced, _ := s.mtmExitCheck(trade, chain, spot, straddleQuantity)
		mtmFloorPtr = &fl
		if priced {
			mtmExecPtr = &ex
		}
	}
	// ATM-straddle exit: every tick, only below its level (combined_sqf.go).
	s.straddleExitCheck(trade, chain)

	livePositions := []TradeLegSnapshot{
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
	}
	livePositions = append(livePositions, extraLegSnapshots...)

	snapshot := TradeSnapshot{
		TradeUID:              trade.TradeUID,
		Timestamp:             time.Now(),
		Status:                trade.Status,
		Symbol:                trade.Symbol,
		Expiry:                trade.Expiry,
		Strike:                trade.Strike,
		Underlying:            chooseUnderlying(*chain),
		TotalPNL:              totalPNL,
		PnLPerStraddle:        pnlPerStraddle,
		StraddleQuantity:      straddleQuantity,
		MTMExitFloor:          mtmFloorPtr,
		MTMExecPNL:            mtmExecPtr,
		PointsOut:             pointsOut,
		PointsAllowed:         pointsAllowed,
		PointsAllowedStraddle: term1,
		PointsAllowedIV:       term2,
		AllowedStrike:         allowedRow.Strike,
		RealizedPNL: func() float64 {
			switch trade.Status {
			case "CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MTM", "CLOSED_STRADDLE", "CLOSED_PORTFOLIO", "CLOSED_MANUAL", "FAILED":
				return totalPNL
			default:
				return 0
			}
		}(),
		UnrealizedPNL: func() float64 {
			switch trade.Status {
			case "CLOSED", "CLOSEDSQF", "CLOSED_SQF", "CLOSED_SL", "CLOSED_TP", "CLOSED_TIME", "CLOSED_MTM", "CLOSED_STRADDLE", "CLOSED_PORTFOLIO", "CLOSED_MANUAL", "FAILED":
				return 0
			default:
				return totalPNL
			}
		}(),
		NetDelta:      netDelta,
		NetGamma:      netGamma,
		NetTheta:      netTheta,
		NetVega:       netVega,
		LivePositions: livePositions,
		WingPct:       trade.Config.WingPct,
		WingPNL:       wingPNL,
		WingNetCE:     wingNetCE,
		WingNetPE:     wingNetPE,
	}

	s.Store.SaveSnapshot(snapshot)

	if s.Snapshot != nil {
		if pushErr := s.Snapshot.PushSnapshot(ctx, snapshot); pushErr != nil {
			log.Printf("[SNAPSHOT] push failed trade=%s err=%v", snapshot.TradeUID, pushErr)
		}
	}

	// Per-tick snapshot: unlike the once-a-minute HEDGE/SL/TP/TIME status
	// lines below (throttled by rt.LastMinuteCheck to avoid duplicate hedge
	// signals), this prints on every runMonitorCycle call -- i.e. every
	// PollIntervalSec -- so PnL/greeks/LTP movement is visible between
	// minute boundaries too, matching the reference system's per-tick
	// snapshot cadence.
	now := time.Now()
	log.Printf(
		"[MONITOR][%s] tick=%s snapshot spot=%.2f syn_fut=%.2f fut_ltp=%.2f total_pnl=%.2f pnl_per_straddle=%.2f delta=%.4f gamma=%.6f theta=%.2f vega=%.2f ce_ltp=%.2f pe_ltp=%.2f strike=%.0f ce_qty=%d pe_qty=%d lot_size=%d",
		tradeUID, now.Format("15:04:05"), spot, chain.SyntheticFuture, chain.FutureLtp, totalPNL, pnlPerStraddle,
		netDelta, netGamma, netTheta, netVega, ceRow.CELtp, peRow.PELtp,
		trade.Strike, trade.CEQty, trade.PEQty, trade.LotSize,
	)

	// Minute-end hedge eligibility check
	// Evaluate hedge only at minute boundaries to avoid duplicate signals
	currentMinute := now.Truncate(time.Minute)

	// While an exit is running (in the background) the minute-end hedge /
	// SL / TP / TIME checks wait -- not consumed, they run once it ends.
	exiting := trade.Status == "SQUARING_OFF" || trade.Status == "PARTIAL-SQF" || s.exitRunning(tradeUID)
	if rt, ok := s.Store.LoadRuntime(tradeUID); ok && !exiting {
		if rt.LastMinuteCheck.IsZero() || !rt.LastMinuteCheck.Equal(currentMinute) {
			rt.LastMinuteCheck = currentMinute

			lotSize := int64(trade.LotSize)
			if lotSize <= 0 {
				lotSize = 1
			}

			hedgeAction := "OK"
			hedgeReason := ""

			forceOneLotTest := trade.Config.ForceOneLotHedgeTest
			decision := decideHedge(hedgeDecisionInput{
				PointsOut:       pointsOut,
				PointsAllowed:   pointsAllowed,
				Spot:            spot,
				NetDelta:        netDelta,
				LotSize:         lotSize,
				TradeLots:       int64(trade.Lots),
				MinThresholdBps: resolveHedgeMinThresholdBps(trade.Config.HedgeMinThresholdBps),
				ForceOneLotTest: forceOneLotTest,
				TestPointsFloor: trade.Config.HedgePointsFloor,
				ForceRegardless: trade.Config.ForceHedgeRegardlessOfPoints,
			})
			hedgeAction = decision.Action
			pointsCrossed := pointsOut > decision.EffectiveAllowed || trade.Config.ForceHedgeRegardlessOfPoints
			minimumPointsReached := pointsOut >= decision.Floor
			deltaHasOneLot := math.Abs(netDelta) >= float64(lotSize)
			minimumHedgePoints := decision.Floor

			if decision.Hedge {
				hedgeReason = fmt.Sprintf(
					"points_out=%.2f points_allowed=%.2f effective_allowed=%.2f net_delta=%.4f lot_size=%d hedge_lots=%d force_one_lot_test=%t",
					pointsOut,
					pointsAllowed,
					decision.EffectiveAllowed,
					netDelta,
					lotSize,
					decision.Lots,
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

					hedgeErr := s.ManualHedgeLots(context.WithValue(context.Background(), hedgeTriggerKey{}, hedgeReason), tradeUID, int(decision.Lots))
					if hedgeErr != nil {
						hedgeAction = "HEDGE_FAILED"
						hedgeReason += " error=" + hedgeErr.Error()
					}

					if forceOneLotTest {
						if latest, ok := s.Store.LoadTrade(tradeUID); ok {
							latest.Config.HedgeTestExecuted = true
							latest.LastUpdateTime = time.Now()
							s.Store.UpdateTrade(latest)
							trade.Config.HedgeTestExecuted = true
						}
						hedgeReason += " test_hedge_marked_executed=true"
					}
				}
			}

			// The synthetic future a hedge WOULD place right now (direction
			// from the signed delta, size floored to whole lots) and the
			// hedge legs already held on other strikes -- shown every minute
			// so the hedge picture is visible without waiting for a trigger.
			synDir, synLegs := "NONE", "-"
			if ceSide, peSide, ok := hedgeSidesFromSignedDelta(netDelta); ok {
				synLegs = ceSide + "_CE/" + peSide + "_PE"
				synDir = "SHORT"
				if ceSide == "BUY" {
					synDir = "LONG"
				}
			}
			synLots := hedgeLotsFloor(netDelta, int64(lotSize))
			held := []string{}
			for _, l := range extraLegSnapshots {
				if l.Wing {
					continue
				}
				signed := l.Quantity
				if l.Action == "SELL" {
					signed = -signed
				}
				held = append(held, fmt.Sprintf("%s%.0f%+d@%.2f", l.OptionType, l.Strike, signed, l.LTP))
			}
			// Synthetic future at the live ATM strike (the strike a hedge
			// uses): LTP-based price, and the executable price for the
			// direction a hedge would trade (long: buy CE at ask, sell PE
			// at bid; short: sell CE at bid, buy PE at ask).
			synStrike, synCE, synPE := allowedRow.Strike, allowedRow.CELtp, allowedRow.PELtp
			synPx := synStrike + synCE - synPE
			synExec := 0.0
			switch synDir {
			case "LONG":
				if allowedRow.CEAsk > 0 && allowedRow.PEBid > 0 {
					synExec = synStrike + allowedRow.CEAsk - allowedRow.PEBid
				}
			case "SHORT":
				if allowedRow.CEBid > 0 && allowedRow.PEAsk > 0 {
					synExec = synStrike + allowedRow.CEBid - allowedRow.PEAsk
				}
			}
			heldStr := "none"
			if len(held) > 0 {
				heldStr = strings.Join(held, ",")
			}

			log.Printf(
				"[MONITOR][%s] minute=%s action=%s points_out=%.4f points_allowed=%.4f min_points=%.4f net_delta=%.4f abs_delta=%.4f lot_size=%d points_crossed=%t min_points_reached=%t delta_has_one_lot=%t force_one_lot_test=%t hedge_test_executed=%t syn=%s syn_lots=%d syn_legs=%s syn_strike=%.0f syn_ce=%.2f syn_pe=%.2f syn_px=%.2f syn_exec=%.2f syn_fut=%.2f syn_spot=%.2f fut_ltp=%.2f hedge_held=%s reason=%s",
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
				synDir,
				synLots,
				synLegs,
				synStrike,
				synCE,
				synPE,
				synPx,
				synExec,
				chain.SyntheticFuture,
				chain.SyntheticSpot,
				chain.FutureLtp,
				heldStr,
				hedgeReason,
			)

			// SL/TP/TIME are only enforced once per minute, together with the
			// minute-end hedge evaluation, to match the live reference behavior.
			// slThreshold is logged every tick, breached or not (matching
			// tpThreshold below) -- previously it stayed 0.0 until the
			// instant SL actually breached, so every "OK" tick logged
			// "threshold +0.00" even with a real, actively-checked
			// threshold configured. Display-only: the breach check itself
			// always used the correctly-computed local `threshold`.
			slBreached, slThreshold, slSource := false, 0.0, ""
			if trade.Config.SLPointsPerLot > 0 {
				lots, threshold := slThresholdForTrade(trade)
				slThreshold = threshold
				slSource = fmt.Sprintf("points_per_lot lots=%.2f", lots)
				if totalPNL <= threshold {
					slBreached = true
				}
			}
			if !slBreached && trade.Config.SLPnLBpsOfSpot > 0 {
				threshold := -bpsOfSpotThreshold(spot, trade.Config.SLPnLBpsOfSpot)
				slThreshold = threshold
				slSource = fmt.Sprintf("bps_of_spot spot=%.2f bps=%.2f", spot, trade.Config.SLPnLBpsOfSpot)
				if pnlPerStraddle <= threshold {
					slBreached = true
				}
			}
			// Trigger lines carry the same PnL/Greeks snapshot fields as the
			// per-tick "snapshot" line above, so a real exit event is
			// self-contained -- an operator reading just this one line gets
			// full context (spot, greeks, CE/PE LTP) instead of having to
			// cross-reference the nearest snapshot line by timestamp.
			snapshotFields := fmt.Sprintf(
				"spot=%.2f delta=%.4f gamma=%.6f theta=%.2f vega=%.2f ce_ltp=%.2f pe_ltp=%.2f",
				spot, netDelta, netGamma, netTheta, netVega, ceRow.CELtp, peRow.PELtp,
			)

			if slBreached {
				log.Printf(
					"[RISK] SL_TRIGGER trade=%s source=%s pnl=%.2f pnl_per_straddle=%.2f threshold=%.2f %s",
					tradeUID, slSource, totalPNL, pnlPerStraddle, slThreshold, snapshotFields,
				)
				s.executeAutoExit(tradeUID, "SL", "CLOSED_SL")
			}

			tpThreshold := 0.0
			if trade.Config.TPPnLBpsOfSpot > 0 {
				tpThreshold = bpsOfSpotThreshold(spot, trade.Config.TPPnLBpsOfSpot)
				if pnlPerStraddle >= tpThreshold {
					log.Printf(
						"[RISK] TP_TRIGGER trade=%s pnl_per_straddle=%.2f threshold=%.2f bps=%.2f %s",
						tradeUID, pnlPerStraddle, tpThreshold, trade.Config.TPPnLBpsOfSpot, snapshotFields,
					)
					s.executeAutoExit(tradeUID, "TP", "CLOSED_TP")
				}
			}

			if !trade.Config.SquareOffHardTime.IsZero() && !time.Now().Before(trade.Config.SquareOffHardTime) {
				log.Printf(
					"[RISK] TIME_TRIGGER trade=%s now=%s target=%s %s",
					tradeUID, time.Now().Format(time.RFC3339), trade.Config.SquareOffHardTime.Format(time.RFC3339), snapshotFields,
				)
				s.executeAutoExit(tradeUID, "TIME", "CLOSED_TIME")
			}

			slStatus := "NOT_CONFIGURED"
			if slBreached {
				slStatus = "BREACHED"
			} else if trade.Config.SLPointsPerLot > 0 || trade.Config.SLPnLBpsOfSpot > 0 {
				slStatus = "OK"
			}
			log.Printf(
				"[MONITOR][%s] minute=%s check=SL status=%s source=%q pnl=%.2f pnl_per_straddle=%.2f threshold=%.2f",
				tradeUID, currentMinute.Format("15:04"), slStatus, slSource, totalPNL, pnlPerStraddle, slThreshold,
			)

			tpStatus, tpThresholdLog := "DISABLED", 0.0
			if trade.Config.TPPnLBpsOfSpot > 0 {
				tpThresholdLog = bpsOfSpotThreshold(spot, trade.Config.TPPnLBpsOfSpot)
				tpStatus = "OK"
				if pnlPerStraddle >= tpThresholdLog {
					tpStatus = "BREACHED"
				}
			}
			log.Printf(
				"[MONITOR][%s] minute=%s check=TP status=%s pnl_per_straddle=%.2f threshold=%.2f bps=%.2f",
				tradeUID, currentMinute.Format("15:04"), tpStatus, pnlPerStraddle, tpThresholdLog, trade.Config.TPPnLBpsOfSpot,
			)

			timeStatus, remaining := "NOT_CONFIGURED", time.Duration(0)
			if !trade.Config.SquareOffHardTime.IsZero() {
				remaining = time.Until(trade.Config.SquareOffHardTime)
				timeStatus = "OK"
				if remaining <= 0 {
					timeStatus = "BREACHED"
				}
			}
			log.Printf(
				"[MONITOR][%s] minute=%s check=TIME status=%s target=%s remaining=%s",
				tradeUID, currentMinute.Format("15:04"), timeStatus,
				trade.Config.SquareOffHardTime.Format("15:04:05"), remaining.Round(time.Second),
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

	// For a trade whose build did not finish cleanly the stored quantities
	// are the INTENDED ones. Sizing the exit from them bought back a leg that
	// had never been sold (2026-09-21: a NOV PE), leaving an unintended long
	// position. Size it from what the orders actually filled instead.
	// ACTIVE (partial) too: exit exactly what was EXECUTED, re-read from the
	// exchange fills right before sizing, never the requested build size.
	// Every status: exit what the exchange fills say is open, never the
	// stored size (2026-10-06: two builds filed under one trade -- stored
	// 260/260, really 520/520; sizing from the stored header would have left
	// half the position open).
	{
		if p, ok := s.Store.(interface {
			TradeOpenQuantities(ctx context.Context, tradeUID string) (int64, int64, error)
		}); ok {
			ce, pe, err := p.TradeOpenQuantities(context.Background(), tradeUID)
			if err != nil {
				return fmt.Errorf("cannot square off %s: unable to verify open quantities from orders: %w", tradeUID, err)
			}
			partial := tr.Status == "PARTIAL" || tr.Status == "RECONCILIATION_REQUIRED" || tr.Config.PartialFill
			if ce == 0 && pe == 0 && !partial && (tr.CEQty > 0 || tr.PEQty > 0) {
				// No fills recorded at all for a normal ACTIVE trade (fills
				// not persisted yet): keep the stored size rather than refuse
				// to exit.
				log.Printf("⚠️ SquareOff %s: no exchange fills recorded on its CE/PE tokens -- exiting the stored CE=%d PE=%d", tradeUID, tr.CEQty, tr.PEQty)
			} else {
				if int64(tr.CEQty) != ce || int64(tr.PEQty) != pe {
					log.Printf("⚠️ SquareOff %s status=%s: stored quantity CE=%d PE=%d differs from filled orders CE=%d PE=%d -- using the filled orders",
						tradeUID, tr.Status, tr.CEQty, tr.PEQty, ce, pe)
				}
				tr.CEQty, tr.PEQty = int(ce), int(pe)
			}
		}
	}

	if tr.CEQty <= 0 && tr.PEQty <= 0 {
		// The straddle itself is already flat, but an additional leg (e.g.
		// an ATM hedge) can still be open -- most importantly when an
		// earlier SquareOff flattened the straddle and then failed to close
		// the hedge, which leaves the trade in its prior status with
		// CEQty=PEQty=0. Refusing here would make every retry (manual, or
		// the monitor's own SL/TP/TIME exit) bail out before ever reaching
		// that hedge, leaving it open indefinitely.
		extra, err := s.extraOpenLegs(tradeUID, tr)
		if err != nil {
			return fmt.Errorf("cannot square off %s: unable to check additional open legs: %w", tradeUID, err)
		}
		if len(extra) == 0 {
			return fmt.Errorf("cannot square off %s: no open CE/PE quantity found", tradeUID)
		}

		prevStatus := tr.Status
		tr.Status = "SQUARING_OFF"
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)

		if err := s.closeExtraLegsThenWings(tradeUID, tr, "SQF-"+reason); err != nil {
			tr.Status = prevStatus
			tr.LastUpdateTime = time.Now()
			s.Store.UpdateTrade(tr)
			return fmt.Errorf("square-off of remaining additional legs failed for %s: %w", tradeUID, err)
		}

		tr.Status = closedStatusForReason(reason)
		tr.ClosedAt = time.Now()
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		log.Printf("✅ Square-off completed for %s reason=%s status=%s (straddle already flat; closed %d additional leg(s))", tradeUID, reason, tr.Status, len(extra))
		return nil
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

	maxOrderQty := int(s.resolveMaxOrderQty(tr.Symbol, lotSize))
	baseLots := int(targetCE / lotSize)
	if peLots := int(targetPE / lotSize); peLots > baseLots {
		baseLots = peLots
	}

	remainingCE := targetCE
	remainingPE := targetPE
	var allVerifiedFills []BrokerFill

	// Hedge legs come down WITH the straddle: after every chunk each extra
	// leg is reduced to the same fraction the straddle has closed (whole
	// lots), so the position keeps its ratio and delta all the way down
	// instead of the straddle first and the hedges at the end (delta drifted
	// mid-exit, 14:01 / 14:18 2026-10-09). The full close after the loop
	// stays as the safety net for whatever is left.
	extraInit := map[int64]int64{}
	if extra, xerr := s.extraOpenLegs(tradeUID, tr); xerr == nil {
		for _, l := range extra {
			if q := abs64(l.Qty - l.WingQty); q > 0 {
				extraInit[l.Token] = q
			}
		}
	}
	extraDone := map[int64]int64{}

	const maxExecutionAttempts = 4
	// Orders whose outcome is unknown (no terminal confirmation). A retry
	// round re-sends the "remaining" quantity, and an unconfirmed order may
	// well have filled -- so while any exist, NOTHING more is sent (live
	// 2026-09-30: unconfirmed exits were re-sent and over-bought).
	var unconfirmedOrders []string

	for executionAttempt := 1; executionAttempt <= maxExecutionAttempts; executionAttempt++ {
		if remainingCE <= 0 && remainingPE <= 0 {
			break
		}
		if len(unconfirmedOrders) > 0 {
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

		// Use aggressive chunking for SL-triggered square-off (max lots/order
		// for fastest exit) -- the trade's SL, the portfolio SL and the
		// emergency Square off ALL.
		var chunks [][]ExecOrder
		var err error
		if reason == "SL" || reason == "PORTFOLIO" || reason == "EMERGENCY" {
			chunks, err = GenerateAggressiveChunkedOrders(
				fmt.Sprintf("S%sA%d", tradeUID, executionAttempt),
				legs,
				baseLots,
				maxOrderQty,
			)
		} else {
			// One lot per leg per order ("65-65") for a non-SL square-off,
			// verifying each order's real, live-confirmed outcome before
			// deciding what (if anything) still needs squaring off --
			// never batching the whole remaining quantity into fewer,
			// larger orders and guessing at completion from a timed poll.
			// SL keeps the aggressive (fewest-orders, max-lots-per-order)
			// path above: a real stop-loss still needs to exit fast, not
			// lot-by-lot.
			chunks, err = GenerateChunkedOrders(
				fmt.Sprintf("S%sA%d", tradeUID, executionAttempt),
				legs,
				baseLots,
				maxOrderQty,
				1,
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
			submitted := make(map[string]submittedOrderMeta)
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
					LotSize:         int64(tr.LotSize),
					OrderType:       "MARKET",
					ProductType:     tr.ProductType,
					LegType:         order.OptionType,
					Phase:           "SQF",
					OrderUID:        order.UID,
					BrokerName:      tr.BrokerName,
					AccountID:       tr.AccountID,
					ExpectedPrice:   order.ExpectedPrice,
				}

				// Never close more than the trade holds on this token.
				if gerr := s.closeQtyGuard(context.Background(), tradeUID, &intent); gerr != nil {
					log.Printf("❌ SQF order not sent trade=%s leg=%s qty=%d: %v", tradeUID, order.OptionType, order.Quantity, gerr)
					continue
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

				submitted[res.BrokerOrderID] = submittedOrderMeta{
					Token:    order.Token,
					Leg:      order.OptionType,
					Quantity: intent.Quantity, // after the close guard
				}
				if order.OptionType == "CE" {
					requestedCE += intent.Quantity
				} else if order.OptionType == "PE" {
					requestedPE += intent.Quantity
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

			verifyCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			summary := waitForVerifiedFillsLive(
				verifyCtx,
				s.OrderEvents,
				provider,
				tradeUID,
				submitted,
				tr.CEToken,
				tr.PEToken,
				requestedCE,
				requestedPE,
				15*time.Second,
			)
			cancel()

			unconfirmedOrders = append(unconfirmedOrders, summary.Unconfirmed...)
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

			if len(extraInit) > 0 && targetCE+targetPE > 0 && (remainingCE > 0 || remainingPE > 0) {
				frac := float64(targetCE-remainingCE+targetPE-remainingPE) / float64(targetCE+targetPE)
				if _, _, xerr := s.reduceExtraLegsInStep(tradeUID, tr, extraInit, extraDone, frac, "SQF"); xerr != nil {
					log.Printf("⚠️ SQF in-step hedge reduction trade=%s: %v -- the rest closes after the straddle", tradeUID, xerr)
				}
			}

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

	if len(unconfirmedOrders) > 0 && (remainingCE > 0 || remainingPE > 0) {
		// Stop here: the true remaining position is unknown. Never send a
		// retry, and stop the monitor so a TIME/SL/TP re-trigger can't
		// either. A human checks the broker position and closes the rest.
		tr.CEQty = int(remainingCE)
		tr.PEQty = int(remainingPE)
		tr.Status = "RECONCILIATION_REQUIRED"
		tr.Config.ExitHalted = true
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		if rt, ok := s.Store.LoadRuntime(tradeUID); ok {
			close(rt.StopCh)
			s.Store.DeleteRuntime(tradeUID)
		}
		log.Printf("🚫 SQF STOPPED trade=%s reason=%s: %d order(s) unconfirmed %v -- NOT re-sending; confirmed so far CE=%d/%d PE=%d/%d; monitor stopped, status=RECONCILIATION_REQUIRED -- check the broker position",
			tradeUID, reason, len(unconfirmedOrders), unconfirmedOrders,
			targetCE-remainingCE, targetCE, targetPE-remainingPE, targetPE)
		return fmt.Errorf("square-off stopped: %d order(s) unconfirmed, not re-sending; check broker position", len(unconfirmedOrders))
	}

	verifiedCE := targetCE - remainingCE
	verifiedPE := targetPE - remainingPE

	tr.CEQty = int(remainingCE)
	tr.PEQty = int(remainingPE)
	tr.LastUpdateTime = time.Now()

	if remainingCE == 0 && remainingPE == 0 {
		// Close any additional open legs (e.g. a hedge placed at a
		// different, live-ATM strike -- see ManualHedgeLots) BEFORE
		// declaring the trade closed. Confirmed live 2026-09-25: a real
		// hedge leg on a neighboring strike was left open after
		// SquareOff, because SquareOff only ever closed tr.CEQty/tr.PEQty
		// on tr.CEToken/tr.PEToken and had no way to know that leg
		// existed. A failure here must not mark the trade closed --
		// better to leave it in its prior status (so the monitor keeps
		// watching it and a retry is possible) than to falsely report a
		// real open position as CLOSEDSQF.
		if extraErr := s.closeExtraLegsThenWings(tradeUID, tr, "SQF-"+reason); extraErr != nil {
			log.Printf("⚠️ SQF extra-leg close failed for %s: %v -- trade NOT marked closed", tradeUID, extraErr)
			tr.Status = prevStatus
			tr.LastUpdateTime = time.Now()
			s.Store.UpdateTrade(tr)
			return fmt.Errorf("square-off closed the straddle but failed to close additional open legs: %w", extraErr)
		}

		tr.Status = closedStatusForReason(reason)
		tr.ClosedAt = time.Now()
	} else {
		tr.Status = prevStatus
	}

	s.Store.UpdateTrade(tr)

	// Realized PnL from the trade's own order fills. The broker-fill path
	// below matched on a strategy key our orders never carry, so it always
	// found nothing and every trade stored 0.
	if calc, ok := s.Store.(interface {
		TradeRealizedPnL(ctx context.Context, tradeUID string) (float64, error)
	}); ok {
		if v, err := calc.TradeRealizedPnL(context.Background(), tradeUID); err != nil {
			log.Printf("⚠️ realized PnL for %s not stored: %v", tradeUID, err)
		} else {
			tr.RealizedPnL = v
			s.Store.UpdateTrade(tr)
			log.Printf("realized PnL stored trade=%s status=%s realized=%.2f (gross, from order fills)", tradeUID, tr.Status, v)
		}
	} else if realizedPnL, ok := s.computeVerifiedRealizedPnL(context.Background(), tr); ok {
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

// closeExtraOpenLegs closes any trade_legs row still OPEN on a token
// OTHER than the trade's own CE/PE (e.g. a hedge placed at a different,
// live-ATM strike -- see ManualHedgeLots' live-ATM resolution). It reads
// from the durable trade_legs table (LoadOpenLegs), not the in-memory
// tr.CEQty/tr.PEQty, as the source of truth for what's genuinely still
// open, so this correctly finds and closes a hedge leg even if the
// process restarted between the hedge and this square-off. Best-effort
// per leg in the sense that it stops and returns an error on the first
// failure rather than silently leaving a later leg unclosed.
// extraOpenLegs returns this trade's currently-OPEN trade_legs rows that
// are on neither tr.CEToken nor tr.PEToken -- i.e. any additional leg
// such as a hedge placed at a different (live ATM) strike. Returns
// (nil, nil) for a MemoryStore, which has no trade_legs concept.
func (s *Service) extraOpenLegs(tradeUID string, tr StoredTrade) ([]OpenLeg, error) {
	pgStore, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return nil, nil
	}

	legs, err := pgStore.LoadOpenLegs(tradeUID)
	if err != nil {
		return nil, fmt.Errorf("load open legs: %w", err)
	}

	var extra []OpenLeg
	for _, leg := range legs {
		if leg.Token != tr.CEToken && leg.Token != tr.PEToken {
			extra = append(extra, leg)
		}
	}
	return extra, nil
}

// closedStatusForReason preserves the audit distinction between a real
// stop-loss/take-profit/time-triggered close and a manual/other
// square-off -- each is already a recognized terminal status elsewhere
// in this file (see the status-guard switches).
func closedStatusForReason(reason string) string {
	switch reason {
	case "SL":
		return "CLOSED_SL"
	case "TP":
		return "CLOSED_TP"
	case "TIME":
		return "CLOSED_TIME"
	case "MTM":
		return "CLOSED_MTM"
	case "STRADDLE":
		return "CLOSED_STRADDLE"
	case "PORTFOLIO":
		return "CLOSED_PORTFOLIO"
	default:
		return "CLOSEDSQF"
	}
}

// closeExtraOpenLegs fully closes every additional open leg (100%). Used
// by a full SquareOff.
// Wings are NOT closed here -- the caller closes them last, after every
// short is flat (closeAllWings), so no short is ever left without its wing.
func (s *Service) closeExtraOpenLegs(tradeUID string, tr StoredTrade) error {
	_, _, err := s.reduceExtraOpenLegs(tradeUID, tr, 100, "SQFX", "SQF")
	return err
}

// closeExtraLegsThenWings is the full-exit tail: hedge legs first, then
// every wing held (net target 0 once nothing else is open).
func (s *Service) closeExtraLegsThenWings(tradeUID string, tr StoredTrade, reason string) error {
	if err := s.closeExtraOpenLegs(tradeUID, tr); err != nil {
		return err
	}
	if err := s.closeAllWings(context.Background(), tr, reason); err != nil {
		return fmt.Errorf("close wings: %w", err)
	}
	return nil
}

// reduceExtraOpenLegs closes `percentage` of every additional open leg
// (e.g. a hedge at a different, live-ATM strike -- see ManualHedgeLots),
// lot-aligned, in the direction that shrinks it. Used by both a full
// SquareOff (percentage=100, via closeExtraOpenLegs) and a
// PartialSquareOff (percentage matching the straddle's own trim), so a
// partial exit reduces the hedge by the SAME proportion instead of
// leaving all of it open to cover a position that's now smaller, or
// closing all of it when only part of the straddle was meant to exit.
//
// Only the NON-wing part of each leg is traded (wings are adjusted by the
// caller). Returns how much the net SHORT per type changed (negative =
// shrank), from confirmed orders only, so the caller can move the wings by
// exactly that much.
func (s *Service) reduceExtraOpenLegs(tradeUID string, tr StoredTrade, percentage float64, tagPrefix, phase string) (shortChangeCE, shortChangePE int64, retErr error) {
	if percentage <= 0 {
		return 0, 0, nil
	}
	lot := int64(tr.LotSize)
	if lot <= 0 {
		lot = 1
	}
	return s.reduceExtraLegsBy(tradeUID, tr, tagPrefix, phase, func(leg OpenLeg, absQty int64) int64 {
		if percentage >= 100 {
			return absQty
		}
		lots := int64(math.Round(float64(absQty) * (percentage / 100.0) / float64(lot)))
		if lots <= 0 && absQty > 0 {
			lots = 1
		}
		return min(lots*lot, absQty)
	}, fmt.Sprintf("%.0f%% of open", percentage))
}

// reduceExtraLegsInStep brings every extra leg down to frac of its size at
// the start of the exit (whole lots, never below what is still open);
// done tracks what this exit already closed per token.
func (s *Service) reduceExtraLegsInStep(tradeUID string, tr StoredTrade, init, done map[int64]int64, frac float64, phase string) (shortChangeCE, shortChangePE int64, err error) {
	lot := int64(tr.LotSize)
	if lot <= 0 {
		lot = 1
	}
	want := map[int64]int64{}
	for tok, q0 := range init {
		target := int64(math.Round(float64(q0)*frac/float64(lot))) * lot
		if n := min(target, q0) - done[tok]; n > 0 {
			want[tok] = n
		}
	}
	if len(want) == 0 {
		return 0, 0, nil
	}
	sent := map[int64]int64{}
	shortChangeCE, shortChangePE, err = s.reduceExtraLegsBy(tradeUID, tr, "SQFS", phase, func(leg OpenLeg, absQty int64) int64 {
		q := min(want[leg.Token], absQty)
		sent[leg.Token] += q
		return q
	}, fmt.Sprintf("in step, %.0f%% of the straddle closed", frac*100))
	for tok, q := range sent {
		done[tok] += q
	}
	return shortChangeCE, shortChangePE, err
}

// reduceExtraLegsBy closes qtyFor(leg, open) of every extra open leg in the
// direction that shrinks it, in orders no bigger than the per-order max,
// each confirmed before the next.
func (s *Service) reduceExtraLegsBy(tradeUID string, tr StoredTrade, tagPrefix, phase string, qtyFor func(leg OpenLeg, absQty int64) int64, what string) (shortChangeCE, shortChangePE int64, retErr error) {

	extra, err := s.extraOpenLegs(tradeUID, tr)
	if err != nil {
		return 0, 0, err
	}
	hasReal := false
	for _, leg := range extra {
		if leg.Qty-leg.WingQty != 0 {
			hasReal = true
		}
	}
	if !hasReal {
		return 0, 0, nil
	}

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return 0, 0, fmt.Errorf("get executor: %w", err)
	}
	record := func(optionType, side string, qty int64) {
		if optionType == "CE" {
			shortChangeCE += shortChangeFromFill(side, qty)
		} else {
			shortChangePE += shortChangeFromFill(side, qty)
		}
	}

	lotSize := int64(tr.LotSize)
	if lotSize <= 0 {
		lotSize = 1
	}

	for idx, leg := range extra {
		realQty := leg.Qty - leg.WingQty
		side := "SELL"
		absQty := realQty
		if realQty < 0 {
			side = "BUY"
			absQty = -realQty
		}
		if absQty <= 0 {
			continue
		}

		qty := qtyFor(leg, absQty)
		if qty > absQty {
			qty = absQty
		}
		if qty <= 0 {
			continue
		}

		// Split into orders no bigger than the live per-order max (e.g.
		// NIFTY 1755): a hedge built in several tranches can be larger than
		// one order is allowed to be, and a single oversized exit order
		// would be rejected by the exchange.
		maxPer := s.resolveMaxOrderQty(tr.Symbol, lotSize)
		for piece, remaining := 0, qty; remaining > 0; piece++ {
			pieceQty := remaining
			if pieceQty > maxPer {
				pieceQty = maxPer
			}

			intentID := BuildShortOrderUID(tr.Symbol, fmt.Sprintf("%s%dP%d", tagPrefix, leg.Token, piece), time.Now(), idx)
			intent := OrderIntent{
				IntentID:        intentID,
				TradeUID:        tradeUID,
				Token:           leg.Token,
				Symbol:          tr.Symbol,
				ExchangeSegment: leg.Exchange,
				Side:            side,
				Quantity:        pieceQty,
				LotSize:         lotSize,
				OrderType:       "MARKET",
				ProductType:     tr.ProductType,
				Phase:           phase,
				OrderUID:        intentID,
				BrokerName:      tr.BrokerName,
				AccountID:       tr.AccountID,
			}

			log.Printf(
				"📝 Persisting %s extra-leg order to DB before execution: trade=%s token=%d side=%s qty=%d (piece %d, %s, open %d, max/order %d)",
				phase, tradeUID, leg.Token, side, pieceQty, piece+1, what, absQty, maxPer,
			)
			brokerOrderID, _, err := s.submitOrderIntent(context.Background(), executor, tradeUID, intent)
			if err != nil {
				return shortChangeCE, shortChangePE, fmt.Errorf("reduce extra leg token=%d: %w", leg.Token, err)
			}

			waitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			upd, err := s.OrderEvents.WaitTerminal(waitCtx, tradeUID, brokerOrderID, pieceQty, 15*time.Second)
			cancel()
			if upd.FilledQty > 0 {
				got := upd.FilledQty
				if got > pieceQty {
					got = pieceQty
				}
				record(leg.OptionType, side, got)
			}
			if err != nil {
				return shortChangeCE, shortChangePE, fmt.Errorf("reduce extra leg token=%d order=%s: no terminal confirmation: %w", leg.Token, brokerOrderID, err)
			}

			log.Printf(
				"✅ %s extra leg reduced trade=%s token=%d side=%s qty=%d broker_order_id=%s",
				phase, tradeUID, leg.Token, side, pieceQty, brokerOrderID,
			)
			remaining -= pieceQty
		}
	}

	return shortChangeCE, shortChangePE, nil
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
	// Always size from what is OPEN per the exchange fills, never the stored
	// size: a stale stored quantity would buy back more than is short and
	// leave a long (exits from several rules combined never exceed 100%).
	{
		if p, ok := s.Store.(interface {
			TradeOpenQuantities(ctx context.Context, tradeUID string) (int64, int64, error)
		}); ok {
			ce, pe, err := p.TradeOpenQuantities(context.Background(), tradeUID)
			if err != nil {
				return fmt.Errorf("cannot partially square off %s: unable to verify executed quantities: %w", tradeUID, err)
			}
			if int64(tr.CEQty) != ce || int64(tr.PEQty) != pe {
				log.Printf("⚠️ PartialSquareOff %s: stored CE=%d PE=%d differs from open per fills CE=%d PE=%d -- using the fills", tradeUID, tr.CEQty, tr.PEQty, ce, pe)
			}
			if ce == 0 && pe == 0 && !tr.Config.PartialFill && (tr.CEQty > 0 || tr.PEQty > 0) {
				log.Printf("⚠️ PartialSquareOff %s: no exchange fills recorded yet -- using the stored CE=%d PE=%d", tradeUID, tr.CEQty, tr.PEQty)
			} else {
				tr.CEQty, tr.PEQty = int(ce), int(pe)
			}
		}
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

	// Calculate partial quantities aligned with LotSize multiples. No
	// hardcoded fallback here -- lot sizes differ a lot across symbols
	// (NIFTY 65, BANKNIFTY 15, SENSEX 20, FINNIFTY 40 as of 2026-09-23) and
	// silently guessing 65 for a trade whose real LotSize wasn't persisted
	// would size a REAL partial-exit order wrong. Matches the same
	// refuse-to-guess stance as GetFallbackLotSize.
	if tr.LotSize <= 0 {
		tr.Status = prevStatus
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		return fmt.Errorf("cannot partially square off %s: trade has no resolved lot size", tradeUID)
	}
	lotSize := int64(tr.LotSize)

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
	maxOrderQty := int(s.resolveMaxOrderQty(tr.Symbol, lotSize))
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

	// Hedge legs come down with the straddle, chunk by chunk, to the same
	// fraction (see SquareOff); the end of the exit takes them to the exact
	// percentage.
	extraInit := map[int64]int64{}
	if extra, xerr := s.extraOpenLegs(tradeUID, tr); xerr == nil {
		for _, l := range extra {
			if q := abs64(l.Qty - l.WingQty); q > 0 {
				extraInit[l.Token] = q
			}
		}
	}
	extraDone := map[int64]int64{}
	var stepCE, stepPE int64

	for chunkIdx, chunk := range chunks {
		submitted := make(map[string]submittedOrderMeta)
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
				LotSize:         lotSize,
				OrderType:       "MARKET",
				ProductType:     tr.ProductType,
				ExchangeSegment: tr.ExchangeSegment,
				LegType:         order.OptionType,
				Phase:           "PSQF",
				OrderUID:        order.UID,
				BrokerName:      tr.BrokerName,
				AccountID:       tr.AccountID,
			}

			// Never close more than the trade holds on this token.
			if gerr := s.closeQtyGuard(context.Background(), tradeUID, &intent); gerr != nil {
				log.Printf("❌ PSQF chunk=%d leg=%s qty=%d not sent: %v", chunkIdx+1, order.OptionType, order.Quantity, gerr)
				continue
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

			submitted[res.BrokerOrderID] = submittedOrderMeta{
				Token:    order.Token,
				Leg:      order.OptionType,
				Quantity: intent.Quantity, // after the close guard
			}
			if order.OptionType == "CE" {
				requestedCE += intent.Quantity
			} else if order.OptionType == "PE" {
				requestedPE += intent.Quantity
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

		verifyCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		summary := waitForVerifiedFillsLive(
			verifyCtx, s.OrderEvents, provider, tradeUID, submitted,
			tr.CEToken, tr.PEToken, requestedCE, requestedPE,
			15*time.Second,
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

		if len(extraInit) > 0 && ceQty+peQty > 0 && (remainingCE > 0 || remainingPE > 0) {
			frac := float64(ceQty-remainingCE+peQty-remainingPE) / float64(ceQty+peQty) * percentage / 100
			xce, xpe, xerr := s.reduceExtraLegsInStep(tradeUID, tr, extraInit, extraDone, frac, "PSQF")
			stepCE, stepPE = stepCE+xce, stepPE+xpe
			if xerr != nil {
				log.Printf("⚠️ PSQF in-step hedge reduction trade=%s: %v -- the rest is reduced at the end", tradeUID, xerr)
			}
		}
	}

	verifiedCE := ceQty - remainingCE
	verifiedPE := peQty - remainingPE

	tr.CEQty = int(math.Max(0, float64(tr.CEQty)-float64(verifiedCE)))
	tr.PEQty = int(math.Max(0, float64(tr.PEQty)-float64(verifiedPE)))
	tr.LastUpdateTime = time.Now()

	// Reduce any additional leg (e.g. a hedge at a different, live-ATM
	// strike) by the SAME percentage, once the straddle's own trim has
	// actually fully verified -- a partial exit shrinks the whole
	// position proportionally, hedge included, rather than leaving the
	// hedge at its old size covering a now-smaller position. If the
	// straddle's own trim wasn't fully verified, leave the hedge alone
	// (don't partially unwind a hedge for an exit that didn't happen).
	// If this fully closed the straddle (100%, or CE/PE otherwise hit
	// zero), close the extra legs fully too, same as SquareOff.
	if remainingCE == 0 && remainingPE == 0 {
		extraPct := percentage
		if tr.CEQty == 0 && tr.PEQty == 0 {
			extraPct = 100
		}
		// What the chunks already took off the hedges counts toward the
		// percentage: only the remainder to exactly extraPct goes out now.
		extraCE, extraPE, extraErr := s.reduceExtraLegsInStep(tradeUID, tr, extraInit, extraDone, extraPct/100, "PSQF")
		if len(extraInit) == 0 {
			extraCE, extraPE, extraErr = s.reduceExtraOpenLegs(tradeUID, tr, extraPct, "PSQFX", "PSQF")
		}
		extraCE, extraPE = extraCE+stepCE, extraPE+stepPE
		// Wings follow whatever net short was actually removed (the
		// straddle's verified trim plus the hedge trim), LIFO -- or all of
		// them once nothing short is left.
		var wingErr error
		if tr.CEQty == 0 && tr.PEQty == 0 && extraErr == nil {
			wingErr = s.closeAllWings(context.Background(), tr, "PSQF")
		} else {
			wingErr = s.adjustWings(context.Background(), executor, tr,
				-verifiedCE+extraCE, -verifiedPE+extraPE, tr.Strike, tr.Strike, "PSQF")
		}
		if wingErr != nil {
			log.Printf("[WINGS] ⚠ PSQF wing reduction for %s: %v", tradeUID, wingErr)
		}
		if extraErr != nil {
			log.Printf("⚠️ PSQF extra-leg reduction failed for %s: %v -- trade NOT marked closed", tradeUID, extraErr)
			tr.Status = prevStatus
			s.Store.UpdateTrade(tr)
			return fmt.Errorf("partial square-off trimmed the straddle but failed to reduce additional open legs by the same %%: %w", extraErr)
		}
		if wingErr != nil && tr.CEQty == 0 && tr.PEQty == 0 {
			tr.Status = prevStatus
			s.Store.UpdateTrade(tr)
			return fmt.Errorf("partial square-off closed the position but failed to close wings: %w", wingErr)
		}
	}

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

// applyHedgeFill books one verified hedge fill into a leg quantity. The
// single-strike trade model stores each leg as a short-side magnitude, so a
// SELL grows it and a BUY shrinks it. A BUY larger than the open quantity
// would mean a net-long leg, which the model (and SquareOff, which only
// ever buys) cannot represent -- that is an error, never a silent clamp.
func applyHedgeFill(qty int, side string, filled int64) (int, error) {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "SELL":
		return qty + int(filled), nil
	case "BUY":
		if int64(qty) < filled {
			return qty, fmt.Errorf("hedge BUY of %d exceeds open short quantity %d", filled, qty)
		}
		return qty - int(filled), nil
	}
	return qty, fmt.Errorf("unknown hedge side %q", side)
}

// bookHedgeFills returns the CE/PE quantities after applying the broker-
// verified hedge fills.
func bookHedgeFills(ceQty, peQty int, ceSide, peSide string, filledCE, filledPE int64) (int, int, error) {
	newCE, err := applyHedgeFill(ceQty, ceSide, filledCE)
	if err != nil {
		return ceQty, peQty, fmt.Errorf("CE: %w", err)
	}
	newPE, err := applyHedgeFill(peQty, peSide, filledPE)
	if err != nil {
		return ceQty, peQty, fmt.Errorf("PE: %w", err)
	}
	return newCE, newPE, nil
}

// ManualHedgeNow hedges the position's COMPLETE current net delta -- the
// server's own live snapshot delta, which already includes any earlier
// hedge legs (runMonitorCycle folds them in) -- in whole lots rounded
// DOWN (hedgeLotsFloor), so it never overshoots to the other side. Unlike
// the minute-end check it has no points-out/allowed gate: it's an
// explicit manual instruction. Returns the number of lots actually
// requested; 0 with a nil error means |delta| is under one lot and
// nothing was placed.
//
// Never sized from a caller-supplied delta: the UI's portfolio row carried
// StoredTrade.NetDelta, which is the entry delta from build time and is
// never updated, so a UI-supplied value can be arbitrarily stale.
func (s *Service) ManualHedgeNow(ctx context.Context, tradeUID string) (int64, error) {
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return 0, fmt.Errorf("trade not found")
	}
	if tr.Status != "ACTIVE" {
		return 0, fmt.Errorf("hedge requires an ACTIVE trade, status=%s", tr.Status)
	}
	if tr.LotSize <= 0 {
		return 0, fmt.Errorf("cannot hedge %s: trade has no resolved lot size", tradeUID)
	}
	snap, ok := s.Store.LoadSnapshot(tradeUID)
	if !ok {
		return 0, fmt.Errorf("snapshot not found")
	}

	lots := hedgeLotsFloor(snap.NetDelta, int64(tr.LotSize))
	if lots <= 0 {
		log.Printf("[HEDGE] manual hedge trade=%s: net delta %.3f is under one lot (%d) -- nothing placed", tradeUID, snap.NetDelta, tr.LotSize)
		return 0, nil
	}
	log.Printf("[HEDGE] manual hedge trade=%s: net delta %.3f -> %d lot(s) of %d", tradeUID, snap.NetDelta, lots, tr.LotSize)
	return lots, s.ManualHedgeLots(ctx, tradeUID, int(lots))
}

// hedgeLegState tracks one leg's resting order across a tranche's
// modify-until-filled attempts.
//
// GreekSoft's FilledQty (qty_filled_today, see normalize.go) is CUMULATIVE
// per broker order for the day, not a per-push delta -- and ModifyOrderPrice
// genuinely changes the order's requested quantity at the broker (verified
// against executor.go's real ModifyOrder call), it does not just re-price
// while silently keeping the old quantity. So a resting order's own target
// (orderTargetQty) must stay FIXED across every modify (matching chase's
// own established, production-proven pattern of always re-sending the same
// quantity) -- shrinking it on every attempt would ask the broker to reduce
// an order below what it may have already filled, which is invalid. Each
// leg's real total is filledBeforeCurrent (from an earlier order on this
// leg that went terminal-but-short, e.g. cancelled after a partial fill)
// plus currentOrderFilled (the live, cumulative fill of whatever order is
// resting right now) -- never summed by adding successive push values.
type hedgeLegState struct {
	leg                 string
	token               int64
	side                string
	brokerOrderID       string
	orderTargetQty      int64 // the CURRENT resting order's own fixed quantity
	filledBeforeCurrent int64 // baseline from a prior, now-dead order on this leg
	currentOrderFilled  int64 // latest known cumulative fill of the current order
	done                bool
}

func (l *hedgeLegState) totalFilled() int64 { return l.filledBeforeCurrent + l.currentOrderFilled }

// executeHedgeTranche submits one LIMIT order per leg (CE, PE) targeting
// trancheQty in total, live-verifies each via the real-time IRIS event
// feed, and -- if a leg's order is still resting but short -- modifies
// that SAME order's price (never its quantity, never a second order while
// the first might still be alive) with the same escalating live-bid/ask
// buffer schedule (1, 2, 4 rupees) used for build-side chasing. Only when
// an order comes back genuinely terminal-but-short (CANCELLED/REJECTED,
// not just a timeout) does the leg get a fresh order, sized to the real
// remainder, since the old order is confirmed dead rather than possibly
// still resolving. Gives up and cancels a leg's resting remainder after
// the buffer schedule is exhausted. Returns each leg's real,
// broker-confirmed filled quantity and whether both legs fully reached
// trancheQty.
func (s *Service) executeHedgeTranche(
	ctx context.Context,
	executor Executor,
	modifier OrderModifier,
	canceller OrderCanceller,
	canCancel bool,
	tr StoredTrade,
	tradeUID string,
	trancheIdx int,
	ceSide, peSide string,
	ceToken, peToken int64,
	trancheQty int64,
) (filledCE, filledPE int64, complete bool) {
	const maxAttempts = 3 // buffer schedule: 1 (initial), 2 (1st modify), 4 (2nd modify)
	const perOrderTimeout = 8 * time.Second

	// ceToken/peToken are the LIVE ATM strike's tokens (resolved by the
	// caller, ManualHedgeLots), NOT tr.CEToken/tr.PEToken -- a hedge is a
	// synthetic future and must be built at the current ATM strike to
	// best approximate delta-1 exposure, which is a different strike
	// than the trade's own build strike once spot has moved. See
	// closeExtraOpenLegs for how SquareOff finds and closes a hedge leg
	// that ends up on a different token than the trade's own CE/PE.
	legs := []*hedgeLegState{
		{leg: "CE", token: ceToken, side: ceSide},
		{leg: "PE", token: peToken, side: peSide},
	}

	stamp := time.Now().UnixNano()
	group := fmt.Sprintf("HDG_%s_T%d_%d", tradeUID, trancheIdx, stamp)

	fetchQuote := func(token int64) (bid, ask float64) {
		if s.Snapshot == nil {
			return 0, 0
		}
		chain, err := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
		if err != nil {
			return 0, 0
		}
		return bidAskForToken(chain, token)
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		for _, l := range legs {
			if l.done {
				continue
			}
			remaining := trancheQty - l.totalFilled()
			if remaining <= 0 {
				l.done = true
				continue
			}

			bid, ask := fetchQuote(l.token)
			price, ok := bidAskLimitPrice(l.side, bid, ask, bufferForAttempt(attempt))
			if !ok {
				log.Printf("[HEDGE] trade=%s tranche=%d attempt=%d leg=%s: no live quote, skipping this round", tradeUID, trancheIdx, attempt, l.leg)
				continue
			}

			if l.brokerOrderID == "" {
				// Fresh order (first attempt for this leg, or restarted
				// after a prior order on this leg went terminal short)
				// targets exactly the real remainder.
				l.orderTargetQty = remaining
				l.currentOrderFilled = 0
				intentID := fmt.Sprintf("%s_%s_%d", group, l.leg, attempt)
				intent := OrderIntent{
					IntentID: intentID, TradeUID: tradeUID, Token: l.token,
					Symbol: strings.TrimSpace(tr.Symbol), ExchangeSegment: tr.ExchangeSegment,
					Side: l.side, Quantity: l.orderTargetQty, LotSize: int64(tr.LotSize),
					OrderType: "LIMIT", LimitPrice: &price, ProductType: tr.ProductType,
					LegType: l.leg, Phase: "HEDGE", HedgeGroupID: group, OrderUID: intentID,
					BrokerName: tr.BrokerName, AccountID: tr.AccountID,
				}
				brokerOrderID, _, err := s.submitOrderIntent(ctx, executor, tradeUID, intent)
				if err != nil {
					log.Printf("[HEDGE] trade=%s tranche=%d leg=%s: submit failed: %v", tradeUID, trancheIdx, l.leg, err)
					continue
				}
				l.brokerOrderID = brokerOrderID
			} else if modifier != nil {
				// Same resting order, re-priced more aggressively, SAME
				// quantity it was created with -- never a second order,
				// never a shrunk one (the exchange already knows what's
				// still pending on it).
				if err := modifier.ModifyOrderPrice(ctx, l.brokerOrderID, price, l.orderTargetQty, int(tr.LotSize)); err != nil {
					log.Printf("[HEDGE] trade=%s tranche=%d leg=%s: modify failed: %v", tradeUID, trancheIdx, l.brokerOrderID, err)
					continue
				}
			}

			waitCtx, cancel := context.WithTimeout(ctx, perOrderTimeout)
			u, err := s.OrderEvents.WaitTerminal(waitCtx, tradeUID, l.brokerOrderID, l.orderTargetQty, perOrderTimeout)
			cancel()
			if err != nil {
				log.Printf("[HEDGE] trade=%s tranche=%d leg=%s order=%s: no terminal confirmation within %s: %v -- will re-price the same order next round", tradeUID, trancheIdx, l.leg, l.brokerOrderID, perOrderTimeout, err)
				continue
			}

			l.currentOrderFilled = u.FilledQty // cumulative for THIS order -- set, never added
			switch strings.ToUpper(strings.TrimSpace(u.Status)) {
			case "FILLED":
				l.done = true
			case "CANCELLED", "CANCELED", "REJECTED":
				// This order is dead. Whatever it filled becomes a
				// permanent baseline; the next attempt (if any) gets a
				// genuinely fresh order for the real remainder, never a
				// modify on an order that no longer exists at the broker.
				l.filledBeforeCurrent += l.currentOrderFilled
				l.currentOrderFilled = 0
				l.brokerOrderID = ""
				if l.totalFilled() >= trancheQty {
					l.done = true
				}
			default:
				// Still resting (ACKED/PARTIAL_FILL/etc) -- next attempt
				// modifies this same order.
			}
		}

		if legs[0].done && legs[1].done {
			break
		}
	}

	allDone := true
	for _, l := range legs {
		if !l.done {
			allDone = false
			if canCancel && l.brokerOrderID != "" {
				if err := canceller.CancelOrder(ctx, l.brokerOrderID); err != nil {
					log.Printf("[HEDGE] trade=%s tranche=%d leg=%s order=%s: cancel of unfilled remainder failed: %v -- it may still be working at the broker", tradeUID, trancheIdx, l.leg, l.brokerOrderID, err)
				} else {
					log.Printf("[HEDGE] trade=%s tranche=%d leg=%s order=%s: cancelled unfilled remainder (filled %d/%d)", tradeUID, trancheIdx, l.leg, l.brokerOrderID, l.totalFilled(), trancheQty)
				}
			}
		}
	}

	return legs[0].totalFilled(), legs[1].totalFilled(), allDone
}

// ManualHedgeLots is ManualHedge for an explicit number of synthetic lots.
func (s *Service) ManualHedgeLots(ctx context.Context, tradeUID string, lots int) (err error) {
	if lots <= 0 {
		return fmt.Errorf("hedge lots must be positive, got %d", lots)
	}
	defer s.lockTrade(tradeUID)()
	// Every attempt is recorded on the trade (count + event) before the
	// lock is released; "no hedge needed" is not an attempt.
	ev := HedgeEvent{Time: time.Now().In(lutIST()).Format("15:04:05"), Trigger: "MANUAL", Lots: lots}
	if t, ok := ctx.Value(hedgeTriggerKey{}).(string); ok && t != "" {
		ev.Trigger, ev.Reason = "AUTO", t
	}
	attempted := false
	defer func() {
		if attempted {
			s.recordHedge(tradeUID, ev, err)
		}
	}()

	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return fmt.Errorf("trade not found")
	}
	if tr.Status != "ACTIVE" {
		return fmt.Errorf("hedge requires an ACTIVE trade, status=%s", tr.Status)
	}
	if tr.CEToken <= 0 || tr.PEToken <= 0 {
		return fmt.Errorf("cannot hedge %s: missing CE/PE tokens", tradeUID)
	}

	snap, ok := s.Store.LoadSnapshot(tradeUID)
	if !ok {
		return fmt.Errorf("snapshot not found")
	}
	ceSide, peSide, needed := hedgeSidesFromSignedDelta(snap.NetDelta)
	if !needed {
		return nil
	}
	attempted = true
	ev.CESide, ev.PESide, ev.DeltaBefore = ceSide, peSide, snap.NetDelta

	// A hedge is a synthetic future (buy CE + sell PE, or the reverse)
	// and must be built at the CURRENT live ATM strike to best
	// approximate delta-1 exposure -- NOT the trade's own build strike,
	// which drifts away from ATM as spot moves and is nowhere near ATM
	// by the time a real hedge is usually needed. Resolved fresh on
	// every call, never cached, since ATM itself moves.
	if s.Snapshot == nil {
		return fmt.Errorf("hedge refused, nothing placed: snapshot client not available to resolve live ATM strike")
	}
	chain, err := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
	if err != nil {
		return fmt.Errorf("hedge refused, nothing placed: resolve live ATM strike: %w", err)
	}
	atmRow, err := FindATMRow(*chain)
	if err != nil {
		return fmt.Errorf("hedge refused, nothing placed: resolve live ATM strike: %w", err)
	}
	hedgeCEToken, hedgePEToken := atmRow.CEToken, atmRow.PEToken
	ev.Strike = atmRow.Strike
	if hedgeCEToken <= 0 || hedgePEToken <= 0 {
		return fmt.Errorf("hedge refused, nothing placed: live ATM row missing CE/PE tokens")
	}

	lotSize := tr.LotSize
	if lotSize <= 0 {
		lotSize = GetFallbackLotSize(tr.Symbol)
	}
	if lotSize <= 0 {
		return fmt.Errorf("invalid hedge quantity for symbol %s", tr.Symbol)
	}
	// The FULL delta-neutralizing quantity -- the per-order max below is a
	// per-tranche ceiling, not a cap on this total (this used to silently
	// truncate the whole hedge to whatever fit in one order, e.g. capping
	// a required 3445 qty down to 1755 and stopping there, leaving the
	// position under-hedged with no error).
	totalQty := int64(lotSize) * int64(lots)
	ev.Qty = totalQty

	// Refuse before placing anything if the booked result is unrepresentable.
	// Only meaningful when the hedge actually lands on the trade's OWN
	// tokens (ATM coincides with the build strike) -- this checks that a
	// BUY hedge doesn't exceed what's open on tr.CEQty/tr.PEQty
	// specifically, which says nothing about a hedge on a different (live
	// ATM) token, an independent position with no such constraint.
	if hedgeCEToken == tr.CEToken && hedgePEToken == tr.PEToken {
		if _, _, err := bookHedgeFills(tr.CEQty, tr.PEQty, ceSide, peSide, totalQty, totalQty); err != nil {
			return fmt.Errorf("hedge refused, nothing placed: %w", err)
		}
	}

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return err
	}
	if _, ok := executor.(VerifiedFillsProvider); !ok {
		return fmt.Errorf("hedge refused, nothing placed: broker executor cannot verify fills")
	}
	modifier, canModify := executor.(OrderModifier)
	canceller, canCancel := executor.(OrderCanceller)
	if !canModify {
		return fmt.Errorf("hedge refused, nothing placed: broker executor cannot modify orders")
	}

	// Per-order max from the live contract, loaded at startup (e.g. NIFTY
	// 1755); one lot per order if it isn't loaded. Never hardcoded.
	maxOrderQty := s.resolveMaxOrderQty(tr.Symbol, int64(lotSize))

	maxTrancheLots := maxOrderQty / int64(lotSize)
	if maxTrancheLots <= 0 {
		return fmt.Errorf("lot size %d exceeds the hedge per-tranche quantity ceiling", lotSize)
	}

	remaining := totalQty
	var totalVerifiedCE, totalVerifiedPE int64
	var bookErr error
	trancheIdx := 0
	incomplete := false

	for remaining > 0 {
		trancheIdx++
		trancheLots := remaining / int64(lotSize)
		if trancheLots > maxTrancheLots {
			trancheLots = maxTrancheLots
		}
		trancheQty := trancheLots * int64(lotSize)
		if trancheQty <= 0 {
			break
		}

		filledCE, filledPE, complete := s.executeHedgeTranche(
			ctx, executor, modifier, canceller, canCancel,
			tr, tradeUID, trancheIdx, ceSide, peSide, hedgeCEToken, hedgePEToken, trancheQty,
		)
		totalVerifiedCE += filledCE
		totalVerifiedPE += filledPE
		ev.CEFilled, ev.PEFilled, ev.Tranches = totalVerifiedCE, totalVerifiedPE, trancheIdx

		// Book this tranche's real, broker-confirmed fill immediately --
		// a later tranche failing must never lose track of an earlier
		// tranche that genuinely executed. Only merged into
		// tr.CEQty/tr.PEQty when the hedge actually traded the trade's
		// OWN tokens (ATM happens to coincide with the build strike);
		// tr.CEQty/tr.PEQty represent quantity AT tr.CEToken/tr.PEToken
		// specifically, so merging a different token's fill into them
		// would misrepresent what's open on the ORIGINAL strike. When
		// the hedge lands on a different (live ATM) token, its real
		// position is already durably tracked in trade_legs (every order
		// here goes through submitOrderIntent -> insertOrderIntent ->
		// persistVerifiedFill -> ensureTradeLeg for its own token) and
		// closeExtraOpenLegs finds and closes it during SquareOff.
		if hedgeCEToken == tr.CEToken && hedgePEToken == tr.PEToken {
			if latest, loaded := s.Store.LoadTrade(tradeUID); loaded {
				newCE, newPE, err := bookHedgeFills(latest.CEQty, latest.PEQty, ceSide, peSide, filledCE, filledPE)
				if err != nil {
					bookErr = err
				} else {
					latest.CEQty, latest.PEQty = newCE, newPE
					latest.LastUpdateTime = time.Now()
					s.Store.UpdateTrade(latest)
					tr = latest
				}
			} else {
				bookErr = fmt.Errorf("trade vanished while booking hedge tranche %d", trancheIdx)
			}
		} else {
			log.Printf(
				"[HEDGE] trade=%s tranche=%d hedge traded live-ATM tokens ce=%d pe=%d (trade's own tokens are ce=%d pe=%d) -- tracked via trade_legs, not tr.CEQty/tr.PEQty",
				tradeUID, trancheIdx, hedgeCEToken, hedgePEToken, tr.CEToken, tr.PEToken,
			)
		}

		log.Printf(
			"HEDGE tranche result trade=%s tranche=%d ce=%s filled=%d pe=%s filled=%d of %d complete=%t book_err=%v",
			tradeUID, trancheIdx, ceSide, filledCE, peSide, filledPE, trancheQty, complete, bookErr,
		)

		// Wings follow this tranche's CONFIRMED fills right away: a short
		// that grew gets a new wing at the hedge's own ATM -/+ WingPct%;
		// a short that shrank sells that much from wings already held
		// (LIFO, never a new strike). A wing problem is logged, never
		// allowed to undo or block the hedge itself.
		if wingErr := s.adjustWings(ctx, executor, tr,
			shortChangeFromFill(ceSide, filledCE), shortChangeFromFill(peSide, filledPE),
			atmRow.Strike, atmRow.Strike, fmt.Sprintf("HEDGE tranche %d", trancheIdx)); wingErr != nil {
			log.Printf("[WINGS] ⚠ trade=%s hedge tranche %d: %v", tradeUID, trancheIdx, wingErr)
		}

		if bookErr != nil || !complete {
			incomplete = !complete
			break
		}
		remaining -= trancheQty
	}

	switch {
	case bookErr != nil:
		return fmt.Errorf("hedge orders placed but booking failed (check broker position): %w", bookErr)
	case incomplete:
		return fmt.Errorf("hedge not fully filled after tranche %d, booked to verified fills only: ce=%d pe=%d of %d total",
			trancheIdx, totalVerifiedCE, totalVerifiedPE, totalQty)
	}

	log.Printf("✅ Manual Hedge completed for Trade %s: %d tranche(s), ce=%d pe=%d", tradeUID, trancheIdx, totalVerifiedCE, totalVerifiedPE)
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

var freezeWarnAt sync.Map // symbol -> last warning time

// freezeWarnDue throttles the repeating freeze-qty warning to once per 15 min.
func freezeWarnDue(sym string) bool {
	if v, ok := freezeWarnAt.Load(sym); ok && time.Since(v.(time.Time)) < 15*time.Minute {
		return false
	}
	freezeWarnAt.Store(sym, time.Now())
	return true
}
