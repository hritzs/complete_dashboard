package trading

// Hedge fallback: cut shorts when the synthetic hedge cannot trade.
//
// A hedge is a synthetic future (BUY CE + SELL PE, or the reverse) at the
// live ATM. When a tranche fills NOTHING on both legs (e.g. the broker's RMS
// refused it -- 19 Oct 09:27: "Calculated MTM Limit exceeds Maximum allowed
// MTM"), the delta still has to come off. Buying back part of a SHORT option
// moves delta the same way without opening anything new:
//
//	need +delta (synthetic would BUY CE / SELL PE) -> buy back short CEs
//	need -delta (synthetic would SELL CE / BUY PE) -> buy back short PEs
//
// The trade's short legs of that type (own straddle leg and hedge legs) are
// cut highest-delta first, each sized remaining delta / |leg delta| in whole
// lots and never more than is held short. Delta per leg is the live chain's.
// The open position (trade_legs from fills, the own-leg CE/PE quantity) and
// the wings follow the confirmed fills.

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"sync/atomic"
	"time"
)

type hedgeCutLeg struct {
	leg   mtmLeg
	delta float64 // |delta| per contract
	qty   int64   // contracts to buy back
}

// hedgeCutPlan: the short legs of type want, highest |delta| first, each cut
// remaining delta / |delta| in whole lots (never more than held short), until
// less than half a lot of the next leg is left to remove.
func hedgeCutPlan(legs []mtmLeg, chain *OptionChainSnapshot, want string, need float64, lot int64) []hedgeCutLeg {
	if chain == nil || lot <= 0 || need <= 0 {
		return nil
	}
	var cands []hedgeCutLeg
	for _, l := range legs {
		if l.Leg != want || l.NetShort <= 0 {
			continue
		}
		d := 0.0
		for _, r := range chain.Chain {
			if want == "CE" && r.CEToken == l.Token {
				d = math.Abs(r.CEDelta)
			} else if want == "PE" && r.PEToken == l.Token {
				d = math.Abs(r.PEDelta)
			}
		}
		if d > 0.01 {
			cands = append(cands, hedgeCutLeg{leg: l, delta: d})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].delta > cands[j].delta })
	var plan []hedgeCutLeg
	for _, c := range cands {
		if need < 0.5*float64(lot)*c.delta {
			break
		}
		q := int64(math.Round(need/c.delta/float64(lot))) * lot
		if held := c.leg.NetShort / lot * lot; q > held {
			q = held
		}
		if q <= 0 {
			continue
		}
		c.qty = q
		plan = append(plan, c)
		need -= float64(q) * c.delta
	}
	return plan
}

// hedgeCutShorts buys back short options of the type that moves delta by
// deltaNeeded (contracts of a delta-1 synthetic). Returns the delta removed
// and the net short change per side for the wings.
func (s *Service) hedgeCutShorts(ctx context.Context, executor Executor, tr StoredTrade, tradeUID, ceSide string,
	deltaNeeded int64, chain *OptionChainSnapshot) (got float64, shortCE, shortPE int64, err error) {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok || deltaNeeded <= 0 || chain == nil {
		return 0, 0, 0, fmt.Errorf("cut-shorts fallback unavailable")
	}
	want := "PE" // synthetic would SELL CE / BUY PE -> -delta -> buy back short PEs
	if ceSide == "BUY" {
		want = "CE" // +delta -> buy back short CEs
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	legs, err := pg.TradeLegCash(qctx, tradeUID)
	cancel()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load legs: %w", err)
	}
	lot := int64(tr.LotSize)
	plan := hedgeCutPlan(legs, chain, want, float64(deltaNeeded), lot)
	if len(plan) == 0 {
		return 0, 0, 0, fmt.Errorf("no short %s leg with a live delta to cut", want)
	}
	maxPer := s.resolveMaxOrderQty(tr.Symbol, lot)
	need := float64(deltaNeeded)
	for _, c := range plan {
		for remaining := c.qty; remaining > 0; {
			piece := min(remaining, maxPer)
			seq := atomic.AddUint64(&mtmOrderSeq, 1) % 1000
			intentID := BuildShortOrderUID(tr.Symbol, fmt.Sprintf("HC%s%05d%03d", want, c.leg.Token%100000, seq), time.Now(), 0)
			intent := OrderIntent{
				IntentID: intentID, TradeUID: tradeUID, Token: c.leg.Token, Symbol: tr.Symbol,
				ExchangeSegment: tr.ExchangeSegment, Side: "BUY", Quantity: piece, LotSize: lot,
				OrderType: "MARKET", ProductType: tr.ProductType, LegType: want, Phase: "HEDGE", OrderUID: intentID,
				BrokerName: tr.BrokerName, AccountID: tr.AccountID,
			}
			octx, ocancel := context.WithTimeout(ctx, sbConfirmWait+5*time.Second)
			brokerOrderID, _, serr := s.submitOrderIntent(octx, executor, tradeUID, intent)
			if serr != nil {
				ocancel()
				return got, shortCE, shortPE, fmt.Errorf("buy back %s %.0f %d: %w", want, c.leg.Strike, piece, serr)
			}
			upd, werr := s.OrderEvents.WaitTerminal(octx, tradeUID, brokerOrderID, piece, sbConfirmWait)
			ocancel()
			filled := min(upd.FilledQty, piece)
			if filled > 0 {
				got += float64(filled) * c.delta
				need -= float64(filled) * c.delta
				if want == "CE" {
					shortCE -= filled
				} else {
					shortPE -= filled
				}
				s.bookOwnLegCut(tradeUID, tr, c.leg.Token, want, filled)
				log.Printf("[HEDGE-CUT] trade=%s bought back %d short %s %.0f (delta %.3f) -> %.0f delta removed, %.0f still needed",
					tradeUID, filled, want, c.leg.Strike, c.delta, float64(filled)*c.delta, math.Max(need, 0))
			}
			if werr != nil || filled < piece {
				return got, shortCE, shortPE, fmt.Errorf("buy back %s %.0f order %s: filled %d of %d (%v)", want, c.leg.Strike, brokerOrderID, filled, piece, werr)
			}
			remaining -= piece
		}
	}
	return got, shortCE, shortPE, nil
}

// bookOwnLegCut keeps the trade's own-leg quantity in step when the cut is on
// its own CE / PE token (other legs are tracked by trade_legs from the fills).
func (s *Service) bookOwnLegCut(tradeUID string, tr StoredTrade, token int64, opt string, qty int64) {
	if token != tr.CEToken && token != tr.PEToken {
		return
	}
	latest, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return
	}
	if opt == "CE" && token == latest.CEToken {
		latest.CEQty = int(max(0, int64(latest.CEQty)-qty))
	} else if opt == "PE" && token == latest.PEToken {
		latest.PEQty = int(max(0, int64(latest.PEQty)-qty))
	}
	latest.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(latest)
}
