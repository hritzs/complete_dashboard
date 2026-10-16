package trading

// Complete-position exit for a normal (timed / TP / manual) square-off, after
// the reference engine (refernce_py_code/trading/square_off.py):
//
//  1. Pre-exit neutral: |net delta| > 1 lot -> one synthetic hedge at the live
//     ATM to delta-neutral first (preExitNeutralise, before the trade lock).
//  2. One inventory: every non-wing leg the fills say is open -- the straddle,
//     every hedge leg, earlier remnants -- net per token.
//  3. One plan: the build's chunker -- 7 rounds, each leg's running total
//     round(total*c/7), ceil(trade lots / 100) lots per order.
//  4. Each round: every order sent concurrently as an IOC limit at ask+buffer
//     (BUY) / bid-buffer (SELL); unfilled remainders re-sent at 2x, then 3x
//     the buffer; whatever is still open goes MARKET, so the exit completes.
//  5. Wings follow the net short after every round; SquareOff's tail closes
//     any leftover leg and then the wings.
//
// SL / PORTFOLIO / EMERGENCY keep the aggressive max-lots MARKET path.

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// exitIOCAttempts: IOC limit attempts per order before the MARKET fallback.
const exitIOCAttempts = 3

// aggressiveExitReason: exits that keep the fastest max-lots MARKET path.
func aggressiveExitReason(reason string) bool {
	return reason == "SL" || reason == "PORTFOLIO" || reason == "EMERGENCY"
}

// exitLimitBuffer: the reference's base limit buffer (2 for NIFTY-type
// indices, 6 for SENSEX / BANKEX), multiplied by the attempt number.
func exitLimitBuffer(symbol string) float64 {
	s := strings.ToUpper(symbol)
	if strings.Contains(s, "SENSEX") || strings.Contains(s, "BANKEX") {
		return 6
	}
	return 2
}

// preExitNeutralise hedges the trade to delta-neutral before a normal exit
// when |net delta| is more than one lot (reference: PRE_SQF_NEUTRAL). Runs
// before SquareOff takes the trade lock (ManualHedgeLots takes it). A failure
// is logged and the exit goes on, as in the reference.
func (s *Service) preExitNeutralise(tradeUID, reason string) {
	if aggressiveExitReason(reason) {
		return
	}
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok || tr.Status != "ACTIVE" || tr.LotSize <= 0 {
		return
	}
	snap, ok := s.Store.LoadSnapshot(tradeUID)
	if !ok {
		log.Printf("[EXIT] trade=%s pre-exit neutral skipped: no snapshot", tradeUID)
		return
	}
	lot := float64(tr.LotSize)
	if math.Abs(snap.NetDelta) <= lot {
		log.Printf("[EXIT] trade=%s pre-exit neutral: delta %.1f within 1 lot (%d) -- none needed", tradeUID, snap.NetDelta, tr.LotSize)
		return
	}
	lots := int(math.Round(math.Abs(snap.NetDelta) / lot))
	ctx := context.WithValue(context.Background(), hedgeTriggerKey{}, fmt.Sprintf("PRE_EXIT neutral before %s exit (delta %.1f)", reason, snap.NetDelta))
	log.Printf("[EXIT] trade=%s pre-exit neutral: delta %.1f -> %d lot(s) synthetic hedge at live ATM before the %s exit", tradeUID, snap.NetDelta, lots, reason)
	if err := s.ManualHedgeLots(ctx, tradeUID, lots); err != nil {
		log.Printf("[EXIT] ⚠ trade=%s pre-exit neutral failed: %v -- exiting anyway", tradeUID, err)
	}
}

// exitLeg: one leg of the complete-exit inventory.
type exitLeg struct {
	mtmLeg
	side string // closing side: BUY a short, SELL a long
	qty  int64  // open contracts (abs)
}

// completeExitInventory: every non-wing leg the fills say is open.
func (s *Service) completeExitInventory(tradeUID string) ([]exitLeg, error) {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	legs, err := pg.TradeLegCash(ctx, tradeUID)
	if err != nil {
		return nil, err
	}
	var out []exitLeg
	for _, l := range legs {
		if l.NetShort == 0 || l.Token <= 0 {
			continue
		}
		side, q := "BUY", l.NetShort
		if l.NetShort < 0 {
			side, q = "SELL", -l.NetShort
		}
		out = append(out, exitLeg{mtmLeg: l, side: side, qty: q})
	}
	return out, nil
}

// exitOrderResult: what one planned order got, across its attempts.
type exitOrderResult struct {
	filled  int64
	unknown string // broker order / reason whose outcome is not known
}

// completeExitRounds closes every leg in inventory in 7 build-like rounds
// and returns the trade's own CE / PE still open and any orders whose outcome
// is unknown (nothing more is sent once one exists).
func (s *Service) completeExitRounds(tradeUID string, tr StoredTrade, executor Executor, inv []exitLeg, reason string) (remCE, remPE int64, unconfirmed []string, err error) {
	lot := int64(tr.LotSize)
	if lot <= 0 {
		return 0, 0, nil, fmt.Errorf("invalid lot size %d", tr.LotSize)
	}
	byToken := map[int64]*exitLeg{}
	legs := make([]LegData, 0, len(inv))
	maxLegLots := 0
	for i := range inv {
		l := &inv[i]
		if l.qty%lot != 0 {
			return 0, 0, nil, fmt.Errorf("leg %s %d qty %d is not a lot multiple of %d", l.Leg, l.Token, l.qty, lot)
		}
		byToken[l.Token] = l
		lots := int(l.qty / lot)
		maxLegLots = max(maxLegLots, lots)
		legs = append(legs, LegData{Token: l.Token, Symbol: tr.Symbol, OptionType: l.Leg, Action: l.side,
			TotalLots: lots, LotSize: int(lot), ExchangeSegment: tr.ExchangeSegment})
		log.Printf("[EXIT] trade=%s inventory %s %s %.0f: %s %d", tradeUID, l.Leg, map[bool]string{true: "short", false: "long"}[l.NetShort > 0], l.Strike, l.side, l.qty)
	}
	maxOrderQty := int(s.resolveMaxOrderQty(tr.Symbol, lot))
	rounds, err := GenerateChunkedOrders(fmt.Sprintf("X%s", tradeUID), legs, exitSizeLots(tr, maxLegLots), maxOrderQty, 0)
	if err != nil {
		return 0, 0, nil, err
	}
	log.Printf("[EXIT] trade=%s %s exit: %d leg(s) in %d round(s), %d lot(s) per order, IOC limit ±%.0f x1/x2/x3 then MARKET",
		tradeUID, reason, len(inv), len(rounds), max(1, min(int(math.Ceil(float64(exitSizeLots(tr, maxLegLots))/100)), maxOrderQty/int(lot))), exitLimitBuffer(tr.Symbol))

	closed := map[int64]int64{}
	var cumCE, cumPE int64 // net short change per side, for the wings
	var wingsF wingFollower

	for ri, round := range rounds {
		pending := make([]int64, len(round)) // qty still to close per planned order
		for i, o := range round {
			pending[i] = int64(o.Quantity)
		}
		roundFilled := make([]int64, len(round))

		for attempt := 1; attempt <= exitIOCAttempts+1; attempt++ {
			idx := []int{}
			for i := range round {
				if pending[i] > 0 {
					idx = append(idx, i)
				}
			}
			if len(idx) == 0 {
				break
			}
			market := attempt > exitIOCAttempts
			var chain *OptionChainSnapshot
			if !market && s.Snapshot != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				chain, _ = s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
				cancel()
			}
			buf := exitLimitBuffer(tr.Symbol) * float64(attempt)

			results := make([]exitOrderResult, len(round))
			var wg sync.WaitGroup
			for _, i := range idx {
				l := byToken[round[i].Token]
				if l == nil {
					pending[i] = 0
					continue
				}
				wg.Add(1)
				go func(i int, l *exitLeg, qty int64) {
					defer wg.Done()
					results[i] = s.exitSendOne(executor, tr, l, qty, chain, buf, market)
				}(i, l, pending[i])
			}
			wg.Wait()

			for _, i := range idx {
				r := results[i]
				pending[i] -= r.filled
				roundFilled[i] += r.filled
				if r.unknown != "" {
					unconfirmed = append(unconfirmed, r.unknown)
					pending[i] = 0 // never re-sent while its outcome is unknown
				}
			}
			if len(unconfirmed) > 0 {
				break
			}
		}

		for i, o := range round {
			if roundFilled[i] == 0 {
				continue
			}
			closed[o.Token] += roundFilled[i]
			l := byToken[o.Token]
			if l.Leg == "CE" {
				cumCE += shortChangeFromFill(l.side, roundFilled[i])
			} else {
				cumPE += shortChangeFromFill(l.side, roundFilled[i])
			}
		}
		left := int64(0)
		for _, l := range inv {
			left += l.qty - closed[l.Token]
		}
		log.Printf("[EXIT] trade=%s round %d/%d done: %d contract(s) still open across %d leg(s)%s",
			tradeUID, ri+1, len(rounds), left, len(inv), map[bool]string{true: fmt.Sprintf(" -- %d order(s) UNCONFIRMED, stopping", len(unconfirmed)), false: ""}[len(unconfirmed) > 0])
		if len(unconfirmed) > 0 {
			break
		}
		if left > 0 {
			wingsF.follow(s, executor, tr, cumCE, cumPE, "SQF-"+reason)
		}
	}

	for _, l := range inv {
		rem := l.qty - closed[l.Token]
		switch l.Token {
		case tr.CEToken:
			remCE = rem
		case tr.PEToken:
			remPE = rem
		}
	}
	return remCE, remPE, unconfirmed, nil
}

// exitSendOne sends one planned order: an IOC limit at bid/ask -+ buf (LTP
// -+ buf without a quote), or MARKET when market is set or no price exists.
func (s *Service) exitSendOne(executor Executor, tr StoredTrade, l *exitLeg, qty int64, chain *OptionChainSnapshot, buf float64, market bool) exitOrderResult {
	if !market {
		bid, ask := bidAskForToken(chain, l.Token)
		limit, ok := bidAskLimitPrice(l.side, bid, ask, buf)
		if !ok {
			if ltp := livePriceForToken(chain, l.Token); ltp > 0 {
				if l.side == "BUY" {
					limit, ok = math.Round((ltp+buf)/0.05)*0.05, true
				} else {
					limit, ok = math.Max(0.05, math.Round((ltp-buf)/0.05)*0.05), true
				}
			}
		}
		if ok {
			got, _, status, unknown := s.mtmSendIOC(executor, tr, l.mtmLeg, l.side, qty, limit)
			if unknown {
				return exitOrderResult{unknown: fmt.Sprintf("%s %d %s %d @%.2f: %s", l.Leg, l.Token, l.side, qty, limit, status)}
			}
			return exitOrderResult{filled: got}
		}
		log.Printf("[EXIT] trade=%s %s %d: no bid/ask/LTP -- sending MARKET", tr.TradeUID, l.Leg, l.Token)
	}
	return s.exitSendMarket(executor, tr, l, qty)
}

// exitSendMarket: the last resort for a remainder the IOC limits left.
func (s *Service) exitSendMarket(executor Executor, tr StoredTrade, l *exitLeg, qty int64) exitOrderResult {
	seq := atomic.AddUint64(&mtmOrderSeq, 1) % 1000 // unique across concurrent sends
	intentID := BuildShortOrderUID(tr.Symbol, fmt.Sprintf("XM%s%05d%03d", l.Leg, l.Token%100000, seq), time.Now(), 0)
	intent := OrderIntent{
		IntentID: intentID, TradeUID: tr.TradeUID, Token: l.Token, Symbol: tr.Symbol,
		ExchangeSegment: tr.ExchangeSegment, Side: l.side, Quantity: qty, LotSize: int64(tr.LotSize),
		OrderType: "MARKET", ProductType: tr.ProductType, LegType: l.Leg, Phase: "SQF", OrderUID: intentID,
		BrokerName: tr.BrokerName, AccountID: tr.AccountID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), sbConfirmWait+5*time.Second)
	defer cancel()
	brokerOrderID, _, err := s.submitOrderIntent(ctx, executor, tr.TradeUID, intent)
	if err != nil {
		// Not sent (refused / failed): nothing is in flight, nothing filled.
		log.Printf("[EXIT] ⚠ trade=%s %s %d MARKET %s %d not sent: %v", tr.TradeUID, l.Leg, l.Token, l.side, qty, err)
		return exitOrderResult{}
	}
	upd, werr := s.OrderEvents.WaitTerminal(ctx, tr.TradeUID, brokerOrderID, qty, sbConfirmWait)
	if werr != nil {
		if got, tpx, _ := s.sbTradeBookFill(executor, brokerOrderID); got > 0 {
			got = min(got, qty)
			s.persistWingFill(intentID, brokerOrderID, "FILLED", got, qty-got, tpx)
			return exitOrderResult{filled: got}
		}
		return exitOrderResult{unknown: fmt.Sprintf("order %s (%s %d MARKET %s %d): %v", brokerOrderID, l.Leg, l.Token, l.side, qty, werr)}
	}
	got := min(upd.FilledQty, qty)
	s.persistWingFill(intentID, brokerOrderID, upd.Status, got, qty-got, upd.AvgFillPrice)
	return exitOrderResult{filled: got}
}

// sbLiveExitRounds is the Straddle Build's live exit in the same shape:
// every leg the PMS holds in 7 build-like rounds (ceil(build lots / 100) per
// order), each round's orders concurrently as IOC limits at bid/ask -+ 2 x1 /
// x2 / x3, then MARKET for what is left. Every fill goes into the PMS; an
// order whose outcome is unknown halts the run (as the MARKET path does).
func (s *Service) sbLiveExitRounds(r *sbRunner, orders []sbMarketOrder, onDone func(ok bool)) {
	r.busy = true
	lot := int64(r.lotSize)
	tradeUID, symbol, expiry := r.tradeUID, r.cfg.Symbol, r.cfg.Expiry
	buildLots := 0
	if lot > 0 {
		buildLots = int(r.cfg.Straddles / lot)
	}
	go func() {
		ok := s.sbLiveExitRun(r, orders, lot, tradeUID, symbol, expiry, buildLots)
		r.mu.Lock()
		r.busy = false
		if onDone != nil {
			onDone(ok)
		}
		r.persistLocked()
		r.mu.Unlock()
	}()
}

func (s *Service) sbLiveExitRun(r *sbRunner, orders []sbMarketOrder, lot int64, tradeUID, symbol, expiry string, buildLots int) bool {
	if lot <= 0 {
		return false
	}
	byToken := map[int64]sbMarketOrder{}
	legs := make([]LegData, 0, len(orders))
	maxLegLots := 0
	for _, o := range orders {
		if o.Qty%lot != 0 {
			r.mu.Lock()
			s.sbLiveHaltLocked(r, fmt.Sprintf("EXIT %s %.0f qty %d is not a lot multiple of %d", o.OptionType, o.Strike, o.Qty, lot))
			r.mu.Unlock()
			return false
		}
		byToken[o.Token] = o
		lots := int(o.Qty / lot)
		maxLegLots = max(maxLegLots, lots)
		legs = append(legs, LegData{Token: o.Token, Symbol: symbol, OptionType: o.OptionType, Action: o.Side, TotalLots: lots, LotSize: int(lot)})
	}
	if buildLots <= 0 {
		buildLots = maxLegLots
	}
	rounds, err := GenerateChunkedOrders(fmt.Sprintf("X%s", tradeUID), legs, buildLots, int(s.resolveMaxOrderQty(symbol, lot)), 0)
	if err != nil {
		r.mu.Lock()
		s.sbLiveHaltLocked(r, "EXIT plan: "+err.Error())
		r.mu.Unlock()
		return false
	}
	r.mu.Lock()
	r.event("EXIT", "LIVE exit: %d leg(s) in %d round(s), IOC limit -+%.0f x1/x2/x3 then MARKET", len(orders), len(rounds), exitLimitBuffer(symbol))
	r.mu.Unlock()

	for ri, round := range rounds {
		pending := make([]int64, len(round))
		for i, o := range round {
			pending[i] = int64(o.Quantity)
		}
		for attempt := 1; attempt <= exitIOCAttempts+1; attempt++ {
			market := attempt > exitIOCAttempts
			var chain *OptionChainSnapshot
			if !market {
				if s.Snapshot != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					chain, _ = s.Snapshot.GetOptionChain(ctx, symbol, expiry)
					cancel()
				}
				if chain == nil {
					r.mu.Lock()
					chain = r.chain
					r.mu.Unlock()
				}
			}
			buf := exitLimitBuffer(symbol) * float64(attempt)
			results := make([]sbLiveOrderResult, len(round))
			var wg sync.WaitGroup
			sent := false
			for i := range round {
				if pending[i] <= 0 {
					continue
				}
				o := byToken[round[i].Token]
				var limit *float64
				if !market {
					bid, ask := bidAskForToken(chain, o.Token)
					if p, ok := bidAskLimitPrice(o.Side, bid, ask, buf); ok {
						limit = &p
					}
				}
				sent = true
				wg.Add(1)
				go func(i int, o sbMarketOrder, q int64, limit *float64) {
					defer wg.Done()
					results[i] = s.sbLiveSendOne(r, tradeUID, symbol, lot, o.Token, o.Strike, o.OptionType, o.Side, q, limit, "EXIT")
				}(i, o, pending[i], limit)
			}
			if !sent {
				break
			}
			wg.Wait()

			r.mu.Lock()
			for i := range round {
				res := results[i]
				if res.Filled > 0 {
					o := byToken[round[i].Token]
					r.pms.ApplyFill(PMSFill{Token: o.Token, Strike: o.Strike, OptionType: o.OptionType, Side: o.Side, Qty: res.Filled, Price: res.AvgPrice, Role: "EXIT"})
					r.event("FILL", "LIVE EXIT %s %s %.0f %d @%.2f (%s)", o.Side, o.OptionType, o.Strike, res.Filled, res.AvgPrice, map[bool]string{true: "MARKET", false: "IOC"}[market])
					pending[i] -= res.Filled
				}
				if res.Unknown || (market && pending[i] > 0 && res.Status != "") {
					why := res.Reason
					if why == "" {
						why = fmt.Sprintf("status %s, filled %d", res.Status, res.Filled)
					}
					s.sbLiveHaltLocked(r, fmt.Sprintf("EXIT round %d order not fully confirmed: %s", ri+1, why))
					r.persistLocked()
					r.mu.Unlock()
					return false
				}
			}
			r.persistLocked()
			r.mu.Unlock()
		}
	}
	return true
}
