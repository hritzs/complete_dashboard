package trading

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// GetTradeReconciliation is a READ-ONLY endpoint that aggregates verified
// broker order-book fills for one specific trade's CE and PE legs.
//
// IMPORTANT CAVEAT: fills are matched to this trade using
// (normalized token + strike + option type), NOT a locally persisted
// broker_order_id list. This is correct as long as only one active
// trade exists per (token, strike, option type) at a time. Once
// multiple overlapping trades can share the same contract, this must
// be upgraded to match by locally stored BrokerOrderID per intent.
// The response always reports "matching_method" so callers know which
// guarantee applies.
//
// This endpoint places no orders, cancels nothing, and does not change
// trade status, CEQty/PEQty, or any stored money field.
//
// Query params:
//
//	trade_uid  (required)
func (h *Handlers) GetTradeReconciliation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	tradeUID := strings.TrimSpace(r.URL.Query().Get("trade_uid"))
	if tradeUID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "trade_uid is required",
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

	executor, err := h.Service.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
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

	strikeStr := strconv.FormatFloat(float64(tr.Strike), 'f', -1, 64)

	ceFills := filterFillsForLeg(allFills, tr.CEToken, strikeStr, "CE")
	peFills := filterFillsForLeg(allFills, tr.PEToken, strikeStr, "PE")

	ceLeg := AggregateLegFromFills(ceFills, tr.CEToken)
	peLeg := AggregateLegFromFills(peFills, tr.PEToken)

	result := TradeReconciliation{
		TradeUID:    tradeUID,
		CE:          ceLeg,
		PE:          peLeg,
		RealizedPnL: ceLeg.RealizedPnL + peLeg.RealizedPnL,
		Verified:    true,
		Source:      "GREEKSOFT_ORDERBOOK",
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"trade_uid":       tradeUID,
		"matching_method": "token_strike_optiontype_best_effort",
		"warning":         "fills matched by token+strike+option_type, not by stored broker_order_id; do not rely on this for trades sharing a contract with another active trade",
		"ce_fill_count":   len(ceFills),
		"pe_fill_count":   len(peFills),
		"reconciliation":  result,
	})
}

func filterFillsForLeg(fills []BrokerFill, token int64, strikeStr string, optionType string) []BrokerFill {
	if token <= 0 {
		return nil
	}

	out := make([]BrokerFill, 0, len(fills))
	for _, f := range fills {
		if f.Token != token {
			continue
		}
		if !strings.EqualFold(f.OptionType, optionType) {
			continue
		}
		fStrikeStr := strconv.FormatFloat(f.Strike, 'f', -1, 64)
		if strikeStr != "" && fStrikeStr != strikeStr {
			continue
		}
		out = append(out, f)
	}
	return out
}
