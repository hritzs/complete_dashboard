package trading

// MTM exit for LIVE trades -- its own exit type (status CLOSED_MTM),
// separate from SL / TP / exit time.
//
// Every trade carries it; the level defaults to infinity (nil = never
// fires). Once a level is set (Modify, in rupees / points per straddle /
// bps of spot), the monitor checks on EVERY tick:
//
//	executable MTM = cash from all non-wing fills (sold - bought)
//	               - each open leg x the price it would close at NOW,
//	                 walking the L1-L5 depth (a short buys back up the
//	                 asks, a long sells down the bids)
//
// and when that is >= the level it closes the trade LOT BY LOT: each lot
// is an IOC limit order priced so that, even filled at its limit, the
// trade's executable MTM stays >= the level (and never above the price
// that clears the lot from the visible book). An IOC that does not fill
// is cancelled by the exchange -- nothing rests; the next lot is re-priced
// on a fresh book. If the book no longer allows the level, it stops and
// the remaining position stays ACTIVE (monitored as before) until the
// level is reachable again. A fill whose fate is unknown stops everything
// (RECONCILIATION_REQUIRED) -- never assumed unfilled.
//
// Wings are margin-only and outside the MTM (as in the trade's PnL); they
// are closed last, once every non-wing leg is flat.

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync/atomic"
	"time"
)

const (
	mtmTick        = 0.05
	mtmMaxMisses   = 5                      // IOCs with no fill in a row before waiting for the next tick
	mtmBookRefresh = 150 * time.Millisecond // wait for a fresh chain publish between lots
)

var mtmOrderSeq uint64

// mtmLeg is one non-wing instrument of a trade from its fills.
type mtmLeg struct {
	Leg       string // CE / PE
	Token     int64
	Strike    float64
	NetShort  int64   // sold - bought (>0 short, <0 long)
	Cash      float64 // sold value - bought value (exact, unrounded)
	BuildSold int64   // contracts sold by the build (the original position)
}

// TradeLegCash returns a trade's non-wing legs with exact cash flows.
func (s *PostgresBackedStore) TradeLegCash(ctx context.Context, tradeUID string) ([]mtmLeg, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, executionsSelect+` WHERE o.trade_uid = $1 ORDER BY o.created_at, o.id`, tradeUID)
	if err != nil {
		return nil, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()
	byTrade, err := scanExecutions(rows)
	if err != nil {
		return nil, err
	}
	return mtmLegsFromExecutions(byTrade[tradeUID]), nil
}

func mtmLegsFromExecutions(execs []OrderExecution) []mtmLeg {
	idx := map[string]int{}
	var out []mtmLeg
	for _, e := range execs {
		if e.FilledQty <= 0 || e.AvgPrice <= 0 || e.Leg == "" || isWingExecution(e) {
			continue
		}
		k := legKey(e.Leg, e.token)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, mtmLeg{Leg: strings.ToUpper(e.Leg), Token: e.token, Strike: e.Strike})
		}
		v := float64(e.FilledQty) * e.AvgPrice
		switch strings.ToUpper(e.Side) {
		case "SELL":
			out[i].NetShort += e.FilledQty
			out[i].Cash += v
			kind := e.Kind
			if kind == "" {
				kind = classifyExecutionKind(e.phase, e.intentID)
			}
			if kind == "ENTRY" {
				out[i].BuildSold += e.FilledQty
			}
		case "BUY":
			out[i].NetShort -= e.FilledQty
			out[i].Cash -= v
		}
	}
	return out
}

// mtmExitFloor converts the trade's level to rupees.
func mtmExitFloor(cfg MonitorConfig, spot float64, straddleQty int64) (float64, bool) {
	if cfg.MTMExitLevel == nil {
		return 0, false // infinity: never fires
	}
	lvl := *cfg.MTMExitLevel
	switch cfg.MTMExitUnit {
	case "pts":
		return lvl * float64(straddleQty), true
	case "bps":
		return spot * lvl / 10000 * float64(straddleQty), true
	}
	return lvl, true
}

// mtmBook is the side of a leg's book that closes it, best level first.
func mtmBook(row *OptionChainRow, leg string, closingBuy bool) []DepthLevel {
	var d *DepthBook
	l1 := row.PEBid
	if closingBuy {
		l1 = row.PEAsk
	}
	if leg == "CE" {
		d = row.CEDepth
		l1 = row.CEBid
		if closingBuy {
			l1 = row.CEAsk
		}
	} else {
		d = row.PEDepth
	}
	if d != nil {
		lv := d.Bids
		if closingBuy {
			lv = d.Asks
		}
		var out []DepthLevel
		for _, x := range lv {
			if x.Price > 0 && x.Qty > 0 {
				out = append(out, x)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if l1 > 0 {
		// No depth published for this strike: the best price, quantity
		// unknown (assumed enough -- the IOC limit still caps the price).
		return []DepthLevel{{Price: l1, Qty: math.MaxInt32}}
	}
	return nil
}

// mtmWalk fills qty from the book: average price and the worst level used.
// ok=false: the visible book cannot fill qty.
func mtmWalk(book []DepthLevel, qty int64) (vwap, worst float64, ok bool) {
	left, value := qty, 0.0
	for _, l := range book {
		take := min(left, l.Qty)
		value += float64(take) * l.Price
		left -= take
		worst = l.Price
		if left == 0 {
			return value / float64(qty), worst, true
		}
	}
	if qty > left && len(book) > 0 {
		// Thin book: value the rest at the worst visible level.
		value += float64(left) * worst
		return value / float64(qty), worst, false
	}
	return 0, 0, false
}

// mtmExecMTM is the trade's executable MTM: realized cash + every open leg
// closed now through the book. ok=false if an open leg cannot be priced.
func mtmExecMTM(legs []mtmLeg, rowOf func(mtmLeg) *OptionChainRow) (float64, bool) {
	total := 0.0
	for _, l := range legs {
		total += l.Cash
		if l.NetShort == 0 {
			continue
		}
		row := rowOf(l)
		if row == nil {
			return 0, false
		}
		q := l.NetShort
		buy := q > 0
		if q < 0 {
			q = -q
		}
		book := mtmBook(row, l.Leg, buy)
		vwap, _, _ := mtmWalk(book, q)
		if vwap <= 0 {
			return 0, false
		}
		if buy {
			total -= float64(q) * vwap
		} else {
			total += float64(q) * vwap
		}
	}
	return total, true
}

// mtmLotLimit prices one closing IOC for qty of leg l so the trade stays
// >= floor even if it fills entirely at the limit. execMTM already values
// this lot at its book price; the slack above the floor may be spent on a
// worse fill, never more. An IOC fills at the resting prices, so a limit
// above them costs nothing -- it only lets the lot fill if the book ticks
// away; it is also kept within max(0.50, 1%) of the level the lot needs so
// the exchange's price protection never rejects it. ok=false: no price can
// both fill and keep the floor right now.
func mtmLotLimit(book []DepthLevel, qty int64, buy bool, execMTM, floor float64) (limit float64, ok bool) {
	if len(book) == 0 || qty <= 0 {
		return 0, false
	}
	vwap, worst, _ := mtmWalk(book, qty)
	slack := (execMTM - floor) / float64(qty) // rupees per unit we may give up
	if slack < 0 {
		return 0, false
	}
	best := book[0].Price
	band := math.Max(0.5, 0.01*worst)
	if buy {
		limit = math.Min(vwap+slack, worst+band)
		limit = math.Floor(limit/mtmTick+1e-9) * mtmTick
		return limit, limit >= best
	}
	limit = math.Max(vwap-slack, worst-band)
	limit = math.Ceil(limit/mtmTick-1e-9) * mtmTick
	return limit, limit <= best
}

// mtmExitCheck runs on every monitor tick for a trade with a level set.
// It returns the executable MTM and floor for display; when the level is
// reached it runs the exit (synchronously, like the SL exit) and ran=true.
func (s *Service) mtmExitCheck(trade StoredTrade, chain *OptionChainSnapshot, spot float64, straddleQty int64) (execMTM, floor float64, ok, ran bool) {
	floor, on := mtmExitFloor(trade.Config, spot, straddleQty)
	if !on || trade.Status != "ACTIVE" || !sbLiveWindow(time.Now().In(lutIST())) {
		return 0, floor, false, false
	}
	src, has := s.Store.(interface {
		TradeLegCash(ctx context.Context, tradeUID string) ([]mtmLeg, error)
	})
	if !has {
		return 0, floor, false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	legs, err := src.TradeLegCash(ctx, trade.TradeUID)
	cancel()
	if err != nil || len(legs) == 0 {
		return 0, floor, false, false
	}
	execMTM, ok = mtmExecMTM(legs, mtmRowLookup(chain))
	if !ok || execMTM < floor {
		return execMTM, floor, ok, false
	}
	open, orig := straddleOpenOrig(legs, trade)
	rem := ruleRemaining(trade.Config.MTMExitPct, orig, trade.Config.MTMExitClosedQty, open, int64(trade.LotSize))
	if rem <= 0 {
		return execMTM, floor, ok, false // its share is done
	}
	started := s.runExitAsync(trade.TradeUID, "MTM", func() { s.mtmExitRun(trade.TradeUID, floor) })
	if started {
		log.Printf("[RISK] MTM_TRIGGER trade=%s executable MTM %.2f >= level %.2f (%s %v) -- closing %d of %d open straddle contracts (rule %.0f%% of %d, %d closed before) lot by lot at depth-checked IOC limits",
			trade.TradeUID, execMTM, floor, trade.Config.MTMExitUnit, *trade.Config.MTMExitLevel, rem, open, ruleShare(trade.Config.MTMExitPct), orig, trade.Config.MTMExitClosedQty)
	}
	return execMTM, floor, true, started
}

func mtmRowLookup(chain *OptionChainSnapshot) func(mtmLeg) *OptionChainRow {
	return func(l mtmLeg) *OptionChainRow {
		if chain == nil {
			return nil
		}
		for i := range chain.Chain {
			r := &chain.Chain[i]
			if (l.Leg == "CE" && r.CEToken == l.Token) || (l.Leg == "PE" && r.PEToken == l.Token) {
				return r
			}
		}
		return nil
	}
}

// mtmRunOpts says what a lot-by-lot MTM close is for.
type mtmRunOpts struct {
	// floor: the trade's MTM floor in rupees, re-read before every lot
	// (constant for the trade's own rule; for the portfolio rule it is the
	// level minus every other trade's MTM, which moves with the market).
	floor  func() (float64, error)
	all    bool   // close everything (portfolio rule), not the trade rule's share
	status string // terminal status once flat
	tag    string // log tag
}

// mtmExitRun closes the trade lot by lot while the level holds.
func (s *Service) mtmExitRun(tradeUID string, floor float64) {
	s.mtmExitRunOpts(tradeUID, mtmRunOpts{floor: func() (float64, error) { return floor, nil }, status: "CLOSED_MTM", tag: "MTM-EXIT"})
}

// mtmExitRunOpts closes the trade lot by lot, each lot priced so the
// trade's executable MTM stays >= the floor.
func (s *Service) mtmExitRunOpts(tradeUID string, o mtmRunOpts) {
	defer s.lockTrade(tradeUID)()
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok || tr.Status != "ACTIVE" {
		return
	}
	src, _ := s.Store.(interface {
		TradeLegCash(ctx context.Context, tradeUID string) ([]mtmLeg, error)
	})
	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil || src == nil || s.OrderEvents == nil {
		log.Printf("[%s] trade=%s cannot run: executor/fills/order events unavailable (%v)", o.tag, tradeUID, err)
		return
	}
	tr.Status, tr.LastUpdateTime = "SQUARING_OFF", time.Now()
	s.Store.UpdateTrade(tr)
	stopAt := func(status, why string) {
		tr.Status, tr.LastUpdateTime = status, time.Now()
		s.Store.UpdateTrade(tr)
		log.Printf("[%s] trade=%s -> %s: %s", o.tag, tradeUID, status, why)
	}
	lot := int64(tr.LotSize)
	if lot <= 0 {
		stopAt("ACTIVE", "invalid lot size")
		return
	}
	// This run's share: the rule's % of the ORIGINAL position, less what
	// it already closed, never more than is open -- split pro rata across
	// every open non-wing leg (straddle + hedges). 100% = everything.
	ctx0, cancel0 := context.WithTimeout(context.Background(), 2*time.Second)
	legs0, lerr0 := src.TradeLegCash(ctx0, tradeUID)
	cancel0()
	if lerr0 != nil {
		stopAt("ACTIVE", fmt.Sprintf("fills unavailable (%v) -- retry next tick", lerr0))
		return
	}
	open0, orig := straddleOpenOrig(legs0, tr)
	rem := ruleRemaining(tr.Config.MTMExitPct, orig, tr.Config.MTMExitClosedQty, open0, lot)
	complete := rem >= open0
	targets := legCloseTargets(legs0, rem, open0, lot)
	if o.all { // portfolio rule: every open non-wing leg, whole
		complete, targets = true, map[int64]int64{}
		for _, l := range legs0 {
			if l.NetShort != 0 {
				targets[l.Token] = abs64(l.NetShort)
			}
		}
	}
	// Each leg's share at the start: the next lot always goes to the leg
	// that is furthest BEHIND its share (least closed so far, as a fraction),
	// so the straddle and the hedge legs come down together in the position's
	// own ratio and the delta stays where it was. (Picking the leg with the
	// most quantity left closed the straddle first and the hedges last; delta
	// drifted to ~+500 mid-exit, 14:01 2026-10-09.)
	tgt0 := map[int64]int64{}
	for t, q := range targets {
		tgt0[t] = q
	}
	var shortChangeCE, shortChangePE int64
	misses := 0
	for step := 0; step < 2000; step++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		legs, lerr := src.TradeLegCash(ctx, tradeUID)
		chain, cerr := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
		cancel()
		if lerr != nil || cerr != nil {
			stopAt("ACTIVE", fmt.Sprintf("fills/chain unavailable (%v / %v) -- retry next tick", lerr, cerr))
			return
		}
		rowOf := mtmRowLookup(chain)
		// Next lot: the open leg with the largest fraction of its share
		// still to close (ties: the larger quantity), never beyond what it
		// holds -- every leg is closed pro rata, lot by lot.
		var pick *mtmLeg
		pickLeft, pickFrac := int64(0), 0.0
		for i := range legs {
			left := min(targets[legs[i].Token], abs64(legs[i].NetShort))
			if left <= 0 || tgt0[legs[i].Token] <= 0 {
				continue
			}
			frac := float64(left) / float64(tgt0[legs[i].Token])
			if pick == nil || frac > pickFrac+1e-9 || (math.Abs(frac-pickFrac) <= 1e-9 && left > pickLeft) {
				pick, pickLeft, pickFrac = &legs[i], left, frac
			}
		}
		if pick == nil {
			anyOpen := false
			for _, l := range legs {
				anyOpen = anyOpen || l.NetShort != 0
			}
			if complete || !anyOpen {
				s.mtmExitFinish(tr, o.status)
				return
			}
			// Partial share done: wings follow the short removed (as PSQF).
			if err := s.adjustWings(context.Background(), executor, tr, shortChangeCE, shortChangePE, tr.Strike, tr.Strike, "PSQF-MTM"); err != nil {
				log.Printf("[WINGS] ⚠ MTM partial wing reduction for %s: %v", tradeUID, err)
			}
			stopAt("ACTIVE", fmt.Sprintf("MTM share done: %d straddle contracts closed by this rule (%.0f%% of %d) -- the rest stays monitored",
				tr.Config.MTMExitClosedQty, ruleShare(tr.Config.MTMExitPct), orig))
			return
		}
		execMTM, priced := mtmExecMTM(legs, rowOf)
		if !priced {
			stopAt("ACTIVE", "an open leg has no price in the chain -- retry next tick")
			return
		}
		floor, ferr := o.floor()
		if ferr != nil {
			stopAt("ACTIVE", fmt.Sprintf("MTM floor unavailable (%v) -- retry next tick", ferr))
			return
		}
		if execMTM < floor {
			stopAt("ACTIVE", fmt.Sprintf("executable MTM %.2f fell below the level %.2f -- remaining position stays monitored; resumes when reachable", execMTM, floor))
			return
		}
		buy := pick.NetShort > 0
		qty := min(lot, abs64(pick.NetShort), targets[pick.Token])
		row := rowOf(*pick)
		limit, can := mtmLotLimit(mtmBook(row, pick.Leg, buy), qty, buy, execMTM, floor)
		if !can {
			misses++
			if misses >= mtmMaxMisses {
				stopAt("ACTIVE", fmt.Sprintf("no %s %s price keeps MTM >= %.2f right now -- waiting", pick.Leg, map[bool]string{true: "ask", false: "bid"}[buy], floor))
				return
			}
			time.Sleep(mtmBookRefresh)
			continue
		}
		side := map[bool]string{true: "BUY", false: "SELL"}[buy]
		got, px, status, unknown := s.mtmSendIOC(executor, tr, *pick, side, qty, limit)
		if unknown {
			stopAt("RECONCILIATION_REQUIRED", fmt.Sprintf("%s %s %d @%.2f IOC: outcome unknown (%s) -- stopped; reconcile with the broker", side, pick.Leg, qty, limit, status))
			return
		}
		if got == 0 {
			misses++
			if misses >= mtmMaxMisses {
				stopAt("ACTIVE", fmt.Sprintf("%d IOCs in a row not filled -- waiting for the next tick", misses))
				return
			}
			time.Sleep(mtmBookRefresh)
			continue
		}
		misses = 0
		log.Printf("[%s] trade=%s %s %s %d/%d @%.2f (IOC limit %.2f) -- executable MTM before %.2f, floor %.2f",
			o.tag, tradeUID, side, pick.Leg, got, qty, px, limit, execMTM, floor)
		targets[pick.Token] -= got
		if pick.Leg == "CE" {
			shortChangeCE += shortChangeFromFill(side, got)
		} else {
			shortChangePE += shortChangeFromFill(side, got)
		}
		if !o.all && (pick.Token == tr.CEToken || pick.Token == tr.PEToken) {
			tr.Config.MTMExitClosedQty += got
		}
		if pick.Token == tr.CEToken && tr.CEQty > 0 {
			tr.CEQty = int(max(0, int64(tr.CEQty)-got))
		}
		if pick.Token == tr.PEToken && tr.PEQty > 0 {
			tr.PEQty = int(max(0, int64(tr.PEQty)-got))
		}
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		time.Sleep(mtmBookRefresh)
	}
	stopAt("ACTIVE", "step limit reached -- continuing next tick")
}

// mtmExitFinish closes the wings (outside the MTM) and marks the trade.
func (s *Service) mtmExitFinish(tr StoredTrade, status string) {
	{ // every wing held, configured or not (no wings = nothing to do)
		if err := s.closeAllWings(context.Background(), tr, "SQF-MTM"); err != nil {
			tr.Status, tr.LastUpdateTime = "ACTIVE", time.Now()
			s.Store.UpdateTrade(tr)
			log.Printf("[MTM-EXIT] trade=%s every non-wing leg is flat but closing the wings failed: %v -- retry next tick", tr.TradeUID, err)
			return
		}
	}
	tr.Status, tr.ClosedAt, tr.LastUpdateTime = status, time.Now(), time.Now()
	if calc, ok := s.Store.(interface {
		TradeRealizedPnL(ctx context.Context, tradeUID string) (float64, error)
	}); ok {
		if v, err := calc.TradeRealizedPnL(context.Background(), tr.TradeUID); err == nil {
			tr.RealizedPnL = v
		}
	}
	s.Store.UpdateTrade(tr)
	log.Printf("[MTM-EXIT] ✅ trade=%s %s realized %.2f", tr.TradeUID, status, tr.RealizedPnL)
	if rt, ok := s.Store.LoadRuntime(tr.TradeUID); ok {
		close(rt.StopCh)
		s.Store.DeleteRuntime(tr.TradeUID)
	}
}

// mtmSendIOC sends one closing IOC limit order and waits for its verified
// outcome. unknown=true: no terminal confirmation and nothing in the
// broker trade book -- the caller must stop.
func (s *Service) mtmSendIOC(executor Executor, tr StoredTrade, l mtmLeg, side string, qty int64, limit float64) (filled int64, px float64, status string, unknown bool) {
	seq := atomic.AddUint64(&mtmOrderSeq, 1) % 1000
	intentID := BuildShortOrderUID(tr.Symbol, fmt.Sprintf("M%s%s%05d%03d", side[:1], l.Leg, l.Token%100000, seq), time.Now(), 0)
	lim := limit
	intent := OrderIntent{
		IntentID: intentID, TradeUID: tr.TradeUID, Token: l.Token, Symbol: tr.Symbol,
		ExchangeSegment: tr.ExchangeSegment, Side: side, Quantity: qty, LotSize: int64(tr.LotSize),
		OrderType: "LIMIT", LimitPrice: &lim, TimeInForce: "IOC", ExpectedPrice: lim,
		ProductType: tr.ProductType, LegType: l.Leg, Phase: "SQF", OrderUID: intentID,
		BrokerName: tr.BrokerName, AccountID: tr.AccountID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), sbConfirmWait+5*time.Second)
	defer cancel()
	brokerOrderID, _, err := s.submitOrderIntent(ctx, executor, tr.TradeUID, intent)
	if err != nil {
		return 0, 0, "submit: " + err.Error(), true
	}
	upd, werr := s.OrderEvents.WaitTerminal(ctx, tr.TradeUID, brokerOrderID, qty, sbConfirmWait)
	if werr != nil {
		if got, tpx, _ := s.sbTradeBookFill(executor, brokerOrderID); got > 0 {
			got = min(got, qty)
			s.persistWingFill(intentID, brokerOrderID, "FILLED", got, qty-got, tpx)
			return got, tpx, "FILLED (trade book)", false
		}
		return 0, 0, fmt.Sprintf("order %s: %v", brokerOrderID, werr), true
	}
	got := min(upd.FilledQty, qty)
	px = upd.AvgFillPrice
	if got > 0 && px <= 0 {
		px = lim // an IOC limit fills at the limit or better: conservative
	}
	s.persistWingFill(intentID, brokerOrderID, upd.Status, got, qty-got, px)
	return got, px, strings.ToUpper(upd.Status), false
}
