package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type manualSquareOffRequest struct {
	TradeUID string `json:"trade_uid"`
	Reason   string `json:"reason"`
}

type manualSquareOffResponse struct {
	Success   bool          `json:"success"`
	Message   string        `json:"message"`
	TradeUID  string        `json:"trade_uid"`
	Results   []interface{} `json:"results,omitempty"`
	Error     string        `json:"error,omitempty"`
	Timestamp string        `json:"timestamp"`
}

// ManualSquareOffHandler handles the direct, non-position-aware square off of a trade.
func (h *Handlers) ManualSquareOffHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"message":"only POST method is allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 1. Extract Trade UID from URL path, e.g., /api/trade/{trade_uid}/manual-squareoff
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.Error(w, `{"message":"invalid URL path, expected /api/trade/{uid}/manual-squareoff"}`, http.StatusBadRequest)
		return
	}
	tradeUID := parts[len(parts)-2]

	var req manualSquareOffRequest
	// The request body is optional, but we'll try to parse it for a reason.
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.TradeUID = tradeUID // Ensure UID from URL is used

	slog.Info("Received manual square-off request", "trade_uid", req.TradeUID, "reason", req.Reason)

	// 2. Forward the request to the execution-gateway
	// This follows the pattern of other actions: the control-api is a proxy.
	execGWURL := fmt.Sprintf("%s/api/v1/trade/%s/manual-squareoff", h.Config.ExecutionGatewayBaseURL, req.TradeUID)

	reqBody, err := json.Marshal(req)
	if err != nil {
		http.Error(w, `{"message":"failed to create request for execution-gateway"}`, http.StatusInternalServerError)
		return
	}

	proxyReq, err := http.NewRequest(http.MethodPost, execGWURL, bytes.NewBuffer(reqBody))
	if err != nil {
		http.Error(w, `{"message":"failed to create proxy request"}`, http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	// Pass along any necessary auth headers if your services require them

	resp, err := h.Client.Do(proxyReq)
	if err != nil {
		slog.Error("Failed to call execution-gateway for manual square-off", "error", err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(manualSquareOffResponse{
			Success:   false,
			Message:   "Failed to connect to execution-gateway",
			TradeUID:  req.TradeUID,
			Error:     err.Error(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		return
	}
	defer resp.Body.Close()

	// 3. Proxy the response from execution-gateway back to the client
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Error("Failed to write response body for manual square-off", "error", err)
	}
}
