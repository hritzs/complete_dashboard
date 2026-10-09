package trading

// Straddle-target build -- LIVE OMS. Real GreekSoft orders, only for a run
// the user started in LIVE mode with the typed confirmation.
//
// Entry: multi-lot IOC LIMIT orders (each leg's remaining planned quantity,
// up to the freeze qty per order) at the tranche plan's own price for
// the leg (the deepest bid level its depth walk authorised, after the
// plan's VWAP test above target) -- never a price derived from earlier
// fills (sbPlanLimits). An IOC limit sell fills at the best bids at or
// above the limit, or is cancelled.
// A lot counts only once the exchange confirms it (reconciler -> order
// events); then it is pushed to PMS. Rejected -> the tranche ends and the
// run re-plans (sbMaxRejects in a row stop building; position stays
// monitored). No confirmation -> the run HALTS (nothing more is
// sent) because the position is no longer known.
// Hedge / exit: MARKET orders (must fill), freeze-qty sized, each
// confirmed before the next.
// Every run owns a trades row (status SBUILD, never resumed by the normal
// monitors) so orders and exchange fills are recorded in the DB.

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	sbTick         = 0.05
	sbLiveConfirm  = "SELL LIVE"
	sbLiveUser     = "U001"
	sbLiveBroker   = "GREEKSOFT"
	sbConfirmWait  = 15 * time.Second
	sbStatusLive   = "SBUILD"
	sbStatusClosed = "SBUILD_CLOSED"
)

var sbOrderSeq uint64

func sbLiveAccount() string {
	if a := strings.TrimSpace(os.Getenv("SBUILD_ACCOUNT_ID")); a != "" {
		return a
	}
	if a := strings.TrimSpace(os.Getenv("GREEK_ACCOUNT_ID")); a != "" {
		return a
	}
	return "147"
}

// sbLiveWindow: orders only in the broker session (09:15-15:40 IST).
func sbLiveWindow(now time.Time) bool {
	hm := now.Hour()*100 + now.Minute()
	return hm >= 915 && hm < 1540
}

// sbPendingOrder is written to the run file BEFORE an order is sent and
// removed once its outcome is confirmed, so a restart can tell a clean
// stop from an order whose fate is unknown.
type sbPendingOrder struct {
	IntentID      string  `json:"intent_id"`
	BrokerOrderID string  `json:"broker_order_id,omitempty"`
	Token         int64   `json:"token"`
	Strike        float64 `json:"strike"`
	OptionType    string  `json:"option_type"`
	Side          string  `json:"side"`
	Qty           int64   `json:"qty"`
	Role          string  `json:"role"`
	Limit         float64 `json:"limit,omitempty"`
	SentAt        string  `json:"sent_at"`
}

// sbLiveCreateTrade writes the run's trades row (orders and fills hang off it).
func (s *Service) sbLiveCreateTrade(c SBConfig, expiry string, lot int64) (StoredTrade, error) {
	if s.Store == nil {
		return StoredTrade{}, fmt.Errorf("no trade store")
	}
	now := time.Now()
	tr := StoredTrade{
		TradeUID:        fmt.Sprintf("SB-%s-%s", c.ID, now.In(lutIST()).Format("060102150405")),
		UserID:          sbLiveUser,
		BrokerName:      sbLiveBroker,
		AccountID:       sbLiveAccount(),
		Symbol:          c.Symbol,
		Expiry:          expiry,
		ProductType:     "NRML",
		ExchangeSegment: ResolveExchangeSegment(c.Symbol, ""),
		Status:          sbStatusLive,
		Mode:            "SBUILD_LIVE",
		LotSize:         int(lot),
		Lots:            int(c.Straddles / lot),
		CreatedAt:       now,
		LastUpdateTime:  now,
	}
	s.Store.SaveTrade(tr)
	if _, ok := s.Store.LoadTrade(tr.TradeUID); !ok {
		return tr, fmt.Errorf("trade %s not stored", tr.TradeUID)
	}
	return tr, nil
}

func (s *Service) sbLiveCloseTrade(tradeUID, status string) {
	if s.Store == nil || tradeUID == "" {
		return
	}
	if tr, ok := s.Store.LoadTrade(tradeUID); ok {
		tr.Status = status
		tr.LastUpdateTime = time.Now()
		if status == sbStatusClosed {
			tr.ClosedAt = time.Now()
		}
		s.Store.UpdateTrade(tr)
	}
}

// sbLiveOrderResult is one confirmed order.
type sbLiveOrderResult struct {
	Filled   int64
	AvgPrice float64
	Status   string
	Reason   string
	Unknown  bool // no terminal confirmation: position unknown
}

// sbLiveSendOne sends one order and waits for the exchange's terminal
// status. limit==nil -> MARKET (DAY); else LIMIT IOC. The pending record
// is persisted around the call. Must be called WITHOUT r.mu held.
func (s *Service) sbLiveSendOne(r *sbRunner, tradeUID, symbol string, lot int64, token int64, strike float64, opt, side string, qty int64, limit *float64, role string) sbLiveOrderResult {
	seq := atomic.AddUint64(&sbOrderSeq, 1) % 1000
	tag := fmt.Sprintf("S%s%s%05d%03d", side[:1], opt, token%100000, seq)
	intentID := BuildShortOrderUID(symbol, tag, time.Now(), 0)
	phase := map[string]string{"BUILD": "BUILD", "HEDGE": "HEDGE", "EXIT": "SQF"}[role]
	intent := OrderIntent{
		IntentID: intentID, TradeUID: tradeUID, Token: token, Symbol: symbol,
		ExchangeSegment: ResolveExchangeSegment(symbol, ""), Side: side, Quantity: qty, LotSize: lot,
		OrderType: "MARKET", ProductType: "NRML", LegType: opt, Phase: phase, OrderUID: intentID,
		BrokerName: sbLiveBroker, AccountID: sbLiveAccount(),
	}
	lim := 0.0
	if limit != nil {
		lim = *limit
		intent.OrderType, intent.LimitPrice, intent.TimeInForce, intent.ExpectedPrice = "LIMIT", limit, "IOC", lim
	}
	p := sbPendingOrder{IntentID: intentID, Token: token, Strike: strike, OptionType: opt, Side: side, Qty: qty, Role: role, Limit: lim,
		SentAt: time.Now().In(lutIST()).Format("15:04:05.000")}

	r.mu.Lock()
	r.pending = append(r.pending, p)
	r.persistLocked() // write-ahead: a crash from here on is detected at restart
	r.mu.Unlock()

	done := func(res sbLiveOrderResult) sbLiveOrderResult {
		r.mu.Lock()
		if !res.Unknown { // an unknown order stays pending (restart halts on it)
			for i := range r.pending {
				if r.pending[i].IntentID == intentID {
					r.pending = append(r.pending[:i], r.pending[i+1:]...)
					break
				}
			}
		}
		r.persistLocked()
		r.mu.Unlock()
		return res
	}

	if s.BrokerFactory == nil {
		return done(sbLiveOrderResult{Status: "REJECTED", Reason: "no broker factory"})
	}
	executor, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		return done(sbLiveOrderResult{Status: "REJECTED", Reason: "executor: " + err.Error()})
	}
	ctx, cancel := context.WithTimeout(context.Background(), sbConfirmWait+5*time.Second)
	defer cancel()
	brokerOrderID, _, err := s.submitOrderIntent(ctx, executor, tradeUID, intent)
	if err != nil {
		// A submit error can come AFTER the broker took the order (timeout,
		// no order id back): its fate is unknown, so the run halts.
		log.Printf("[SBUILD-LIVE] order submit FAILED %s %s %s qty=%d: %v", side, opt, intentID, qty, err)
		return done(sbLiveOrderResult{Status: "ERROR", Reason: "submit: " + err.Error(), Unknown: true})
	}
	r.mu.Lock()
	for i := range r.pending {
		if r.pending[i].IntentID == intentID {
			r.pending[i].BrokerOrderID = brokerOrderID
		}
	}
	r.persistLocked()
	r.mu.Unlock()

	if s.OrderEvents == nil {
		return done(sbLiveOrderResult{Unknown: true, Reason: "no order-event registry to confirm the fill"})
	}
	upd, werr := s.OrderEvents.WaitTerminal(ctx, tradeUID, brokerOrderID, qty, sbConfirmWait)
	if werr != nil {
		// No push (e.g. the reconciler's feed was reconnecting): ask the
		// broker's trade book. A fill found there is booked; nothing found
		// stays unknown (halt) -- never assumed unfilled.
		if got, px, _ := s.sbTradeBookFill(executor, brokerOrderID); got > 0 {
			log.Printf("[SBUILD-LIVE] broker order %s: no push confirmation in %s; trade book shows %d filled @%.2f -- booked from the trade book", brokerOrderID, sbConfirmWait, got, px)
			if got > qty {
				got = qty
			}
			s.persistWingFill(intentID, brokerOrderID, "FILLED", got, qty-got, px)
			return done(sbLiveOrderResult{Filled: got, AvgPrice: px, Status: "FILLED", Reason: "confirmed from the broker trade book"})
		}
		return done(sbLiveOrderResult{Unknown: true, Reason: fmt.Sprintf("broker order %s: no terminal confirmation in %s and not in the broker trade book: %v", brokerOrderID, sbConfirmWait, werr)})
	}
	got := upd.FilledQty
	if got > qty {
		got = qty
	}
	px := upd.AvgFillPrice
	if got > 0 && px <= 0 {
		px = lim // IOC limit sell fills at the limit or better: conservative
		log.Printf("[SBUILD-LIVE] ⚠ broker order %s filled %d with no price; booked at limit %.2f", brokerOrderID, got, lim)
	}
	s.persistWingFill(intentID, brokerOrderID, upd.Status, got, qty-got, px)
	log.Printf("[SBUILD-LIVE] trade=%s %s %s %s token=%d qty=%d limit=%.2f -> filled %d @%.2f status=%s %s broker_order_id=%s",
		tradeUID, role, side, opt, token, qty, lim, got, px, upd.Status, upd.ReasonText, brokerOrderID)
	return done(sbLiveOrderResult{Filled: got, AvgPrice: px, Status: strings.ToUpper(upd.Status), Reason: upd.ReasonText})
}

// sbLiveHalt stops everything on the run: nothing more is sent. Caller holds r.mu.
func (s *Service) sbLiveHaltLocked(r *sbRunner, why string) {
	r.phase = "HALTED"
	r.haltReason = why
	r.event("HALT", "LIVE run HALTED -- nothing more will be sent: %s. Check the broker order book / positions for trade %s, then Exit now or Stop (acknowledge).", why, r.tradeUID)
	r.persistLocked()
}

// sbPlanLimits: each lot's IOC limit is the tranche plan's own price for
// its leg -- the deepest bid level the plan's depth walk (shown in the UI)
// authorised, so the lot fills at the best bids at or above it. No cushion
// from earlier fills ever lowers it (2026-10-08 12:44: a cushion priced CE
// at 116.05 vs LTP 145 -> broker RMS reject). A leg whose fresh best bid
// has fallen below the plan's price stops the tranche: re-plan, never sell
// lower than planned.
func sbPlanLimits(legs []string, plan DepthEntryPlan, bestBid map[string]float64) (map[string]float64, string) {
	limits := map[string]float64{}
	for _, leg := range legs {
		lim := plan.CEWorst
		if leg == "PE" {
			lim = plan.PEWorst
		}
		if lim <= 0 {
			return limits, fmt.Sprintf("no planned %s price", leg)
		}
		if bestBid[leg] > 0 && bestBid[leg] < lim-1e-9 {
			return limits, fmt.Sprintf("%s best bid %.2f fell below the planned price %.2f", leg, bestBid[leg], lim)
		}
		limits[leg] = lim
	}
	return limits, ""
}

// sbMaxRejects: consecutive rejected build orders (no fill between) that
// stop building -- a rejection that will not clear (margin) must not loop.
const sbMaxRejects = 3

// sbRejectLocked: a rejected build order ends only its tranche; the run
// re-plans after the tranche gap and keeps building. Caller holds r.mu.
func (r *sbRunner) sbRejectLocked(what, reason string) {
	r.rejects++
	r.lastReject = &SBReject{Time: time.Now().In(lutIST()).Format("15:04:05"), What: what, Reason: reason, Streak: r.rejects, Max: sbMaxRejects, Stopped: r.rejects >= sbMaxRejects}
	if r.rejects < sbMaxRejects {
		r.event("ORDER", "LIVE %s REJECTED (%s) -- rejection %d/%d in a row: re-planning, building continues", what, reason, r.rejects, sbMaxRejects)
		return
	}
	if r.phase == "BUILDING" {
		r.phase = "COMPLETE"
	}
	r.event("ORDER", "LIVE %s REJECTED (%s) -- %d rejections in a row: building stopped; position stays monitored", what, reason, r.rejects)
}

// sbFreshBids re-reads the best bids and L5 limits of one strike (each
// falls back to the old value).
func (s *Service) sbFreshBids(symbol, expiry string, strike float64, old, oldL5 map[string]float64) (map[string]float64, map[string]float64) {
	out := map[string]float64{"CE": old["CE"], "PE": old["PE"]}
	l5 := map[string]float64{"CE": oldL5["CE"], "PE": oldL5["PE"]}
	if s.Snapshot == nil {
		return out, l5
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c, err := s.Snapshot.GetOptionChain(ctx, symbol, expiry)
	if err != nil || c == nil {
		return out, l5
	}
	for _, row := range c.Chain {
		if math.Abs(row.Strike-strike) > 1e-6 {
			continue
		}
		bb, low := sbRowBids(row), sbRowL5(row)
		for _, leg := range []string{"CE", "PE"} {
			if bb[leg] > 0 && low[leg] > 0 {
				out[leg], l5[leg] = bb[leg], low[leg]
			}
		}
	}
	return out, l5
}

func sbRowBids(row OptionChainRow) map[string]float64 {
	bb := map[string]float64{"CE": row.CEBid, "PE": row.PEBid}
	if row.CEDepth != nil && len(row.CEDepth.Bids) > 0 && row.CEDepth.Bids[0].Price > 0 {
		bb["CE"] = row.CEDepth.Bids[0].Price
	}
	if row.PEDepth != nil && len(row.PEDepth.Bids) > 0 && row.PEDepth.Bids[0].Price > 0 {
		bb["PE"] = row.PEDepth.Bids[0].Price
	}
	return bb
}

// sbL5Levels: build orders are limited at the lowest of the top 5 bids.
const sbL5Levels = 5

// sbRowL5 returns each leg's L5 limit: the LOWEST of the top 5 bid levels
// (fewer levels shown -> the deepest one; no depth -> the best bid).
func sbRowL5(row OptionChainRow) map[string]float64 {
	out := sbRowBids(row)
	for leg, d := range map[string]*DepthBook{"CE": row.CEDepth, "PE": row.PEDepth} {
		if d == nil {
			continue
		}
		for i := 0; i < len(d.Bids) && i < sbL5Levels; i++ {
			if p := d.Bids[i].Price; p > 0 && p < out[leg] {
				out[leg] = p
			}
		}
	}
	return out
}

// sbCompleteRetries: how many times a leg whose partner already filled is
// re-sent at the fresh best bid before the tranche gives up (the cycle's
// completion step keeps trying after that).
const sbCompleteRetries = 3

// sbExecuteLiveAsync sends an authorized tranche in the background (the
// runner is busy meanwhile): CE and PE of each pair go out TOGETHER, then
// the extra delta lots. A pair leg that doesn't fill while its partner did
// is completed at once at the best bid -- the position is never left
// one-sided waiting for the target. Caller holds r.mu.
func (s *Service) sbExecuteLiveAsync(r *sbRunner, plan DepthEntryPlan, atm OptionChainRow) {
	r.busy = true
	lot := int64(r.lotSize)
	tranche, tradeUID, symbol, expiry := r.tranches, r.tradeUID, r.cfg.Symbol, r.expiry
	bestBid, l5 := sbRowBids(atm), sbRowL5(atm)
	token := map[string]int64{"CE": atm.CEToken, "PE": atm.PEToken}
	planQty := map[string]int64{"CE": plan.CEQty, "PE": plan.PEQty}
	maxQ := s.resolveMaxOrderQty(symbol, lot) / lot * lot // freeze qty, whole lots
	if maxQ < lot {
		maxQ = lot
	}
	go func() {
		filled := map[string]int64{}
		stopWhy := ""
		// send sends each leg's order (qty at its limit) concurrently and
		// returns what filled per leg. Caller must NOT hold r.mu.
		type order struct {
			qty int64
			lim float64
		}
		send := func(orders map[string]order, role string) map[string]int64 {
			type out struct {
				leg string
				res sbLiveOrderResult
			}
			ch := make(chan out, len(orders))
			for leg, o := range orders {
				go func(leg string, o order) {
					l := o.lim
					ch <- out{leg, s.sbLiveSendOne(r, tradeUID, symbol, lot, token[leg], atm.Strike, leg, "SELL", o.qty, &l, "BUILD")}
				}(leg, o)
			}
			// Collect every result WITHOUT r.mu: each sbLiveSendOne takes
			// r.mu itself (write-ahead pending record). Holding it here
			// deadlocked the run (2026-10-06 14:50:58 -- nothing was sent).
			results := make([]out, 0, len(orders))
			for range orders {
				results = append(results, <-ch)
			}
			got := map[string]int64{}
			r.mu.Lock()
			defer r.mu.Unlock()
			for _, o := range results {
				if o.res.Filled > 0 {
					r.pms.ApplyFill(PMSFill{Token: token[o.leg], Strike: atm.Strike, OptionType: o.leg, Side: "SELL", Qty: o.res.Filled, Price: o.res.AvgPrice, Role: "BUILD", Tranche: tranche})
					filled[o.leg] += o.res.Filled
					ceA, peA := r.pms.BuildAverages()
					r.event("FILL", "LIVE %s%s SELL %d/%d @%.2f (IOC limit %.2f) -- build avg CE %.2f + PE %.2f = %.2f vs target %.2f",
						role, o.leg, o.res.Filled, orders[o.leg].qty, o.res.AvgPrice, orders[o.leg].lim, ceA, peA, ceA+peA, r.cfg.TargetStraddle)
				}
				switch {
				case o.res.Unknown:
					s.sbLiveHaltLocked(r, fmt.Sprintf("%s order unconfirmed: %s", o.leg, o.res.Reason))
					stopWhy = "halted"
				case o.res.Status == "REJECTED":
					r.sbRejectLocked(o.leg+" order", o.res.Reason)
					stopWhy = "rejected"
				case o.res.Filled > 0:
					r.rejects = 0
				}
				got[o.leg] = o.res.Filled
			}
			r.persistLocked()
			return got
		}
		canSend := func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			switch {
			case stopWhy != "":
			case r.phase != "BUILDING":
				stopWhy = "run is " + r.phase
			case !sbLiveWindow(time.Now().In(lutIST())):
				stopWhy = "outside the broker session"
			}
			return stopWhy == ""
		}
		left := func(leg string) int64 { return planQty[leg] - filled[leg] }

		// Multi-lot shots: each leg's whole remaining planned quantity (up
		// to the freeze qty per order) at the plan's own price -- the IOC
		// takes every contract resting at or above it, the rest cancels.
		for shot := 0; shot < 200 && (left("CE") >= lot || left("PE") >= lot); shot++ {
			if !canSend() {
				break
			}
			if shot > 0 {
				bestBid, l5 = s.sbFreshBids(symbol, expiry, atm.Strike, bestBid, l5)
			}
			var legs []string
			for _, leg := range []string{"CE", "PE"} {
				if left(leg) >= lot {
					legs = append(legs, leg)
				}
			}
			limits, why := sbPlanLimits(legs, plan, bestBid)
			if stopWhy = why; stopWhy != "" {
				break
			}
			orders := map[string]order{}
			for _, leg := range legs {
				orders[leg] = order{qty: min(left(leg)/lot*lot, maxQ), lim: limits[leg]}
			}
			got := send(orders, "")
			if stopWhy != "" {
				break
			}
			// Keep the tranche's CE:PE ratio: a leg that filled less than
			// its share of what its partner filled is completed now at the
			// fresh L5 bid (not held to the target), multi-lot.
			if plan.CEQty > 0 && plan.PEQty > 0 {
				for k := 0; k < sbCompleteRetries && canSend(); k++ {
					needCE := filled["PE"] * plan.CEQty / plan.PEQty / lot * lot
					needPE := filled["CE"] * plan.PEQty / plan.CEQty / lot * lot
					miss, deficit := "", int64(0)
					if d := needCE - filled["CE"]; d >= lot {
						miss, deficit = "CE", d
					} else if d := needPE - filled["PE"]; d >= lot {
						miss, deficit = "PE", d
					}
					if miss == "" {
						break
					}
					bestBid, l5 = s.sbFreshBids(symbol, expiry, atm.Strike, bestBid, l5)
					if l5[miss] <= 0 {
						continue
					}
					q := min(deficit, maxQ)
					r.mu.Lock()
					r.event("COMPLETE", "LIVE %s behind its partner by %d -- completing %d at the L5 bid %.2f (best %.2f, attempt %d/%d)", miss, deficit, q, l5[miss], bestBid[miss], k+1, sbCompleteRetries)
					r.mu.Unlock()
					send(map[string]order{miss: {qty: q, lim: l5[miss]}}, "COMPLETE ")
				}
				if stopWhy == "" {
					if d := filled["PE"]*plan.CEQty/plan.PEQty/lot*lot - filled["CE"]; d >= lot {
						stopWhy = fmt.Sprintf("CE still %d behind its partner after %d completion attempts", d, sbCompleteRetries)
					} else if d := filled["CE"]*plan.PEQty/plan.CEQty/lot*lot - filled["PE"]; d >= lot {
						stopWhy = fmt.Sprintf("PE still %d behind its partner after %d completion attempts", d, sbCompleteRetries)
					}
				}
			}
			if stopWhy != "" {
				break
			}
			// Nothing at all filled, or the book did not hold the rest at
			// the plan's price: re-plan from the fresh book.
			if got["CE"]+got["PE"] == 0 {
				stopWhy = "nothing filled at the plan's prices (IOC cancelled)"
				break
			}
			partial := false
			for _, leg := range legs {
				if got[leg] < orders[leg].qty {
					partial = true
				}
			}
			if partial && (left("CE") >= lot || left("PE") >= lot) {
				stopWhy = "the book did not hold the whole level at the plan's price -- re-plan"
				break
			}
		}
		r.mu.Lock()
		r.busy = false
		r.lastTranche = time.Now()
		msg := ""
		if stopWhy != "" {
			msg = " -- stopped: " + stopWhy + "; re-plan from PMS"
		}
		r.event("TRANCHE", "LIVE tranche %d done: sold CE %d + PE %d at ATM %.0f (planned %d + %d)%s",
			tranche, filled["CE"], filled["PE"], atm.Strike, plan.CEQty, plan.PEQty, msg)
		r.persistLocked()
		r.mu.Unlock()
	}()
}

// sbCompletionLeg: when the build is lopsided -- one leg ahead, the
// position carrying delta that one lot of the lagging leg would cut -- it
// returns that leg. Completion sells it at the best bid without the
// target check: an unfinished straddle's open delta is the bigger risk.
func sbCompletionLeg(view PMSView, remaining, lot int64, dCE, dPE float64) (string, bool) {
	if lot <= 0 || remaining < lot || view.BuildCE == view.BuildPE {
		return "", false
	}
	leg, legDelta := "CE", -math.Abs(dCE)*float64(lot) // short CE: delta down
	if view.BuildPE < view.BuildCE {
		leg, legDelta = "PE", math.Abs(dPE)*float64(lot) // short PE: delta up
	}
	if legDelta == 0 {
		return "", false
	}
	before := math.Abs(view.NetDelta)
	after := math.Abs(view.NetDelta + legDelta)
	if before < 0.5*math.Abs(legDelta) || after >= before {
		return "", false
	}
	return leg, true
}

// sbCompleteLocked sells one lot of the lagging leg at the ATM best bid.
// Caller holds r.mu.
func (s *Service) sbCompleteLocked(r *sbRunner, leg string, atm OptionChainRow, view PMSView) {
	lot := int64(r.lotSize)
	bid := sbRowBids(atm)[leg]
	if bid <= 0 {
		r.sbReason(fmt.Sprintf("completion: no %s bid at %.0f", leg, atm.Strike))
		return
	}
	tok := atm.CEToken
	if leg == "PE" {
		tok = atm.PEToken
	}
	r.tranches++
	r.lastTranche = time.Now()
	r.addStrike(atm.Strike)
	r.event("COMPLETE", "build lopsided (CE %d / PE %d, net delta %+.2f): completing 1 lot %s @%.0f, limit the L5 bid %.2f (best %.2f) -- not held to the target",
		view.BuildCE, view.BuildPE, view.NetDelta, leg, atm.Strike, sbRowL5(atm)[leg], bid)
	if !r.isLive {
		r.pms.ApplyFill(PMSFill{Token: tok, Strike: atm.Strike, OptionType: leg, Side: "SELL", Qty: lot, Price: bid, Role: "BUILD", Tranche: r.tranches})
		r.dirty = true
		return
	}
	r.busy = true
	tranche, tradeUID, symbol := r.tranches, r.tradeUID, r.cfg.Symbol
	l5 := sbRowL5(atm)[leg] // the L5 bid is the limit (fills at the best bids above it)
	go func() {
		lim := l5
		res := s.sbLiveSendOne(r, tradeUID, symbol, lot, tok, atm.Strike, leg, "SELL", lot, &lim, "BUILD")
		r.mu.Lock()
		defer r.mu.Unlock()
		if res.Filled > 0 {
			r.rejects = 0
			r.pms.ApplyFill(PMSFill{Token: tok, Strike: atm.Strike, OptionType: leg, Side: "SELL", Qty: res.Filled, Price: res.AvgPrice, Role: "BUILD", Tranche: tranche})
			ceA, peA := r.pms.BuildAverages()
			r.event("FILL", "LIVE COMPLETE %s SELL %d @%.2f (IOC limit %.2f) -- build avg CE %.2f + PE %.2f = %.2f vs target %.2f",
				leg, res.Filled, res.AvgPrice, lim, ceA, peA, ceA+peA, r.cfg.TargetStraddle)
		}
		switch {
		case res.Unknown:
			s.sbLiveHaltLocked(r, fmt.Sprintf("%s completion order unconfirmed: %s", leg, res.Reason))
		case res.Status == "REJECTED":
			r.sbRejectLocked(leg+" completion", res.Reason)
		case res.Filled < lot:
			r.event("COMPLETE", "%s completion lot not filled at %.2f (IOC cancelled) -- retrying next cycle", leg, lim)
		}
		r.busy = false
		r.lastTranche = time.Now()
		r.persistLocked()
	}()
}

// sbMarketOrder is one hedge / exit order (MARKET).
type sbMarketOrder struct {
	Token      int64
	Strike     float64
	OptionType string
	Side       string
	Qty        int64
}

// sbLiveMarketAsync sends MARKET orders (hedge or exit) in the background,
// split at the freeze qty, each confirmed before the next. onDone runs
// with r.mu held after the last order. Caller holds r.mu.
func (s *Service) sbLiveMarketAsync(r *sbRunner, role string, orders []sbMarketOrder, onDone func(ok bool)) {
	r.busy = true
	lot := int64(r.lotSize)
	tradeUID, symbol := r.tradeUID, r.cfg.Symbol
	go func() {
		ok := true
		maxPer := s.resolveMaxOrderQty(symbol, lot)
		if maxPer <= 0 {
			maxPer = lot
		}
		maxPer = (maxPer / lot) * lot
		if maxPer <= 0 {
			maxPer = lot
		}
	outer:
		for _, o := range orders {
			for remaining := o.Qty; remaining > 0; {
				q := remaining
				if q > maxPer {
					q = maxPer
				}
				res := s.sbLiveSendOne(r, tradeUID, symbol, lot, o.Token, o.Strike, o.OptionType, o.Side, q, nil, role)
				r.mu.Lock()
				if res.Filled > 0 {
					r.pms.ApplyFill(PMSFill{Token: o.Token, Strike: o.Strike, OptionType: o.OptionType, Side: o.Side, Qty: res.Filled, Price: res.AvgPrice, Role: role})
					r.event("FILL", "LIVE %s %s %s %.0f %d @%.2f (MARKET)", role, o.Side, o.OptionType, o.Strike, res.Filled, res.AvgPrice)
				}
				if res.Unknown || res.Filled < q {
					why := res.Reason
					if why == "" {
						why = fmt.Sprintf("status %s, filled %d of %d", res.Status, res.Filled, q)
					}
					s.sbLiveHaltLocked(r, fmt.Sprintf("%s %s %s %.0f MARKET order not fully confirmed: %s", role, o.Side, o.OptionType, o.Strike, why))
					r.mu.Unlock()
					ok = false
					break outer
				}
				r.persistLocked()
				r.mu.Unlock()
				remaining -= q
			}
		}
		r.mu.Lock()
		r.busy = false
		if onDone != nil {
			onDone(ok)
		}
		r.persistLocked()
		r.mu.Unlock()
	}()
}

// sbFlattenOrders lists the orders that close every open PMS leg.
func sbFlattenOrders(view PMSView) []sbMarketOrder {
	var out []sbMarketOrder
	for _, l := range view.Legs {
		if l.Qty == 0 {
			continue
		}
		side, qty := "BUY", -l.Qty
		if l.Qty > 0 {
			side, qty = "SELL", l.Qty
		}
		out = append(out, sbMarketOrder{Token: l.Token, Strike: l.Strike, OptionType: l.OptionType, Side: side, Qty: qty})
	}
	// Buy back shorts first (reduces margin before any sell).
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Side == "BUY" && out[i].Side == "SELL" {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// sbExitLocked flattens the run (exit trigger or "Exit now"). Shadow:
// simulated at the marks. Live: MARKET orders, EXITED once confirmed.
// Caller holds r.mu.
func (s *Service) sbExitLocked(r *sbRunner, chain *OptionChainSnapshot, why string) {
	view := r.pms.View(chain)
	if !r.isLive {
		r.phase = "EXITED"
		r.event("EXIT", "%s -- build stopped for good; flattening what PMS holds (all strikes)", why)
		for _, l := range view.Legs {
			if l.Qty == 0 {
				continue
			}
			side, qty := "BUY", -l.Qty
			if l.Qty > 0 {
				side, qty = "SELL", l.Qty
			}
			r.pms.ApplyFill(PMSFill{Token: l.Token, Strike: l.Strike, OptionType: l.OptionType, Side: side, Qty: qty, Price: l.Mark, Role: "EXIT"})
		}
		r.dirty = true
		final := r.pms.View(chain)
		r.event("EXIT", "flat=%t realized ₹%.0f", final.Flat, final.PnL)
		return
	}
	orders := sbFlattenOrders(view)
	if len(orders) == 0 {
		r.phase = "EXITED"
		r.event("EXIT", "%s -- LIVE position already flat", why)
		s.sbLiveCloseTrade(r.tradeUID, sbStatusClosed)
		r.persistLocked()
		return
	}
	r.phase = "EXITING"
	r.event("EXIT", "%s -- LIVE: building stopped for good; buying back / selling out %d leg(s) with MARKET orders", why, len(orders))
	r.persistLocked()
	s.sbLiveMarketAsync(r, "EXIT", orders, func(ok bool) {
		if !ok {
			return // halted inside
		}
		final := r.pms.View(r.chain)
		r.phase = "EXITED"
		r.event("EXIT", "LIVE exit confirmed: flat=%t realized ₹%.0f", final.Flat, final.PnL)
		if final.Flat {
			s.sbLiveCloseTrade(r.tradeUID, sbStatusClosed)
		}
	})
}

// sbTradeBookFill looks one broker order up in the broker's OWN order book
// (REST -- not the reconciler DB, which misses a push lost while its feed
// reconnects). A few tries: the book can lag the exchange by a moment.
// checked=false: the book could not be read (fate still unknown).
func (s *Service) sbTradeBookFill(executor Executor, brokerOrderID string) (qty int64, px float64, checked bool) {
	if brokerOrderID == "" {
		return 0, 0, false
	}
	read := func(ctx context.Context) ([]BrokerFill, error) {
		if p, ok := executor.(BrokerOrderBookFillsProvider); ok {
			return p.GetBrokerOrderBookFills(ctx)
		}
		if p, ok := executor.(VerifiedFillsProvider); ok {
			return p.GetVerifiedFills(ctx)
		}
		return nil, fmt.Errorf("executor cannot read the order book")
	}
	for try := 0; try < 3; try++ {
		if try > 0 {
			time.Sleep(time.Second)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		fills, err := read(ctx)
		cancel()
		if err != nil {
			log.Printf("[SBUILD-LIVE] order book lookup for %s failed: %v", brokerOrderID, err)
			continue
		}
		checked = true
		var value float64
		qty = 0
		for _, f := range fills {
			if strings.TrimSpace(f.BrokerOrderID) == brokerOrderID && f.FilledQty > 0 {
				qty += f.FilledQty
				value += float64(f.FilledQty) * f.AveragePrice
			}
		}
		if qty > 0 {
			return qty, value / float64(qty), true
		}
	}
	return 0, 0, checked
}

// sbResolvePendingLocked settles orders whose outcome was never confirmed
// (r.pending) from the broker's order book: a fill found there is booked in
// PMS (and the DB), so an exit flattens the REAL position. ok=false: the
// book could not be read -- the caller must not assume anything.
// Caller holds r.mu (the lookups are short).
func (s *Service) sbResolvePendingLocked(r *sbRunner) (bool, string) {
	if len(r.pending) == 0 {
		return true, ""
	}
	if s.BrokerFactory == nil {
		return false, "no broker factory"
	}
	executor, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		return false, err.Error()
	}
	var left []sbPendingOrder
	var notes []string
	for _, p := range r.pending {
		if p.BrokerOrderID == "" {
			left = append(left, p)
			notes = append(notes, fmt.Sprintf("%s %s %d: never got a broker order id", p.Side, p.OptionType, p.Qty))
			continue
		}
		qty, px, checked := s.sbTradeBookFill(executor, p.BrokerOrderID)
		if !checked {
			left = append(left, p)
			notes = append(notes, fmt.Sprintf("order %s: broker order book unreadable", p.BrokerOrderID))
			continue
		}
		if qty > p.Qty {
			qty = p.Qty
		}
		if qty > 0 {
			r.pms.ApplyFill(PMSFill{Token: p.Token, Strike: p.Strike, OptionType: p.OptionType, Side: p.Side, Qty: qty, Price: px, Role: p.Role, Tranche: r.tranches})
			s.persistWingFill(p.IntentID, p.BrokerOrderID, "FILLED", qty, p.Qty-qty, px)
		}
		r.event("RESUME", "unconfirmed order %s (%s %s %d) settled from the broker order book: filled %d @%.2f", p.BrokerOrderID, p.Side, p.OptionType, p.Qty, qty, px)
	}
	r.pending = left
	r.persistLocked()
	if len(left) > 0 {
		return false, strings.Join(notes, "; ")
	}
	return true, ""
}

// sbGatewayUp / sbLiveWarmup: a LIVE run sends nothing in the first
// seconds after the gateway starts (the reconciler's order feed and the
// order-event stream reconnect after a restart).
var sbGatewayUp = time.Now()

const sbLiveWarmup = 20 * time.Second
