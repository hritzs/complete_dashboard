package trading

import (
	"context"
	"log"
	"math"
	"time"
)

// buildTiming holds the build's verification and chase pacing. Zero values
// mean the production defaults; tests shrink them.
type buildTiming struct {
	verifyAttempts      int
	verifyDelay         time.Duration
	chaseRounds         int
	chaseVerifyAttempts int
	chaseVerifyDelay    time.Duration
}

func (t buildTiming) withDefaults() buildTiming {
	if t.verifyAttempts <= 0 {
		t.verifyAttempts = 6
	}
	if t.verifyDelay <= 0 {
		t.verifyDelay = 800 * time.Millisecond
	}
	if t.chaseRounds <= 0 {
		t.chaseRounds = 3
	}
	if t.chaseVerifyAttempts <= 0 {
		t.chaseVerifyAttempts = 4
	}
	if t.chaseVerifyDelay <= 0 {
		t.chaseVerifyDelay = 500 * time.Millisecond
	}
	return t
}

// chaseMaxDiscount bounds how far below the live price a chased SELL may be
// re-priced (10%): a build that cannot fill within it is cancelled rather
// than sold at any price.
const chaseMaxDiscount = 0.10

// chaseOrder is one build order that was acknowledged by the broker.
type chaseOrder struct {
	BrokerOrderID string
	Token         int64
	Leg           string
	Side          string
	Quantity      int64
}

// filledByOrder is the largest verified filled quantity seen per broker order.
func filledByOrder(fills []BrokerFill) map[string]int64 {
	out := map[string]int64{}
	for _, f := range fills {
		if f.Verified && f.FilledQty > out[f.BrokerOrderID] {
			out[f.BrokerOrderID] = f.FilledQty
		}
	}
	return out
}

// unfilledChaseOrders returns the orders whose verified fills fall short of
// their quantity.
func unfilledChaseOrders(orders []chaseOrder, filled map[string]int64) []chaseOrder {
	var out []chaseOrder
	for _, o := range orders {
		if filled[o.BrokerOrderID] < o.Quantity {
			out = append(out, o)
		}
	}
	return out
}

// chasePrice is the next limit for a resting SELL: the LIVE price less the
// sell buffer times the round number (more aggressive each round), never more
// than chaseMaxDiscount below live, on the 0.05 tick.
func chasePrice(livePrice, sellBuffer float64, round int) float64 {
	p := livePrice - sellBuffer*float64(round)
	if floor := livePrice * (1 - chaseMaxDiscount); p < floor {
		p = floor
	}
	if p < 0.05 {
		p = 0.05
	}
	return math.Round(p/0.05) * 0.05
}

// livePriceForToken reads a token's current LTP from a chain snapshot.
func livePriceForToken(chain *OptionChainSnapshot, token int64) float64 {
	if chain == nil {
		return 0
	}
	for _, row := range chain.Chain {
		if row.CEToken == token && row.CELtp > 0 {
			return row.CELtp
		}
		if row.PEToken == token && row.PELtp > 0 {
			return row.PELtp
		}
	}
	return 0
}

// chaseUnfilledBuild works the leftover quantity of a build.
//
// A build leg is a LIMIT order priced from the chain LTP at deploy time. On
// an illiquid contract that price can already be stale (the 2026-09-21 NOV
// build sold a PE at 408.50 into a 404.30 market and it just rested), and
// the build used to stop there, leaving the trade PARTIAL.
//
// For every order that is short of its quantity it re-prices the SAME order
// (modify -- never a second order, so a late fill cannot double the
// quantity) to a marketable limit based on the live price, more aggressively
// each round, verifying fills after each round. If it cannot complete within
// chaseRounds -- or a modify fails -- it CANCELS what is still resting, so
// nothing is left working unmonitored, and reports the verified result. Every
// failure path degrades to "stop and cancel"; it never sends a new order.
func (s *Service) chaseUnfilledBuild(
	ctx context.Context,
	executor Executor,
	provider VerifiedFillsProvider,
	trade StoredTrade,
	orders []chaseOrder,
	submitted map[string]struct{},
	summary VerifiedExecutionSummary,
	timing buildTiming,
) VerifiedExecutionSummary {
	timing = timing.withDefaults()

	modifier, canModify := executor.(OrderModifier)
	canceller, canCancel := executor.(OrderCanceller)
	if !canModify {
		log.Printf("[BUILD-CHASE] trade=%s executor cannot modify orders; leftover quantity left as is", trade.TradeUID)
		return summary
	}

	sellBuffer := trade.Config.SellBuffer
	if sellBuffer <= 0 {
		sellBuffer = 2.0
	}

	reverify := func(attempts int, delay time.Duration) {
		next, err := s.verifyAndPersistTradeFills(
			ctx, provider, trade.BrokerName, trade.AccountID, submitted,
			trade.CEToken, trade.PEToken, int64(trade.CEQty), int64(trade.PEQty),
			attempts, delay,
		)
		if err != nil {
			log.Printf("[BUILD-CHASE] trade=%s verification note: %v", trade.TradeUID, err)
		}
		if len(next.Fills) > 0 || err == nil {
			summary = next
		}
	}

	gaveUp := ""
	for round := 1; round <= timing.chaseRounds; round++ {
		left := unfilledChaseOrders(orders, filledByOrder(summary.Fills))
		if len(left) == 0 {
			break
		}

		chain, err := s.Snapshot.GetOptionChain(ctx, trade.Symbol, trade.Expiry)
		if err != nil {
			gaveUp = "live prices unavailable: " + err.Error()
			break
		}

		failed := false
		for _, o := range left {
			if o.Side != "SELL" {
				continue
			}
			live := livePriceForToken(chain, o.Token)
			if live <= 0 {
				gaveUp = "no live price for token"
				failed = true
				break
			}
			price := chasePrice(live, sellBuffer, round)
			if err := modifier.ModifyOrderPrice(ctx, o.BrokerOrderID, price, o.Quantity, trade.LotSize); err != nil {
				gaveUp = "modify failed: " + err.Error()
				failed = true
				break
			}
			log.Printf("[BUILD-CHASE] trade=%s round=%d leg=%s order=%s re-priced to %.2f (live %.2f)",
				trade.TradeUID, round, o.Leg, o.BrokerOrderID, price, live)
		}
		if failed {
			break
		}

		reverify(timing.chaseVerifyAttempts, timing.chaseVerifyDelay)
	}

	left := unfilledChaseOrders(orders, filledByOrder(summary.Fills))
	if len(left) == 0 {
		log.Printf("[BUILD-CHASE] trade=%s leftover quantity filled", trade.TradeUID)
		return summary
	}

	if gaveUp == "" {
		gaveUp = "not filled within the chase rounds"
	}
	log.Printf("[BUILD-CHASE] trade=%s giving up (%s); %d order(s) still unfilled", trade.TradeUID, gaveUp, len(left))

	if !canCancel {
		log.Printf("[BUILD-CHASE] trade=%s executor cannot cancel: unfilled order(s) are STILL RESTING at the broker", trade.TradeUID)
		return summary
	}
	for _, o := range left {
		if err := canceller.CancelOrder(ctx, o.BrokerOrderID); err != nil {
			log.Printf("[BUILD-CHASE] trade=%s cancel of %s FAILED: %v -- it may still be working at the broker", trade.TradeUID, o.BrokerOrderID, err)
			continue
		}
		log.Printf("[BUILD-CHASE] trade=%s cancelled unfilled order %s (%s)", trade.TradeUID, o.BrokerOrderID, o.Leg)
	}
	// A fill can land while the cancel is in flight.
	reverify(2, timing.chaseVerifyDelay)
	return summary
}
