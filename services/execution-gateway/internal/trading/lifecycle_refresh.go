package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type RefreshTradeLifecycleResponse struct {
	Success   bool                      `json:"success"`
	TradeUID  string                    `json:"trade_uid"`
	OldStatus string                    `json:"old_status"`
	NewStatus string                    `json:"new_status"`
	Counts    BuildOrderLifecycleCounts `json:"counts"`
	Changed   bool                      `json:"changed"`
	Message   string                    `json:"message"`
}

func (h *Handlers) RefreshTradeLifecycle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost && r.Method != http.MethodGet {
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

	resp := RefreshTradeLifecycleResponse{
		Success:  true,
		TradeUID: tradeUID,
	}

	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(status, '')
		FROM trades
		WHERE trade_uid = $1
	`, tradeUID).Scan(&resp.OldStatus)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":"trade not found or status query failed: %s"}`, err.Error()), http.StatusNotFound)
		return
	}

	err = db.QueryRowContext(ctx, `
		WITH build_orders AS (
			SELECT
				COALESCE(status, '') AS status,
				COALESCE(filled_qty, 0) AS filled_qty,
				COALESCE(quantity, 0) AS quantity
			FROM orders
			WHERE trade_uid = $1
			  AND (
				intent_id LIKE 'BUI_%'
				OR order_uid LIKE 'BUI_%'
				OR UPPER(COALESCE(raw_broker_request::text, '')) LIKE '%"PHASE":"BUILD"%'
				OR UPPER(COALESCE(raw_broker_request::text, '')) LIKE '%"PHASE": "BUILD"%'
			  )
		)
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (
				WHERE status IN ('FILLED','SUCCESS')
				  AND filled_qty >= quantity
				  AND quantity > 0
			) AS filled,
			COUNT(*) FILTER (
				WHERE status IN ('OPEN','SUBMITTED','PARTIALLY_FILLED')
				  AND filled_qty < quantity
			) AS pending,
			COUNT(*) FILTER (
				WHERE status IN ('REJECTED','CANCELLED','CANCELED')
			) AS terminal_fail,
			COUNT(*) FILTER (
				WHERE status NOT IN (
					'FILLED','SUCCESS',
					'OPEN','SUBMITTED','PARTIALLY_FILLED',
					'REJECTED','CANCELLED','CANCELED'
				)
			) AS unknown
		FROM build_orders
	`, tradeUID).Scan(
		&resp.Counts.Total,
		&resp.Counts.Filled,
		&resp.Counts.Pending,
		&resp.Counts.TerminalFail,
		&resp.Counts.Unknown,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":"count query failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	resp.NewStatus = deriveTradeStatusFromBuildCounts(resp.Counts)

	_, err = db.ExecContext(ctx, `
		UPDATE trades
		SET status = $2
		WHERE trade_uid = $1
	`, tradeUID, resp.NewStatus)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"success":false,"error":"trade update failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	resp.Changed = strings.TrimSpace(resp.OldStatus) != strings.TrimSpace(resp.NewStatus)
	resp.Message = "trade lifecycle refreshed from build order rows"

	json.NewEncoder(w).Encode(resp)
}
