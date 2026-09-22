package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ManualOrderRequest is a direct, ad-hoc order: NOT tied to a StoredTrade's
// tracked quantities (see the package doc comment on ManualOrder below for
// why). token is the same short instrument token DeployStraddle/SquareOff/
// ManualHedgeLots already use elsewhere; side/order_type use this codebase's
// usual "BUY"/"SELL" and "LIMIT"/"MARKET" strings, not GreekSoft's raw
// numeric codes.
type ManualOrderRequest struct {
	UserID          string  `json:"user_id,omitempty"`
	BrokerName      string  `json:"broker_name"`
	AccountID       string  `json:"account_id"`
	ExchangeSegment string  `json:"exchange_segment,omitempty"`
	ProductType     string  `json:"product_type,omitempty"`
	Symbol          string  `json:"symbol"`
	Token           int64   `json:"token"`
	Side            string  `json:"side"`
	OrderType       string  `json:"order_type"`
	Price           float64 `json:"price,omitempty"`
	TotalLots       int     `json:"total_lots"`
	LotsPerOrder    int     `json:"lots_per_order,omitempty"`
	LotSize         int     `json:"lot_size"`
}

// ManualOrderLegResult is one broker order placed for a ManualOrderRequest.
type ManualOrderLegResult struct {
	IntentID      string  `json:"intent_id"`
	Lots          int     `json:"lots"`
	Quantity      int64   `json:"quantity"`
	BrokerOrderID string  `json:"broker_order_id,omitempty"`
	Status        string  `json:"status"`
	Error         string  `json:"error,omitempty"`
	VerifiedQty   int64   `json:"verified_qty,omitempty"`
	VerifiedPrice float64 `json:"verified_price,omitempty"`
}

type ManualOrderResponse struct {
	Success bool                   `json:"success"`
	Tag     string                 `json:"tag"`
	Legs    []ManualOrderLegResult `json:"legs"`
	Error   string                 `json:"error,omitempty"`
}

func (r ManualOrderRequest) validate() error {
	if strings.TrimSpace(r.BrokerName) == "" {
		return fmt.Errorf("broker_name is required")
	}
	if strings.TrimSpace(r.AccountID) == "" {
		return fmt.Errorf("account_id is required")
	}
	if strings.TrimSpace(r.Symbol) == "" {
		return fmt.Errorf("symbol is required")
	}
	if r.Token <= 0 {
		return fmt.Errorf("token is required")
	}
	side := strings.ToUpper(strings.TrimSpace(r.Side))
	if side != "BUY" && side != "SELL" {
		return fmt.Errorf("side must be BUY or SELL, got %q", r.Side)
	}
	orderType := strings.ToUpper(strings.TrimSpace(r.OrderType))
	if orderType != "LIMIT" && orderType != "MARKET" {
		return fmt.Errorf("order_type must be LIMIT or MARKET, got %q", r.OrderType)
	}
	if orderType == "LIMIT" && r.Price <= 0 {
		return fmt.Errorf("price is required for a LIMIT order")
	}
	if r.TotalLots <= 0 {
		return fmt.Errorf("total_lots must be positive")
	}
	if r.LotSize <= 0 {
		return fmt.Errorf("lot_size is required")
	}
	return nil
}

// manualOrderChunks splits totalLots into chunks of at most lotsPerOrder lots
// each (default: all in one chunk). Pure so it can be tested without a
// broker.
func manualOrderChunks(totalLots, lotsPerOrder int) []int {
	if lotsPerOrder <= 0 || lotsPerOrder >= totalLots {
		return []int{totalLots}
	}
	var out []int
	remaining := totalLots
	for remaining > 0 {
		lots := lotsPerOrder
		if lots > remaining {
			lots = remaining
		}
		out = append(out, lots)
		remaining -= lots
	}
	return out
}

// ManualOrder places one or more real broker orders for an arbitrary
// token/side/quantity, for manual testing (the Testing tab's "Direct manual
// order" tool) -- e.g. checking how a specific limit price behaves, or
// exercising the chase/modify path by hand.
//
// Deliberately NOT tied to any StoredTrade's CEQty/PEQty: this is an ad-hoc
// order, not a leg of a tracked position, so there is nothing to reconcile
// it against. It does not touch the trades/orders tables (AppendIntent
// there requires a resolvable trade_id, which an ad-hoc order has none of);
// the reconciler's own Iris-push tracking still records the real fill at
// the broker regardless. If the executor can verify fills, this polls
// briefly and reports what filled; if not, it reports only the broker's
// initial acknowledgement.
//
// This replaces a stub ("Wire ExecuteGreeksoftOrder() call here to actually
// submit") that the UI's "SELL LIVE ATM" button called with a real-looking
// confirmation dialog while silently placing nothing.
func (h *Handlers) ManualOrder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ManualOrderResponse{Error: "method not allowed"})
		return
	}

	var req ManualOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualOrderResponse{Error: err.Error()})
		return
	}
	if err := req.validate(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualOrderResponse{Error: err.Error()})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = "U001"
	}
	side := strings.ToUpper(strings.TrimSpace(req.Side))
	orderType := strings.ToUpper(strings.TrimSpace(req.OrderType))
	exchangeSegment := strings.ToUpper(strings.TrimSpace(req.ExchangeSegment))
	if exchangeSegment == "" {
		exchangeSegment = ResolveExchangeSegment(req.Symbol, "")
	}
	productType := strings.ToUpper(strings.TrimSpace(req.ProductType))
	if productType == "" {
		productType = "NRML"
	}

	executor, err := h.Service.BrokerFactory.GetExecutor(userID, req.BrokerName, req.AccountID)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(ManualOrderResponse{Error: err.Error()})
		return
	}
	provider, canVerify := executor.(VerifiedFillsProvider)

	tag := fmt.Sprintf("MANUAL_%d_%d", req.Token, time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	resp := ManualOrderResponse{Success: true, Tag: tag}
	submitted := map[string]struct{}{}

	for i, lots := range manualOrderChunks(req.TotalLots, req.LotsPerOrder) {
		qty := int64(lots * req.LotSize)
		intentID := fmt.Sprintf("%s_C%d", tag, i+1)
		var limitPrice *float64
		if orderType == "LIMIT" {
			p := req.Price
			limitPrice = &p
		}
		intent := OrderIntent{
			IntentID:        intentID,
			Token:           req.Token,
			Symbol:          strings.TrimSpace(req.Symbol),
			ExchangeSegment: exchangeSegment,
			Side:            side,
			Quantity:        qty,
			OrderType:       orderType,
			LimitPrice:      limitPrice,
			ProductType:     productType,
			Phase:           "MANUAL",
			OrderUID:        intentID,
			BrokerName:      strings.ToUpper(req.BrokerName),
			AccountID:       req.AccountID,
		}

		leg := ManualOrderLegResult{IntentID: intentID, Lots: lots, Quantity: qty, Status: "SUBMIT_FAILED"}

		res, execErr := executor.ExecuteOrderIntent(ctx, intent)
		if execErr != nil {
			leg.Error = execErr.Error()
			resp.Success = false
			log.Printf("[MANUAL ORDER] leg=%s token=%d side=%s qty=%d FAILED: %v", intentID, req.Token, side, qty, execErr)
		} else {
			leg.BrokerOrderID = res.BrokerOrderID
			leg.Status = res.Status
			log.Printf("[MANUAL ORDER] leg=%s token=%d side=%s qty=%d broker_order_id=%s status=%s",
				intentID, req.Token, side, qty, res.BrokerOrderID, res.Status)
			if strings.TrimSpace(res.BrokerOrderID) != "" {
				submitted[res.BrokerOrderID] = struct{}{}
			}
		}
		resp.Legs = append(resp.Legs, leg)
	}

	// Built AFTER the submit loop (not accumulated during it): resp.Legs may
	// reallocate as it grows via append, so a pointer taken mid-loop could
	// point at freed memory once the slice moves.
	if canVerify && len(submitted) > 0 {
		idxByOrderID := map[string]int{}
		for i := range resp.Legs {
			if resp.Legs[i].BrokerOrderID != "" {
				idxByOrderID[resp.Legs[i].BrokerOrderID] = i
			}
		}
		fills, verr := provider.GetVerifiedFills(ctx)
		if verr != nil {
			log.Printf("[MANUAL ORDER] tag=%s fill verification failed: %v", tag, verr)
		} else {
			for _, f := range fills {
				if !f.Verified {
					continue
				}
				if idx, ok := idxByOrderID[f.BrokerOrderID]; ok {
					resp.Legs[idx].VerifiedQty = f.FilledQty
					resp.Legs[idx].VerifiedPrice = f.AveragePrice
					if f.Status != "" {
						resp.Legs[idx].Status = f.Status
					}
				}
			}
		}
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// ManualModifyOrderRequest re-prices (and/or re-sizes) a resting order this
// same account placed -- e.g. one just submitted via ManualOrder, or any
// other broker_order_id the caller already knows about.
type ManualModifyOrderRequest struct {
	BrokerName    string  `json:"broker_name"`
	AccountID     string  `json:"account_id"`
	BrokerOrderID string  `json:"broker_order_id"`
	Price         float64 `json:"price"`
	Quantity      int64   `json:"quantity"`
	LotSize       int     `json:"lot_size"`
}

// ManualModifyOrder re-prices a resting order via the same OrderModifier
// interface the build-leftover chase uses (GreekSoft's SmallModifyOrderRequest).
func (h *Handlers) ManualModifyOrder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}

	var req ManualModifyOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.BrokerOrderID) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "broker_order_id is required"})
		return
	}
	if req.Price <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "price must be positive"})
		return
	}
	if req.Quantity <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "quantity must be positive"})
		return
	}

	executor, err := h.Service.BrokerFactory.GetExecutor("U001", req.BrokerName, req.AccountID)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	modifier, ok := executor.(OrderModifier)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "this broker cannot modify orders"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := modifier.ModifyOrderPrice(ctx, req.BrokerOrderID, req.Price, req.Quantity, req.LotSize); err != nil {
		log.Printf("[MANUAL MODIFY] broker_order_id=%s FAILED: %v", req.BrokerOrderID, err)
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	log.Printf("[MANUAL MODIFY] broker_order_id=%s re-priced to %.2f qty=%d", req.BrokerOrderID, req.Price, req.Quantity)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "broker_order_id": req.BrokerOrderID, "price": req.Price, "quantity": req.Quantity,
	})
}
