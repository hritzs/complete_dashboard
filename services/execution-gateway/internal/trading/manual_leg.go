package trading

// Manual legs on an existing trade (Portfolio card -> "Add / Close leg"):
//
//   ADD   -- buy or sell any strike / CE-PE / EXPIRY (across expiry too) of the
//            trade's symbol as part of THIS trade. include_risk chooses whether
//            the leg counts in the trade's PnL / greeks / PointsOut / SL / TP
//            (excluded legs are still shown and still closed by Full Exit).
//   CLOSE -- square off some or all lots of one open leg of the trade
//            (straddle leg, hedge, or a manual leg; any expiry).
//
// Every order is a MARKET order tagged to the trade (same path as Full
// Exit's extra-leg close): persisted before sending, confirmed by the
// broker, the fill booked under the trade, trade legs recomputed, and the
// straddle's CE/PE quantity re-read from fills if a straddle leg changed.
// Only ever sent on an explicit user request with a typed confirmation.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

type ManualLegRequest struct {
	TradeUID    string  `json:"trade_uid"`
	Action      string  `json:"action"` // ADD | CLOSE
	Expiry      string  `json:"expiry"` // ADD ("" = trade expiry)
	Strike      float64 `json:"strike"` // ADD
	OptionType  string  `json:"option_type"`
	Side        string  `json:"side"`  // ADD: BUY | SELL
	Token       int64   `json:"token"` // CLOSE: the leg's token
	Lots        int     `json:"lots"`  // ADD: lots; CLOSE: lots (0 = all)
	IncludeRisk *bool   `json:"include_risk,omitempty"`
	Confirm     string  `json:"confirm"`
}

type ManualLegResult struct {
	TradeUID  string  `json:"trade_uid"`
	Action    string  `json:"action"`
	Token     int64   `json:"token"`
	Contract  string  `json:"contract"`
	Side      string  `json:"side"`
	Qty       int64   `json:"qty"`
	Filled    int64   `json:"filled"`
	AvgPrice  float64 `json:"avg_price"`
	InRisk    bool    `json:"in_risk"`
	OpenAfter int64   `json:"open_after"` // signed, this token
	Message   string  `json:"message"`
}

const manualLegConfirm = "CONFIRM"

func manualLegWindow(now time.Time) bool {
	hm := now.Hour()*100 + now.Minute()
	return hm >= 915 && hm < 1540
}

// ManualLeg runs one ADD / CLOSE on a trade.
func (s *Service) ManualLeg(ctx context.Context, req ManualLegRequest) (ManualLegResult, error) {
	res := ManualLegResult{TradeUID: req.TradeUID, Action: strings.ToUpper(strings.TrimSpace(req.Action))}
	if strings.TrimSpace(req.Confirm) != manualLegConfirm {
		return res, fmt.Errorf("type %s to confirm a real order", manualLegConfirm)
	}
	if !manualLegWindow(time.Now().In(lutIST())) {
		return res, fmt.Errorf("outside the broker session (09:15-15:40)")
	}
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return res, fmt.Errorf("postgres store unavailable")
	}
	defer s.lockTrade(req.TradeUID)()
	tr, ok := s.Store.LoadTrade(req.TradeUID)
	if !ok {
		return res, fmt.Errorf("trade %s not found", req.TradeUID)
	}
	if isTerminalTradeStatus(tr.Status) || strings.HasPrefix(strings.ToUpper(tr.Status), "CLOSED") || tr.Status == "SQUARING_OFF" || tr.Status == "BUILDING" {
		return res, fmt.Errorf("trade is %s -- manual legs only on an open trade", tr.Status)
	}
	if s.OrderEvents == nil || !s.OrderEvents.Healthy() {
		return res, fmt.Errorf("order confirmations (reconciler feed) are not healthy -- not sending")
	}

	var token int64
	var exchange, contract, side string
	var qty int64
	lotSize := int64(tr.LotSize)

	switch res.Action {
	case "ADD":
		opt := strings.ToUpper(strings.TrimSpace(req.OptionType))
		side = strings.ToUpper(strings.TrimSpace(req.Side))
		if (opt != "CE" && opt != "PE") || (side != "BUY" && side != "SELL") || req.Lots <= 0 || req.Strike <= 0 {
			return res, fmt.Errorf("ADD needs option_type CE/PE, side BUY/SELL, strike and lots > 0")
		}
		expiry := strings.ToUpper(strings.TrimSpace(req.Expiry))
		if expiry == "" {
			expiry = tr.Expiry
		}
		chain, err := s.Snapshot.GetOptionChain(ctx, tr.Symbol, expiry)
		if err != nil || chain == nil {
			return res, fmt.Errorf("option chain %s %s: %v", tr.Symbol, expiry, err)
		}
		row, err := FindRowByStrike(*chain, int(math.Round(req.Strike)))
		if err != nil || row == nil {
			return res, fmt.Errorf("strike %.0f not in the %s chain", req.Strike, chain.Expiry)
		}
		token = row.CEToken
		if opt == "PE" {
			token = row.PEToken
		}
		if token == 0 {
			return res, fmt.Errorf("no token for %s %.0f %s", chain.Expiry, req.Strike, opt)
		}
		if s.LotSize != nil {
			if l, lerr := s.LotSize.GetLotSize(ctx, tr.Symbol, chain.Expiry); lerr == nil && l > 0 {
				lotSize = int64(l)
			}
		}
		exchange = tr.ExchangeSegment
		qty = int64(req.Lots) * lotSize
		contract = fmt.Sprintf("%s %s %.0f %s", tr.Symbol, chain.Expiry, row.Strike, opt)
		res.InRisk = req.IncludeRisk == nil || *req.IncludeRisk

	case "CLOSE":
		legs, err := pg.LoadOpenLegs(req.TradeUID)
		if err != nil {
			return res, err
		}
		var leg *OpenLeg
		for i := range legs {
			if legs[i].Token == req.Token {
				leg = &legs[i]
			}
		}
		if leg == nil || leg.Qty-leg.WingQty == 0 {
			return res, fmt.Errorf("token %d has no open position in this trade", req.Token)
		}
		open := leg.Qty - leg.WingQty
		side = "BUY" // close a short
		abs := -open
		if open > 0 {
			side, abs = "SELL", open
		}
		qty = abs
		if req.Lots > 0 && lotSize > 0 && int64(req.Lots)*lotSize < abs {
			qty = int64(req.Lots) * lotSize
		}
		token, exchange = leg.Token, leg.Exchange
		contract = fmt.Sprintf("%s %s %.0f %s", tr.Symbol, leg.Expiry, leg.Strike, leg.OptionType)
	default:
		return res, fmt.Errorf("action must be ADD or CLOSE")
	}
	if lotSize <= 0 || qty <= 0 {
		return res, fmt.Errorf("invalid quantity")
	}
	res.Token, res.Contract, res.Side, res.Qty = token, contract, side, qty

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return res, fmt.Errorf("executor: %w", err)
	}
	log.Printf("[MANUAL-LEG] trade=%s %s %s %s qty=%d (requested by user)", tr.TradeUID, res.Action, side, contract, qty)

	var value float64
	maxPer := s.resolveMaxOrderQty(tr.Symbol, lotSize)
	for piece, remaining := 0, qty; remaining > 0; piece++ {
		pq := remaining
		if pq > maxPer {
			pq = maxPer
		}
		intentID := BuildShortOrderUID(tr.Symbol, fmt.Sprintf("ML%s%d", side[:1], piece), time.Now(), piece)
		intent := OrderIntent{
			IntentID: intentID, TradeUID: tr.TradeUID, Token: token, Symbol: tr.Symbol, ExchangeSegment: exchange,
			Side: side, Quantity: pq, LotSize: lotSize, OrderType: "MARKET", ProductType: tr.ProductType,
			Phase: "MANUAL_LEG", OrderUID: intentID, BrokerName: tr.BrokerName, AccountID: tr.AccountID,
		}
		brokerOrderID, _, serr := s.submitOrderIntent(ctx, executor, tr.TradeUID, intent)
		if serr != nil {
			res.Message = fmt.Sprintf("order failed after %d filled: %v", res.Filled, serr)
			break
		}
		wctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		upd, werr := s.OrderEvents.WaitTerminal(wctx, tr.TradeUID, brokerOrderID, pq, 15*time.Second)
		cancel()
		got := upd.FilledQty
		px := upd.AvgFillPrice
		if werr != nil {
			if g, p, _ := s.sbTradeBookFill(executor, brokerOrderID); g > 0 {
				got, px = g, p
			}
		}
		if got > pq {
			got = pq
		}
		if got > 0 {
			s.persistWingFill(intentID, brokerOrderID, "FILLED", got, pq-got, px)
			res.Filled += got
			value += float64(got) * px
		}
		log.Printf("[MANUAL-LEG] trade=%s %s %s %d -> filled %d @%.2f broker_order_id=%s", tr.TradeUID, side, contract, pq, got, px, brokerOrderID)
		if werr != nil && got == 0 {
			res.Message = fmt.Sprintf("broker order %s not confirmed -- check the broker; Sync with Broker will record it if it filled", brokerOrderID)
			break
		}
		if got < pq {
			res.Message = fmt.Sprintf("order %s filled %d of %d", brokerOrderID, got, pq)
			break
		}
		remaining -= pq
	}
	if res.Filled > 0 {
		res.AvgPrice = math.Round(value/float64(res.Filled)*100) / 100
	}

	// Book it on the trade: legs from fills, straddle quantity re-read,
	// risk inclusion, and an entry in the trade's change history.
	if err := pg.RecomputeTradeLegs(context.Background(), tr.TradeUID); err != nil {
		log.Printf("[MANUAL-LEG] trade=%s recompute legs: %v", tr.TradeUID, err)
	}
	cur, _ := s.Store.LoadTrade(tr.TradeUID)
	if token == cur.CEToken || token == cur.PEToken {
		if ce, pe, qerr := pg.TradeOpenQuantities(context.Background(), cur.TradeUID); qerr == nil {
			cur.CEQty, cur.PEQty = int(ce), int(pe)
		}
	}
	if res.Action == "ADD" && !res.InRisk && token != cur.CEToken && token != cur.PEToken {
		seen := false
		for _, t := range cur.Config.RiskExcludedTokens {
			seen = seen || t == token
		}
		if !seen {
			cur.Config.RiskExcludedTokens = append(cur.Config.RiskExcludedTokens, token)
		}
	}
	if res.Filled > 0 {
		note := fmt.Sprintf("%s %d @%.2f", side, res.Filled, res.AvgPrice)
		if res.Action == "ADD" {
			note += map[bool]string{true: " (in P&L / greeks)", false: " (NOT in P&L / greeks)"}[res.InRisk]
		}
		cur.Config.Modifications = append(cur.Config.Modifications, ConfigChange{
			Time: time.Now().In(lutIST()).Format("15:04:05"), Field: fmt.Sprintf("Manual leg %s: %s", strings.ToLower(res.Action), contract), From: "", To: note,
		})
	}
	cur.LastUpdateTime = time.Now()
	s.Store.UpdateTrade(cur)

	if legs, err := pg.LoadOpenLegs(tr.TradeUID); err == nil {
		for _, l := range legs {
			if l.Token == token {
				res.OpenAfter = l.Qty
			}
		}
	}
	if res.Message == "" {
		res.Message = fmt.Sprintf("%s %s %d %s filled @%.2f", res.Action, side, res.Filled, contract, res.AvgPrice)
	}
	return res, nil
}

// ManualLegHandler: POST /api/trade/manual-leg (ManualLegRequest).
func (h *Handlers) ManualLegHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var req ManualLegRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "bad body: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	res, err := h.Service.ManualLeg(ctx, req)
	if err != nil {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error(), "result": res})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": res.Filled > 0, "result": res, "error": map[bool]string{true: "", false: res.Message}[res.Filled > 0]})
}
