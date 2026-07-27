package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ExecutionOrderSummary struct {
	ID            int64   `json:"id"`
	TradeUID      string  `json:"trade_uid"`
	IntentID      string  `json:"intent_id"`
	OrderUID      string  `json:"order_uid"`
	BrokerOrderID string  `json:"broker_order_id"`
	BrokerName    string  `json:"broker_name"`
	AccountID     string  `json:"account_id"`
	Symbol        string  `json:"symbol"`
	Phase         string  `json:"phase"`
	LegType       string  `json:"leg_type"`
	Token         int64   `json:"token"`
	Side          string  `json:"side"`
	Quantity      int64   `json:"quantity"`
	Status        string  `json:"status"`
	FilledQty     int64   `json:"filled_qty"`
	PendingQty    int64   `json:"pending_qty"`
	AvgFillPrice  float64 `json:"avg_fill_price"`
	Lifecycle     string  `json:"lifecycle"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type ExecutionNetPosition struct {
	Symbol      string  `json:"symbol"`
	LegType     string  `json:"leg_type"`
	Token       int64   `json:"token"`
	NetQuantity int64   `json:"net_quantity"`
	AvgPrice    float64 `json:"avg_price"`
}

type ExecutionSummaryResponse struct {
	Success       bool                               `json:"success"`
	TradeUID      string                             `json:"trade_uid"`
	TradeStatus   string                             `json:"trade_status"`
	BrokerName    string                             `json:"broker_name"`
	AccountID     string                             `json:"account_id"`
	Symbol        string                             `json:"symbol"`
	Orders        []ExecutionOrderSummary            `json:"orders"`
	GroupedOrders map[string][]ExecutionOrderSummary `json:"grouped_orders"`
	Positions     []ExecutionNetPosition             `json:"positions"`
	Counts        map[string]int                     `json:"counts"`
}

func (h *Handlers) ExecutionSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, `{"success":false,"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	tradeUID := strings.TrimSpace(r.URL.Query().Get("trade_uid"))
	if tradeUID == "" {
		http.Error(w, `{"success":false,"error":"trade_uid is required"}`, http.StatusBadRequest)
		return
	}

	db := dbFromHandlerStore(h)
	if db == nil {
		http.Error(w, `{"success":false,"error":"postgres store is not available"}`, http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := ExecutionSummaryResponse{
		Success:       true,
		TradeUID:      tradeUID,
		GroupedOrders: map[string][]ExecutionOrderSummary{},
		Counts:        map[string]int{},
	}

	_ = db.QueryRowContext(ctx, `
		SELECT
			COALESCE(status, ''),
			COALESCE(broker_name, ''),
			COALESCE(account_id, ''),
			COALESCE(symbol, '')
		FROM trades
		WHERE trade_uid = $1
	`, tradeUID).Scan(&resp.TradeStatus, &resp.BrokerName, &resp.AccountID, &resp.Symbol)

	rows, err := db.QueryContext(ctx, `
		SELECT
			id,
			COALESCE(trade_uid, ''),
			COALESCE(intent_id, ''),
			COALESCE(order_uid, ''),
			COALESCE(broker_order_id, ''),
			COALESCE(broker_name, ''),
			COALESCE(account_id, ''),
			COALESCE(side, ''),
			COALESCE(quantity, 0),
			COALESCE(status, ''),
			COALESCE(filled_qty, 0),
			COALESCE(pending_qty, 0),
			COALESCE(avg_fill_price, 0),
			COALESCE(raw_broker_request::text, ''),
			created_at,
			updated_at
		FROM orders
		WHERE trade_uid = $1
		ORDER BY created_at ASC, id ASC
	`, tradeUID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":"query failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	positionMap := map[string]*ExecutionNetPosition{}

	for rows.Next() {
		var o ExecutionOrderSummary
		var rawReq string
		var createdAt time.Time
		var updatedAt time.Time

		if err := rows.Scan(
			&o.ID,
			&o.TradeUID,
			&o.IntentID,
			&o.OrderUID,
			&o.BrokerOrderID,
			&o.BrokerName,
			&o.AccountID,
			&o.Side,
			&o.Quantity,
			&o.Status,
			&o.FilledQty,
			&o.PendingQty,
			&o.AvgFillPrice,
			&rawReq,
			&createdAt,
			&updatedAt,
		); err != nil {
			http.Error(w, fmt.Sprintf(`{"success":false,"error":"scan failed: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		o.CreatedAt = createdAt.Format(time.RFC3339)
		o.UpdatedAt = updatedAt.Format(time.RFC3339)

		enrichOrderFromRawIntent(&o, rawReq)

		if o.Symbol == "" {
			o.Symbol = resp.Symbol
		}
		if o.Phase == "" {
			o.Phase = inferOrderPhase(o.IntentID, o.OrderUID)
		}
		if o.Phase == "" {
			o.Phase = "UNKNOWN"
		}
		o.Lifecycle = classifyOrderLifecycle(o.Status)

		resp.Orders = append(resp.Orders, o)
		resp.GroupedOrders[o.Phase] = append(resp.GroupedOrders[o.Phase], o)
		resp.Counts[o.Lifecycle]++

		if o.FilledQty > 0 && (o.Status == "FILLED" || o.Status == "SUCCESS") {
			key := fmt.Sprintf("%s|%s|%d", o.Symbol, o.LegType, o.Token)
			pos := positionMap[key]
			if pos == nil {
				pos = &ExecutionNetPosition{
					Symbol:  o.Symbol,
					LegType: o.LegType,
					Token:   o.Token,
				}
				positionMap[key] = pos
			}

			signedQty := o.FilledQty
			if strings.ToUpper(o.Side) == "SELL" {
				signedQty = -signedQty
			}

			oldAbs := absInt64(pos.NetQuantity)
			newAbs := oldAbs + absInt64(signedQty)
			if newAbs > 0 {
				pos.AvgPrice = ((pos.AvgPrice * float64(oldAbs)) + (o.AvgFillPrice * float64(absInt64(signedQty)))) / float64(newAbs)
			}
			pos.NetQuantity += signedQty
		}
	}

	for _, p := range positionMap {
		if p.NetQuantity != 0 {
			resp.Positions = append(resp.Positions, *p)
		}
	}

	json.NewEncoder(w).Encode(resp)
}

func dbFromHandlerStore(h *Handlers) *sql.DB {
	if h == nil || h.Store == nil {
		return nil
	}
	if pg, ok := h.Store.(*PostgresBackedStore); ok {
		return pg.DB()
	}
	return nil
}

func enrichOrderFromRawIntent(o *ExecutionOrderSummary, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return
	}

	o.Phase = firstStringFromMap(m, "phase", "Phase")
	o.LegType = firstStringFromMap(m, "leg_type", "LegType")
	o.Symbol = firstStringFromMap(m, "symbol", "Symbol")

	o.Token = int64(firstFloatFromMap(m, "token", "Token"))
}

func firstStringFromMap(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}
	return ""
}

func firstFloatFromMap(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch x := v.(type) {
			case float64:
				return x
			case int:
				return float64(x)
			case int64:
				return float64(x)
			case string:
				var f float64
				fmt.Sscanf(x, "%f", &f)
				return f
			}
		}
	}
	return 0
}

func inferOrderPhase(intentID string, orderUID string) string {
	s := strings.ToUpper(intentID + "_" + orderUID)

	switch {
	case strings.Contains(s, "HEDGE") || strings.Contains(s, "_HCE") || strings.Contains(s, "_HPE"):
		return "HEDGE"
	case strings.Contains(s, "SQF") || strings.Contains(s, "SQUARE") || strings.Contains(s, "_SCE") || strings.Contains(s, "_SPE"):
		return "SQUAREOFF"
	case strings.Contains(s, "PSQF") || strings.Contains(s, "PARTIAL"):
		return "PARTIAL_SQUAREOFF"
	case strings.Contains(s, "BUI") || strings.Contains(s, "BUILD"):
		return "BUILD"
	default:
		return "UNKNOWN"
	}
}

func classifyOrderLifecycle(status string) string {
	s := strings.ToUpper(strings.TrimSpace(status))

	switch s {
	case "FILLED", "SUCCESS":
		return "FILLED_OK"
	case "OPEN", "SUBMITTED", "PARTIALLY_FILLED":
		return "NOT_FILLED_YET"
	case "REJECTED", "CANCELLED", "CANCELED":
		return "TERMINAL_FAIL"
	case "":
		return "UNKNOWN"
	default:
		return "UNKNOWN"
	}
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
