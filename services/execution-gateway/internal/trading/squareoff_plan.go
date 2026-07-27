package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type SquareOffPlanLeg struct {
	Symbol      string  `json:"symbol"`
	LegType     string  `json:"leg_type"`
	Token       int64   `json:"token"`
	NetQuantity int64   `json:"net_quantity"`
	AvgPrice    float64 `json:"avg_price"`

	SquareOffSide     string `json:"squareoff_side"`
	SquareOffQuantity int64  `json:"squareoff_quantity"`
	LotSize           int64  `json:"lot_size"`
	Lots              int64  `json:"lots"`
	QuantityWarning   string `json:"quantity_warning,omitempty"`
}

type SquareOffPlanResponse struct {
	Success     bool               `json:"success"`
	TradeUID    string             `json:"trade_uid"`
	TradeStatus string             `json:"trade_status"`
	BrokerName  string             `json:"broker_name"`
	AccountID   string             `json:"account_id"`
	Symbol      string             `json:"symbol"`
	LotSize     int64              `json:"lot_size"`
	Legs        []SquareOffPlanLeg `json:"legs"`
	Message     string             `json:"message"`
}

// SquareOffPlan is read-only.
// It calculates net position from filled orders and returns the opposite orders required to close.
// It does not place, cancel, modify, or square off anything.
func (h *Handlers) SquareOffPlan(w http.ResponseWriter, r *http.Request) {
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

	resp := SquareOffPlanResponse{
		Success:  true,
		TradeUID: tradeUID,
	}

	var rawConfig sql.NullString

	_ = db.QueryRowContext(ctx, `
		SELECT
			COALESCE(status, ''),
			COALESCE(broker_name, ''),
			COALESCE(account_id, ''),
			COALESCE(symbol, ''),
			config
		FROM trades
		WHERE trade_uid = $1
	`, tradeUID).Scan(
		&resp.TradeStatus,
		&resp.BrokerName,
		&resp.AccountID,
		&resp.Symbol,
		&rawConfig,
	)

	resp.LotSize = lotSizeFromTradeConfig(rawConfig, resp.Symbol)

	rows, err := db.QueryContext(ctx, `
		SELECT
			COALESCE(intent_id, ''),
			COALESCE(order_uid, ''),
			COALESCE(side, ''),
			COALESCE(status, ''),
			COALESCE(filled_qty, 0),
			COALESCE(avg_fill_price, 0),
			COALESCE(raw_broker_request::text, '')
		FROM orders
		WHERE trade_uid = $1
		  AND COALESCE(filled_qty, 0) > 0
		  AND COALESCE(status, '') IN ('FILLED', 'SUCCESS')
		ORDER BY created_at ASC, id ASC
	`, tradeUID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":"query failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type accumulator struct {
		Symbol      string
		LegType     string
		Token       int64
		NetQuantity int64
		Value       float64
		AbsQty      int64
	}

	acc := map[string]*accumulator{}

	for rows.Next() {
		var intentID string
		var orderUID string
		var side string
		var status string
		var filledQty int64
		var avgPrice float64
		var rawReq string

		if err := rows.Scan(
			&intentID,
			&orderUID,
			&side,
			&status,
			&filledQty,
			&avgPrice,
			&rawReq,
		); err != nil {
			http.Error(w, fmt.Sprintf(`{"success":false,"error":"scan failed: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		if filledQty <= 0 {
			continue
		}

		o := ExecutionOrderSummary{
			IntentID:     intentID,
			OrderUID:     orderUID,
			Side:         side,
			Status:       status,
			FilledQty:    filledQty,
			AvgFillPrice: avgPrice,
			Symbol:       resp.Symbol,
		}
		enrichOrderFromRawIntent(&o, rawReq)

		if o.Symbol == "" {
			o.Symbol = resp.Symbol
		}

		key := fmt.Sprintf("%s|%s|%d", o.Symbol, o.LegType, o.Token)
		rowAcc := acc[key]
		if rowAcc == nil {
			rowAcc = &accumulator{
				Symbol:  o.Symbol,
				LegType: o.LegType,
				Token:   o.Token,
			}
			acc[key] = rowAcc
		}

		signedQty := filledQty
		if strings.ToUpper(side) == "SELL" {
			signedQty = -filledQty
		}

		rowAcc.NetQuantity += signedQty
		rowAcc.Value += float64(absInt64(signedQty)) * avgPrice
		rowAcc.AbsQty += absInt64(signedQty)
	}

	for _, a := range acc {
		if a.NetQuantity == 0 {
			continue
		}

		avg := 0.0
		if a.AbsQty > 0 {
			avg = a.Value / float64(a.AbsQty)
		}

		sqfSide := "SELL"
		if a.NetQuantity < 0 {
			sqfSide = "BUY"
		}

		closeQty := absInt64(a.NetQuantity)

		leg := SquareOffPlanLeg{
			Symbol:            a.Symbol,
			LegType:           a.LegType,
			Token:             a.Token,
			NetQuantity:       a.NetQuantity,
			AvgPrice:          avg,
			SquareOffSide:     sqfSide,
			SquareOffQuantity: closeQty,
			LotSize:           resp.LotSize,
		}

		if resp.LotSize > 0 {
			leg.Lots = closeQty / resp.LotSize
			if closeQty%resp.LotSize != 0 {
				leg.QuantityWarning = fmt.Sprintf("quantity %d is not an exact multiple of lot_size %d", closeQty, resp.LotSize)
			}
		} else {
			leg.QuantityWarning = "lot_size unavailable"
		}

		resp.Legs = append(resp.Legs, leg)
	}

	if len(resp.Legs) == 0 {
		resp.Message = "No filled net position found for this trade. Nothing to square off yet."
	} else {
		resp.Message = "Square-off plan generated from filled order quantities."
	}

	json.NewEncoder(w).Encode(resp)
}

func lotSizeFromTradeConfig(rawConfig sql.NullString, symbol string) int64 {
	if rawConfig.Valid && strings.TrimSpace(rawConfig.String) != "" {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(rawConfig.String), &m); err == nil {
			for _, key := range []string{"lot_size", "LotSize", "lotsize"} {
				if v, ok := m[key]; ok {
					if f := genericFloat(v); f > 0 {
						return int64(math.Round(f))
					}
				}
			}
		}
	}

	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "NIFTY":
		return 65
	case "BANKNIFTY":
		return 35
	case "FINNIFTY":
		return 65
	case "MIDCPNIFTY":
		return 120
	case "SENSEX":
		return 20
	default:
		return 0
	}
}

func genericFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	default:
		return 0
	}
}
