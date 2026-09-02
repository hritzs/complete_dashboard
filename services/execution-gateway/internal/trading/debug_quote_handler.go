package trading

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (h *Handlers) DebugGreeksoftQuote(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed",
		})
		return
	}

	q := r.URL.Query()

	token, err := strconv.ParseInt(
		strings.TrimSpace(q.Get("token")),
		10,
		64,
	)
	if err != nil || token <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "positive token is required",
		})
		return
	}

	userID := strings.TrimSpace(q.Get("user_id"))
	brokerName := strings.ToUpper(strings.TrimSpace(q.Get("broker_name")))
	accountID := strings.TrimSpace(q.Get("account_id"))
	exchangeSegment := strings.ToUpper(strings.TrimSpace(q.Get("exchange_segment")))

	if userID == "" || brokerName == "" || accountID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "user_id, broker_name, and account_id are required",
		})
		return
	}

	if exchangeSegment == "" {
		exchangeSegment = "NSEFO"
	}

	if h.Service == nil || h.Service.BrokerFactory == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "broker factory is nil",
		})
		return
	}

	executor, err := h.Service.BrokerFactory.GetExecutor(
		userID,
		brokerName,
		accountID,
	)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	provider, ok := executor.(BestQuoteProvider)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "best quote provider is not implemented for this broker",
		})
		return
	}

	quote, err := provider.GetBestQuote(
		r.Context(),
		token,
		exchangeSegment,
	)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"source":           "GREEKSOFT_QUOTE",
		"token":            token,
		"exchange_segment": exchangeSegment,
		"best_bid":         quote.BestBid,
		"best_ask":         quote.BestAsk,
	})
}
