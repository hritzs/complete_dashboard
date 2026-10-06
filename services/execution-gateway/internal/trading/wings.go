package trading

// Wings: long far-OTM options bought purely for margin (RM).
//
// Rule: after every action, net CE and net PE across ALL strikes -- shorts,
// hedge legs and wings together -- are zero. Wings are adjusted only by the
// quantity an action actually filled (broker-confirmed), never "topped up to a
// target" computed from the DB: a restart, a retried tick or a lagging
// reconciler can therefore never buy the same wings twice.
//
//   - Net short on a type GROWS by q  -> BUY q wings of that type at
//     (action strike -/+ strike*WingPct/100), rounded to the chain's strike step.
//   - Net short SHRINKS by q          -> SELL q from wings ALREADY held,
//     most recently bought first (LIFO); never a new strike, never more than held.
//   - Full square-off                 -> sell every wing held.
//
// Every wing order carries phase "WING" (buy and sell), persisted in orders
// before it is sent; its confirmed fill is written to fills/trade_legs right
// away (not only when the reconciler catches up). Holdings are always read
// back from the DB, so they survive a restart.

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

var wingOrderSeq uint64

const (
	wingPhase  = "WING"
	maxWingPct = 20.0
)

// wingHolding is one wing strike currently held (Qty > 0 = long).
type wingHolding struct {
	Token      int64
	Exchange   string
	OptionType string
	Strike     float64
	Qty        int64
	LastBuyAt  time.Time
}

// wingSell is one piece of a LIFO wing reduction.
type wingSell struct {
	Holding wingHolding
	Qty     int64
}

// strikeStepFromChain is the smallest positive gap between listed strikes
// (e.g. 50 for NIFTY, 100 for BANKNIFTY) -- read from the live chain, never
// hardcoded.
func strikeStepFromChain(chain OptionChainSnapshot) float64 {
	strikes := make([]float64, 0, len(chain.Chain))
	for _, r := range chain.Chain {
		if r.Strike > 0 {
			strikes = append(strikes, r.Strike)
		}
	}
	sort.Float64s(strikes)
	step := 0.0
	for i := 1; i < len(strikes); i++ {
		d := strikes[i] - strikes[i-1]
		if d > 0.001 && (step == 0 || d < step) {
			step = d
		}
	}
	return step
}

// wingTargetStrike: PE -> base - base*pct/100, CE -> base + base*pct/100,
// MROUNDed to step. 23000 @2% step 50 -> PE 22550, CE 23450. Always at
// least one step OTM of base.
func wingTargetStrike(base, pct, step float64, optionType string) float64 {
	if step <= 0 {
		step = 1
	}
	dist := base * pct / 100
	var target float64
	if optionType == "CE" {
		target = math.Round((base+dist)/step) * step
		if target <= base {
			target = base + step
		}
	} else {
		target = math.Round((base-dist)/step) * step
		if target >= base {
			target = base - step
		}
	}
	return target
}

// findWingRow returns the chain row for the wing strike. If the exact strike
// isn't listed it takes the nearest one further OTM. A strike whose token is
// in avoid (a non-wing leg of this trade: the straddle or a hedge) is
// skipped one step further OTM, so a wing never nets against a short/hedge
// on the same token.
func findWingRow(chain OptionChainSnapshot, target float64, optionType string, avoid map[int64]bool) (*OptionChainRow, error) {
	rows := make([]*OptionChainRow, 0, len(chain.Chain))
	for i := range chain.Chain {
		r := &chain.Chain[i]
		tok := r.PEToken
		if optionType == "CE" {
			tok = r.CEToken
		}
		if tok > 0 && r.Strike > 0 {
			rows = append(rows, r)
		}
	}
	if optionType == "CE" {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Strike < rows[j].Strike }) // OTM = upward
	} else {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Strike > rows[j].Strike }) // OTM = downward
	}
	for _, r := range rows {
		further := r.Strike >= target-0.001
		if optionType != "CE" {
			further = r.Strike <= target+0.001
		}
		if !further {
			continue
		}
		tok := r.PEToken
		if optionType == "CE" {
			tok = r.CEToken
		}
		if avoid[tok] {
			continue
		}
		return r, nil
	}
	return nil, fmt.Errorf("no %s strike at or beyond %.0f in the live chain", optionType, target)
}

// allocateWingSells picks which held wings to sell for qty of optionType:
// most recently bought first, capped at what is held. shortfall > 0 means
// fewer wings are held than asked -- never sold beyond holdings.
func allocateWingSells(holdings []wingHolding, optionType string, qty int64) (plan []wingSell, shortfall int64) {
	var mine []wingHolding
	for _, h := range holdings {
		if h.OptionType == optionType && h.Qty > 0 {
			mine = append(mine, h)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if !mine[i].LastBuyAt.Equal(mine[j].LastBuyAt) {
			return mine[i].LastBuyAt.After(mine[j].LastBuyAt)
		}
		// Same time: the one further OTM was the latest added (it's cheaper
		// to hold), deterministic either way.
		if optionType == "CE" {
			return mine[i].Strike > mine[j].Strike
		}
		return mine[i].Strike < mine[j].Strike
	})
	remaining := qty
	for _, h := range mine {
		if remaining <= 0 {
			break
		}
		q := h.Qty
		if q > remaining {
			q = remaining
		}
		plan = append(plan, wingSell{Holding: h, Qty: q})
		remaining -= q
	}
	return plan, remaining
}

// shortChangeFromFill: how much the net SHORT of that type grows when an
// order of side fills qty. SELL -> +qty, BUY -> -qty.
func shortChangeFromFill(side string, qty int64) int64 {
	if strings.EqualFold(side, "SELL") {
		return qty
	}
	return -qty
}

// wingStore is the durable part: holdings are read back from orders/fills.
type wingStore interface {
	LoadWingHoldings(tradeUID string) ([]wingHolding, error)
}

func (s *Service) loadWingHoldings(tradeUID string) ([]wingHolding, error) {
	ws, ok := s.Store.(wingStore)
	if !ok {
		return nil, nil
	}
	return ws.LoadWingHoldings(tradeUID)
}

// wingAvoidTokens: every token of this trade that isn't a wing -- the
// straddle's own tokens plus any open non-wing leg (hedges).
func (s *Service) wingAvoidTokens(tradeUID string, tr StoredTrade) map[int64]bool {
	avoid := map[int64]bool{tr.CEToken: true, tr.PEToken: true}
	if legs, err := s.extraOpenLegs(tradeUID, tr); err == nil {
		for _, l := range legs {
			if l.Qty-l.WingQty != 0 {
				avoid[l.Token] = true
			}
		}
	}
	return avoid
}

// adjustWings applies one action's net-short changes to the wings.
// shortChangeCE/PE > 0: buy that many wings at baseCE/basePE -/+ WingPct%.
// < 0: sell that many from held wings, LIFO. Disabled when WingPct <= 0.
// Returns an error if any wing quantity could not be confirmed -- the
// caller logs it; the short-side action itself is never undone for it.
func (s *Service) adjustWings(ctx context.Context, executor Executor, tr StoredTrade, shortChangeCE, shortChangePE int64, baseCE, basePE float64, reason string) error {
	if tr.Config.WingPct <= 0 || (shortChangeCE == 0 && shortChangePE == 0) {
		return nil
	}
	var errs []string
	for _, side := range []struct {
		opt    string
		change int64
		base   float64
	}{{"CE", shortChangeCE, baseCE}, {"PE", shortChangePE, basePE}} {
		var err error
		switch {
		case side.change > 0:
			_, err = s.buyWings(ctx, executor, tr, side.opt, side.change, side.base, reason)
		case side.change < 0:
			_, err = s.sellWings(ctx, executor, tr, side.opt, -side.change, reason)
		}
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("wings (%s): %s", reason, strings.Join(errs, "; "))
	}
	return nil
}

// resolveWingRow finds the wing strike/row for optionType off base.
func (s *Service) resolveWingRow(ctx context.Context, tr StoredTrade, optionType string, base float64) (*OptionChainRow, float64, error) {
	if s.Snapshot == nil {
		return nil, 0, fmt.Errorf("no snapshot client to resolve wing strike")
	}
	chain, err := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
	if err != nil {
		return nil, 0, fmt.Errorf("option chain: %w", err)
	}
	step := strikeStepFromChain(*chain)
	target := wingTargetStrike(base, tr.Config.WingPct, step, optionType)
	row, err := findWingRow(*chain, target, optionType, s.wingAvoidTokens(tr.TradeUID, tr))
	if err != nil {
		return nil, target, err
	}
	return row, target, nil
}

// buyWings buys qty of optionType wings at base -/+ WingPct%.
func (s *Service) buyWings(ctx context.Context, executor Executor, tr StoredTrade, optionType string, qty int64, base float64, reason string) (int64, error) {
	row, target, err := s.resolveWingRow(ctx, tr, optionType, base)
	if err != nil {
		log.Printf("[WINGS] ⚠ trade=%s %s BUY %d NOT placed: %v", tr.TradeUID, optionType, qty, err)
		return 0, err
	}
	token := row.PEToken
	if optionType == "CE" {
		token = row.CEToken
	}
	log.Printf("[WINGS] trade=%s %s base=%.0f %.2f%% -> target %.0f, using strike %.0f token=%d: BUY %d (%s)",
		tr.TradeUID, optionType, base, tr.Config.WingPct, target, row.Strike, token, qty, reason)
	filled, err := s.placeWingOrders(ctx, executor, tr, token, optionType, "BUY", qty)
	if err != nil {
		log.Printf("[WINGS] ⚠ trade=%s %s BUY filled %d of %d at %.0f: %v", tr.TradeUID, optionType, filled, qty, row.Strike, err)
	}
	return filled, err
}

// sellWings sells qty of optionType from wings already held, LIFO.
func (s *Service) sellWings(ctx context.Context, executor Executor, tr StoredTrade, optionType string, qty int64, reason string) (int64, error) {
	holdings, err := s.loadWingHoldings(tr.TradeUID)
	if err != nil {
		return 0, fmt.Errorf("load wing holdings: %w", err)
	}
	plan, shortfall := allocateWingSells(holdings, optionType, qty)
	if shortfall > 0 {
		log.Printf("[WINGS] ⚠ trade=%s %s SELL %d asked but only %d held -- selling only what is held (%s)",
			tr.TradeUID, optionType, qty, qty-shortfall, reason)
	}
	var total int64
	for _, p := range plan {
		log.Printf("[WINGS] trade=%s %s SELL %d from held strike %.0f token=%d (held %d, LIFO) (%s)",
			tr.TradeUID, optionType, p.Qty, p.Holding.Strike, p.Holding.Token, p.Holding.Qty, reason)
		filled, err := s.placeWingOrders(ctx, executor, tr, p.Holding.Token, optionType, "SELL", p.Qty)
		total += filled
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// closeAllWings sells every wing held (full square-off, net target 0).
func (s *Service) closeAllWings(ctx context.Context, tr StoredTrade, reason string) error {
	holdings, err := s.loadWingHoldings(tr.TradeUID)
	if err != nil {
		return fmt.Errorf("load wing holdings: %w", err)
	}
	if len(holdings) == 0 {
		return nil
	}
	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return fmt.Errorf("get executor: %w", err)
	}
	for _, h := range holdings {
		if h.Qty <= 0 {
			// A net-short wing token should never exist; don't touch it
			// automatically, just make it loud.
			log.Printf("[WINGS] ⚠ trade=%s wing token=%d strike=%.0f has qty %d (not long) -- left for manual check", tr.TradeUID, h.Token, h.Strike, h.Qty)
			continue
		}
		log.Printf("[WINGS] trade=%s closing wing %s strike=%.0f token=%d SELL %d (%s)", tr.TradeUID, h.OptionType, h.Strike, h.Token, h.Qty, reason)
		if _, err := s.placeWingOrders(ctx, executor, tr, h.Token, h.OptionType, "SELL", h.Qty); err != nil {
			return err
		}
	}
	return nil
}

// pendingWing is a wing order sent but not yet confirmed.
type pendingWing struct {
	IntentID      string
	BrokerOrderID string
	OptionType    string
	Token         int64
	Qty           int64
}

// submitWingOrder sends ONE wing MARKET order (qty must already be within
// the per-order max). The intent is persisted (phase WING) before sending.
func (s *Service) submitWingOrder(ctx context.Context, executor Executor, tr StoredTrade, token int64, optionType, side string, qty int64) (pendingWing, error) {
	lotSize := int64(tr.LotSize)
	if lotSize <= 0 {
		lotSize = 1
	}
	// intent_id is UNIQUE in orders: the sequence keeps two wing orders on
	// the same token in the same second (e.g. two hedge tranches) apart.
	seq := atomic.AddUint64(&wingOrderSeq, 1) % 1000
	intentID := BuildShortOrderUID(tr.Symbol, fmt.Sprintf("W%s%s%05d%03d", side[:1], optionType, token%100000, seq), time.Now(), 0)
	intent := OrderIntent{
		IntentID:        intentID,
		TradeUID:        tr.TradeUID,
		Token:           token,
		Symbol:          tr.Symbol,
		ExchangeSegment: ResolveExchangeSegment(tr.Symbol, tr.ExchangeSegment),
		Side:            side,
		Quantity:        qty,
		LotSize:         lotSize,
		OrderType:       "MARKET",
		ProductType:     tr.ProductType,
		LegType:         optionType,
		Phase:           wingPhase,
		OrderUID:        intentID,
		BrokerName:      tr.BrokerName,
		AccountID:       tr.AccountID,
	}
	brokerOrderID, _, err := s.submitOrderIntent(ctx, executor, tr.TradeUID, intent)
	if err != nil {
		return pendingWing{}, fmt.Errorf("wing %s %s token=%d qty=%d: %w", side, optionType, token, qty, err)
	}
	return pendingWing{IntentID: intentID, BrokerOrderID: brokerOrderID, OptionType: optionType, Token: token, Qty: qty}, nil
}

// confirmWingOrder waits for the broker's terminal status and writes the
// confirmed fill to the DB. Returns the confirmed filled quantity.
func (s *Service) confirmWingOrder(ctx context.Context, tr StoredTrade, p pendingWing) (int64, error) {
	if s.OrderEvents == nil {
		return 0, fmt.Errorf("wing order %s: no order-event registry to confirm it", p.BrokerOrderID)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	upd, err := s.OrderEvents.WaitTerminal(waitCtx, tr.TradeUID, p.BrokerOrderID, p.Qty, 15*time.Second)
	cancel()
	if err != nil {
		return 0, fmt.Errorf("wing order %s token=%d: no terminal confirmation: %w", p.BrokerOrderID, p.Token, err)
	}
	got := upd.FilledQty
	if got > p.Qty {
		got = p.Qty
	}
	s.persistWingFill(p.IntentID, p.BrokerOrderID, upd.Status, got, p.Qty-got, upd.AvgFillPrice)
	log.Printf("[WINGS] trade=%s %s token=%d qty=%d filled=%d status=%s broker_order_id=%s",
		tr.TradeUID, p.OptionType, p.Token, p.Qty, got, upd.Status, p.BrokerOrderID)
	if got < p.Qty {
		return got, fmt.Errorf("wing order %s token=%d filled %d of %d (status %s %s)", p.BrokerOrderID, p.Token, got, p.Qty, upd.Status, upd.ReasonText)
	}
	return got, nil
}

// placeWingOrders sends MARKET orders split at the live per-order max,
// confirming each before the next. Returns the confirmed filled quantity.
func (s *Service) placeWingOrders(ctx context.Context, executor Executor, tr StoredTrade, token int64, optionType, side string, qty int64) (int64, error) {
	lotSize := int64(tr.LotSize)
	if lotSize <= 0 {
		lotSize = 1
	}
	maxPer := s.resolveMaxOrderQty(tr.Symbol, lotSize)
	if maxPer <= 0 {
		maxPer = lotSize
	}
	var filled int64
	for remaining := qty; remaining > 0; {
		pieceQty := remaining
		if pieceQty > maxPer {
			pieceQty = maxPer
		}
		p, err := s.submitWingOrder(ctx, executor, tr, token, optionType, side, pieceQty)
		if err != nil {
			return filled, err
		}
		got, err := s.confirmWingOrder(ctx, tr, p)
		filled += got
		if err != nil {
			return filled, err
		}
		remaining -= pieceQty
	}
	return filled, nil
}

// buildWingPlan holds the wing token per side for a straddle build, off
// each short leg's own strike.
type buildWingPlan struct {
	token map[string]int64
	base  map[string]float64
}

func (s *Service) planBuildWings(ctx context.Context, tr StoredTrade) buildWingPlan {
	plan := buildWingPlan{token: map[string]int64{}, base: map[string]float64{"CE": tr.Strike, "PE": tr.Strike}}
	if tr.Config.WingPct <= 0 || s.Snapshot == nil {
		return plan
	}
	if chain, err := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry); err == nil {
		for _, r := range chain.Chain {
			if r.CEToken == tr.CEToken && r.Strike > 0 {
				plan.base["CE"] = r.Strike
			}
			if r.PEToken == tr.PEToken && r.Strike > 0 {
				plan.base["PE"] = r.Strike
			}
		}
	}
	for _, opt := range []string{"CE", "PE"} {
		row, target, err := s.resolveWingRow(ctx, tr, opt, plan.base[opt])
		if err != nil {
			log.Printf("[WINGS] ⚠ trade=%s build %s wing (target %.0f) not resolved, will retry after the build: %v", tr.TradeUID, opt, target, err)
			continue
		}
		if opt == "CE" {
			plan.token[opt] = row.CEToken
		} else {
			plan.token[opt] = row.PEToken
		}
		log.Printf("[WINGS] trade=%s build %s wing: base %.0f %.2f%% -> target %.0f, strike %.0f token=%d",
			tr.TradeUID, opt, plan.base[opt], tr.Config.WingPct, target, row.Strike, plan.token[opt])
	}
	return plan
}

// settleBuildWings confirms the wing orders sent alongside the build, then
// trues each side up to the VERIFIED short quantity: buys what's missing
// (a wing order failed), sells the excess (a short didn't fully fill).
// trueUp=false (build fills unverified) only confirms and records.
func (s *Service) settleBuildWings(ctx context.Context, executor Executor, tr StoredTrade, plan buildWingPlan, pending []pendingWing, verifiedCE, verifiedPE int64, trueUp bool) {
	if tr.Config.WingPct <= 0 {
		return
	}
	filled := map[string]int64{}
	for _, p := range pending {
		got, err := s.confirmWingOrder(ctx, tr, p)
		filled[p.OptionType] += got
		if err != nil {
			log.Printf("[WINGS] ⚠ trade=%s build wing: %v", tr.TradeUID, err)
		}
	}
	if !trueUp {
		log.Printf("[WINGS] ⚠ trade=%s build fills unverified -- wings bought CE=%d PE=%d, NOT trued up; check net CE/PE", tr.TradeUID, filled["CE"], filled["PE"])
		return
	}
	for _, side := range []struct {
		opt  string
		want int64
	}{{"CE", verifiedCE}, {"PE", verifiedPE}} {
		diff := side.want - filled[side.opt]
		switch {
		case diff > 0:
			_, _ = s.buyWings(ctx, executor, tr, side.opt, diff, plan.base[side.opt], "BUILD true-up")
		case diff < 0:
			_, _ = s.sellWings(ctx, executor, tr, side.opt, -diff, "BUILD true-up")
		}
		log.Printf("[WINGS] trade=%s build %s: short verified %d, wings bought with build %d, true-up %+d",
			tr.TradeUID, side.opt, side.want, filled[side.opt], diff)
	}
}

// persistWingFill writes the confirmed fill straight away so the next
// action (and a restart) sees the wing as held -- the dedupe in
// perOrderFillsSQL keeps a later reconciler copy of the same fill from
// double counting.
func (s *Service) persistWingFill(intentID, brokerOrderID, status string, filled, pending int64, price float64) {
	updater, ok := s.Store.(interface {
		MarkOrderExecution(intentID, brokerOrderID, status string, filledQty, pendingQty int64, fillPrice float64, rawResponse string)
	})
	if !ok {
		return
	}
	st := strings.ToUpper(strings.TrimSpace(status))
	if filled > 0 && pending == 0 {
		st = "FILLED"
	}
	updater.MarkOrderExecution(intentID, brokerOrderID, st, filled, pending, price, "{}")
}
