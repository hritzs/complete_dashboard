package trading

import (
	"encoding/json"
	"net/http"
	"strings"
)

type TradeSyncPreviewOrder struct {
	BrokerOrderID string  `json:"broker_order_id"`
	Tag           string  `json:"tag,omitempty"`
	UserTag       string  `json:"user_tag,omitempty"`
	Token         int64   `json:"token"`
	Side          string  `json:"side"`
	FilledQty     int64   `json:"filled_qty"`
	AveragePrice  float64 `json:"avg_fill_price"`
	Status        string  `json:"status"`
	OptionType    string  `json:"option_type"`
	Strike        float64 `json:"strike"`
	BrokerTime    string  `json:"broker_time,omitempty"`
}

func (h *Handlers) TradeSyncPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed",
		})
		return
	}

	tradeUID := tradeUIDFromTradeActionPath(r.URL.Path, "/sync-preview")
	if tradeUID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid trade sync-preview path",
		})
		return
	}

	tr, ok := h.Service.Store.LoadTrade(tradeUID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "trade not found",
		})
		return
	}

	executor, err := h.Service.BrokerFactory.GetExecutor(
		tr.UserID,
		tr.BrokerName,
		tr.AccountID,
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

	allFills, err := provider.GetVerifiedFills(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	matched := make([]BrokerFill, 0)
	orders := make([]TradeSyncPreviewOrder, 0)

	for _, fill := range allFills {
		tagMatch := strings.TrimSpace(fill.BrokerTag) == tradeUID
		userTagMatch := strings.TrimSpace(fill.BrokerUserTag) == tradeUID

		if !tagMatch && !userTagMatch {
			continue
		}

		matched = append(matched, fill)

		brokerTime := ""
		if !fill.BrokerTime.IsZero() {
			brokerTime = fill.BrokerTime.Format("2006-01-02T15:04:05Z07:00")
		}

		orders = append(orders, TradeSyncPreviewOrder{
			BrokerOrderID: fill.BrokerOrderID,
			Tag:           fill.BrokerTag,
			UserTag:       fill.BrokerUserTag,
			Token:         fill.Token,
			Side:          fill.Side,
			FilledQty:     fill.FilledQty,
			AveragePrice:  fill.AveragePrice,
			Status:        fill.Status,
			OptionType:    fill.OptionType,
			Strike:        fill.Strike,
			BrokerTime:    brokerTime,
		})
	}

	if len(matched) == 0 {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":         false,
			"trade_uid":       tradeUID,
			"matching_method": "broker_tag_exact",
			"error":           "no verified Greeksoft fills matched exact tag or userTag for this trade_uid",
		})
		return
	}

	ceLeg := AggregateLegFromFills(matched, tr.CEToken)
	peLeg := AggregateLegFromFills(matched, tr.PEToken)

	proposedStatus := "ACTIVE"
	if ceLeg.OpenQty == 0 && peLeg.OpenQty == 0 {
		proposedStatus = "CLOSED"
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":               true,
		"read_only":             true,
		"trade_uid":             tradeUID,
		"source":                "GREEKSOFT_ORDERBOOK",
		"matching_method":       "broker_tag_or_usertag_exact",
		"matched_order_count":   len(orders),
		"matched_orders":        orders,
		"ce":                    ceLeg,
		"pe":                    peLeg,
		"realized_pnl":          ceLeg.RealizedPnL + peLeg.RealizedPnL,
		"proposed_trade_status": proposedStatus,
		"requires_confirmation": true,
		"warning":               "read-only preview only: no local database state or broker order was changed",
	})
}

func tradeUIDFromTradeActionPath(path string, suffix string) string {
	path = strings.TrimSpace(path)

	if !strings.HasPrefix(path, "/api/trade/") ||
		!strings.HasSuffix(path, suffix) {
		return ""
	}

	uid := strings.TrimSuffix(path, suffix)
	uid = strings.TrimPrefix(uid, "/api/trade/")
	uid = strings.Trim(uid, "/")

	return uid
}
