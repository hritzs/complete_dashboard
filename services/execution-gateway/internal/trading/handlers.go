package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Handlers struct {
	Service *Service
	Store   Store
}

func NewHandlers(service *Service, store Store) *Handlers {
	return &Handlers{
		Service: service,
		Store:   store,
	}
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"mode":   "live",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func (h *Handlers) DeployStraddle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeployStraddleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(DeployStraddleResponse{
			Success: false,
			Error:   "invalid request body: " + err.Error(),
		})
		return
	}

	resp, err := h.Service.DeployStraddle(context.Background(), req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(DeployStraddleResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) ConfigBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid request body: " + err.Error(),
		})
		return
	}

	dReq := DeployStraddleRequest{
		UserID:           req.UserID,
		BrokerName:       req.BrokerName,
		AccountID:        req.AccountID,
		ExchangeSegment:  req.ExchangeSegment,
		Symbol:           req.Symbol,
		Lots:             req.Size,
		ProductType:      "MIS",
		DeltaNeutral:     true,
		TargetExpiry:     req.TargetExpiry,
		OrderLotsPerCall: req.OrderLotsPerCall,
	}

	if dReq.Lots == 0 {
		dReq.Lots = req.Lots
	}

	resp, err := h.Service.DeployStraddle(context.Background(), dReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) CustomSell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CustomStraddleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid request body: " + err.Error(),
		})
		return
	}

	resp, err := h.Service.DeployStraddle(context.Background(), DeployStraddleRequest{
		UserID:           req.UserID,
		BrokerName:       req.BrokerName,
		AccountID:        req.AccountID,
		ExchangeSegment:  req.ExchangeSegment,
		Symbol:           req.Symbol,
		Lots:             req.Lots,
		CEStrikePrice:    req.CEStrikePrice,
		PEStrikePrice:    req.PEStrikePrice,
		DeltaNeutral:     req.DeltaNeutral,
		ProductType:      req.ProductType,
		OrderLotsPerCall: req.OrderLotsPerCall,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) SquareOff(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid trade square-off path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	if err := h.Service.SquareOff(tradeUID, "manual"); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	tr, _ := h.Store.LoadTrade(tradeUID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"trade_uid": tradeUID,
		"status":    tr.Status,
	})
}

func (h *Handlers) PartialSquareOff(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	var req PartialSquareOffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Percentage = 50
	}

	if err := h.Service.PartialSquareOff(tradeUID, req.Percentage); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Partial square-off for %s completed", tradeUID),
	})
}

func (h *Handlers) ManualHedge(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	if err := h.Service.ManualHedge(context.Background(), tradeUID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Manual hedge for %s completed", tradeUID),
	})
}

type ManualSquareOffResponse struct {
	Success   bool               `json:"success"`
	TradeUID  string             `json:"trade_uid"`
	Phase     string             `json:"phase"`
	Broker    string             `json:"broker"`
	AccountID string             `json:"account_id"`
	Results   []*ExecutionResult `json:"results,omitempty"`
	Error     string             `json:"error,omitempty"`
	Message   string             `json:"message,omitempty"`
}

// ManualSquareOff handles the direct, non-position-aware square off of a trade.
func (h *Handlers) ManualSquareOff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ManualSquareOffResponse{
			Success: false,
			Error:   "method not allowed",
		})
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualSquareOffResponse{
			Success: false,
			Error:   "invalid trade path, expected /api/v1/trade/{uid}/manual-squareoff",
		})
		return
	}
	tradeUID := parts[2]

	results, err := h.Service.ManualSquareOff(r.Context(), tradeUID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualSquareOffResponse{
			Success:  false,
			TradeUID: tradeUID,
			Phase:    "MANUAL_SQF",
			Error:    err.Error(),
		})
		return
	}

	tr, _ := h.Store.LoadTrade(tradeUID)
	_ = json.NewEncoder(w).Encode(ManualSquareOffResponse{
		Success:   true,
		TradeUID:  tradeUID,
		Phase:     "MANUAL_SQF",
		Broker:    tr.BrokerName,
		AccountID: tr.AccountID,
		Results:   results,
		Message:   "manual square-off order completed",
	})
}

func (h *Handlers) ManualRoll(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   false,
		"trade_uid": tradeUID,
		"error":     "manual roll not implemented yet in Go runtime",
	})
}

func (h *Handlers) ManualVerify(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	snap, ok := h.Store.LoadSnapshot(tradeUID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "snapshot not found",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"snapshot": snap,
	})
}

func (h *Handlers) CancelAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   false,
		"trade_uid": tradeUID,
		"error":     "cancel action not implemented yet in Go runtime",
	})
}

func (h *Handlers) GetStraddles(w http.ResponseWriter, r *http.Request) {
	trades := h.Store.AllTrades()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"count":     len(trades),
		"straddles": trades,
	})
}

func (h *Handlers) GetActiveStraddles(w http.ResponseWriter, r *http.Request) {
	all := h.Store.AllTrades()
	active := make([]StoredTrade, 0)

	for _, tr := range all {
		if tr.Status == "ACTIVE" || tr.Status == "BUILDING" || tr.Status == "PARTIAL" {
			active = append(active, tr)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"count":     len(active),
		"straddles": active,
	})
}

func (h *Handlers) GetSnapshot(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	snap, ok := h.Store.LoadSnapshot(tradeUID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "snapshot not found",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    snap,
	})
}

func (h *Handlers) GetPnL(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	snap, ok := h.Store.LoadSnapshot(tradeUID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "snapshot not found",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"total_pnl":      snap.TotalPNL,
			"realized_pnl":   snap.RealizedPNL,
			"unrealized_pnl": snap.UnrealizedPNL,
			"net_delta":      snap.NetDelta,
			"net_gamma":      snap.NetGamma,
			"net_theta":      snap.NetTheta,
			"net_vega":       snap.NetVega,
		},
	})
}

func (h *Handlers) GetOrders(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	intents := h.Store.LoadIntents(tradeUID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   len(intents),
		"orders":  intents,
	})
}

func (h *Handlers) TradeStatus(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	tr, ok := h.Store.LoadTrade(tradeUID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "trade not found",
		})
		return
	}

	intents := h.Store.LoadIntents(tradeUID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"trade":   tr,
		"intents": intents,
	})
}
