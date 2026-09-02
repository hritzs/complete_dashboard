package trading

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// DebugGreeksoftFills is a READ-ONLY diagnostic endpoint.
// It fetches the current broker order book (via the VerifiedFillsProvider
// interface so this package never imports a concrete broker package,
// avoiding an import cycle), normalizes verified fills, and returns them
// as JSON. It never places, cancels, or modifies any order, and it never
// marks a trade closed.
//
// Query params:
//
//	user_id      (defaults to "U001")
//	broker_name  (required, e.g. GREEKSOFT)
//	account_id   (required, e.g. 147)
//	token        (optional) filter to one normalized token
func (h *Handlers) DebugGreeksoftFills(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		userID = "U001"
	}
	brokerName := strings.TrimSpace(r.URL.Query().Get("broker_name"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))

	if brokerName == "" || accountID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "broker_name and account_id are required",
		})
		return
	}

	executor, err := h.Service.BrokerFactory.GetExecutor(userID, brokerName, accountID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	provider, ok := executor.(VerifiedFillsProvider)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "verified-fill reconciliation is not implemented for this broker executor",
		})
		return
	}

	fills, err := provider.GetVerifiedFills(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if tokenStr := strings.TrimSpace(r.URL.Query().Get("token")); tokenStr != "" {
		wantToken, convErr := strconv.ParseInt(tokenStr, 10, 64)
		filtered := make([]BrokerFill, 0, len(fills))
		if convErr == nil {
			for _, f := range fills {
				if f.Token == wantToken {
					filtered = append(filtered, f)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"source":  "GREEKSOFT_ORDERBOOK",
			"fills":   filtered,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"source":  "GREEKSOFT_ORDERBOOK",
		"fills":   fills,
	})
}

// ReconcileGreeksoftFills persists broker-confirmed fills for existing local
// orders only. It is intentionally manual and requires POST + confirm=true.
// It never places, modifies, cancels, or square-offs broker orders.
func (h *Handlers) ReconcileGreeksoftFills(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed; use POST",
		})
		return
	}

	if strings.TrimSpace(r.URL.Query().Get("confirm")) != "true" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "explicit confirmation required: add confirm=true",
		})
		return
	}

	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		userID = "U001"
	}

	brokerName := strings.TrimSpace(r.URL.Query().Get("broker_name"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))

	if brokerName == "" || accountID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "broker_name and account_id are required",
		})
		return
	}

	providerStore, ok := h.Store.(VerifiedFillPersistence)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "configured store does not support verified fill persistence",
		})
		return
	}

	executor, err := h.Service.BrokerFactory.GetExecutor(
		userID,
		brokerName,
		accountID,
	)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	provider, ok := executor.(VerifiedFillsProvider)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "verified-fill reconciliation is not implemented for this broker executor",
		})
		return
	}

	fills, err := provider.GetVerifiedFills(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	report, err := providerStore.PersistVerifiedFills(
		r.Context(),
		brokerName,
		accountID,
		fills,
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"report":  report,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"source":  "GREEKSOFT_ORDERBOOK",
		"report":  report,
	})
}
