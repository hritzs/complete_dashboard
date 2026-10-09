package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Handlers struct {
	Service   *Service
	Store     Store
	Scheduler *BuildScheduler
}

func NewHandlers(service *Service, store Store) *Handlers {
	return &Handlers{
		Service:   service,
		Store:     store,
		Scheduler: NewBuildScheduler(service),
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
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed",
		})
		return
	}

	var req ConfigBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid request body: " + err.Error(),
		})
		return
	}

	if h.Scheduler == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "build scheduler is not initialized",
		})
		return
	}

	entryTime := strings.TrimSpace(req.EntryTime)
	if entryTime == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "entry_time is required for config build",
		})
		return
	}

	runAt, err := ParseTodayIST(entryTime, time.Now())
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// A past entry_time fires immediately instead of being discarded -- e.g.
	// requested 09:21:00 but this request is only processed at 09:21:30.
	now := time.Now().In(runAt.Location())
	requestedRunAt := runAt
	runAt, timeAlreadyPassed := EffectiveRunAt(runAt, now)
	if timeAlreadyPassed {
		log.Printf("[BUILD SCHEDULER] entry_time %s already passed (now %s) -- firing immediately for symbol=%s", entryTime, now.Format("15:04:05"), req.Symbol)
	}

	// The expiry must be chosen explicitly. It used to be inherited silently
	// from whatever the UI last had selected, which sold a 23-NOV-26 straddle
	// when the weekly expiry was intended.
	targetExpiry := strings.TrimSpace(req.TargetExpiry)
	if targetExpiry == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "target_expiry is required: choose the expiry explicitly (e.g. 22-SEP-26); the automated build never picks one for you",
		})
		return
	}
	// Check it exists for this symbol now, while no order exists, instead of
	// failing (or selling something else) at entry time.
	if h.Service != nil && h.Service.Snapshot != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		chain, cerr := h.Service.Snapshot.GetOptionChain(ctx, NormalizeSymbol(req.Symbol), targetExpiry)
		cancel()
		switch {
		case cerr != nil:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("expiry %s is not available for %s: %v", targetExpiry, req.Symbol, cerr),
			})
			return
		case chain == nil || !strings.EqualFold(strings.TrimSpace(chain.Expiry), targetExpiry):
			got := ""
			if chain != nil {
				got = chain.Expiry
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("requested expiry %s but the market data returned %q; not scheduling", targetExpiry, got),
			})
			return
		}
	}
	req.TargetExpiry = targetExpiry

	risk := &BuildRiskConfig{
		ExitTime:    req.ExitTime,
		SlBps:       req.SlBps,
		TpBps:       req.TpBps,
		BuyBuffer:   req.BuyBuffer,
		SellBuffer:  req.SellBuffer,
		HedgeDiv:    req.HedgeDiv,
		StraddleDiv: req.StraddleDiv,
		WingPct:     req.WingPct,
	}
	// Reject a bad exit time now, while no order exists, not at entry time.
	if err := risk.Validate(runAt); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
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
		ProductType:      req.ProductType,
		DeltaNeutral:     true,
		TargetExpiry:     req.TargetExpiry,
		OrderLotsPerCall: req.OrderLotsPerCall,
		Risk:             risk,
	}

	if dReq.Lots == 0 {
		dReq.Lots = req.Lots
	}

	if dReq.Lots <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "lots must be greater than zero",
		})
		return
	}

	job, err := h.Scheduler.Schedule(BuildSourceConfig, runAt, dReq)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	message := "Config build scheduled; no broker order has been sent yet. " +
		"Pending builds are kept in memory and are lost if the gateway restarts."
	if timeAlreadyPassed {
		message = fmt.Sprintf(
			"Requested entry_time %s had already passed (it is now %s); firing immediately instead of discarding it. ",
			requestedRunAt.Format("15:04:05"), now.Format("15:04:05"),
		) + message
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"status":               "SCHEDULED",
		"job_id":               job.ID,
		"source":               job.Source,
		"symbol":               dReq.Symbol,
		"lots":                 dReq.Lots,
		"entry_time":           job.RunAt.Format("15:04:05"),
		"requested_entry_time": requestedRunAt.Format("15:04:05"),
		"fired_immediately":    timeAlreadyPassed,
		"scheduled_at":         job.RunAt.Format(time.RFC3339),
		"expiry":               targetExpiry,
		"exit_time":            strings.TrimSpace(req.ExitTime),
		"sl_bps":               req.SlBps,
		"tp_bps":               req.TpBps,
		"not_applied":          notAppliedBuildFields(req),
		"message":              message,
	})
}

// ListScheduledBuilds returns the pending scheduled builds.
func (h *Handlers) ListScheduledBuilds(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	jobs := []map[string]interface{}{}
	if h.Scheduler != nil {
		for _, j := range h.Scheduler.List() {
			jobs = append(jobs, map[string]interface{}{
				"job_id":       j.ID,
				"source":       j.Source,
				"symbol":       j.Request.Symbol,
				"lots":         j.Request.Lots,
				"expiry":       j.Request.TargetExpiry,
				"broker_name":  j.Request.BrokerName,
				"account_id":   j.Request.AccountID,
				"run_at":       j.RunAt.Format(time.RFC3339),
				"exit_time":    riskExitTime(j.Request.Risk),
				"sl_bps":       riskSlBps(j.Request.Risk),
				"tp_bps":       riskTpBps(j.Request.Risk),
				"scheduled_at": j.CreatedAt.Format(time.RFC3339),
			})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "count": len(jobs), "jobs": jobs})
}

// CancelScheduledBuild cancels a pending build: POST {"job_id": "..."}.
func (h *Handlers) CancelScheduledBuild(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var body struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.JobID) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "job_id is required"})
		return
	}
	if h.Scheduler == nil || !h.Scheduler.Cancel(strings.TrimSpace(body.JobID)) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "no pending build with that job_id (unknown, already ran, or gateway restarted)"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "job_id": body.JobID, "status": "CANCELLED"})
}

func riskExitTime(r *BuildRiskConfig) string {
	if r == nil {
		return ""
	}
	return r.ExitTime
}

func riskSlBps(r *BuildRiskConfig) float64 {
	if r == nil {
		return 0
	}
	return r.SlBps
}

func riskTpBps(r *BuildRiskConfig) float64 {
	if r == nil {
		return 0
	}
	return r.TpBps
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

	// Without ?lots: hedge the position's complete live delta (nearest
	// whole lot, see ManualHedgeNow). ?lots=N is an explicit manual size,
	// e.g. to test the freeze-qty tranche logic.
	var lots int64
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("lots")); raw != "" {
		parsed, perr := strconv.Atoi(raw)
		if perr != nil || parsed <= 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("invalid lots %q: must be a positive integer", raw),
			})
			return
		}
		lots = int64(parsed)
		err = h.Service.ManualHedgeLots(context.Background(), tradeUID, parsed)
	} else {
		lots, err = h.Service.ManualHedgeNow(context.Background(), tradeUID)
		if err == nil && lots == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": fmt.Sprintf("nothing to hedge for %s: live net delta is under one lot", tradeUID),
			})
			return
		}
	}

	if err != nil {
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
		"message": fmt.Sprintf("Manual hedge for %s completed (%d lots)", tradeUID, lots),
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
	// Enrich trades with latest snapshot data for PointsOut/PointsAllowed
	for i := range trades {
		if snap, ok := h.Store.LoadSnapshot(trades[i].TradeUID); ok {
			trades[i].PointsOut = snap.PointsOut
			trades[i].PointsAllowed = snap.PointsAllowed
		}
	}
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
		if tr.Status == "ACTIVE" || tr.Status == "BUILDING" || tr.Status == "PARTIAL" || tr.Status == "RECONCILIATION_REQUIRED" {
			// Enrich with snapshot data
			if snap, ok := h.Store.LoadSnapshot(tr.TradeUID); ok {
				tr.PointsOut = snap.PointsOut
				tr.PointsAllowed = snap.PointsAllowed
			}
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

// ModifyTradeRequest holds fields that can be updated for an active trade
type ModifyTradeRequest struct {
	SLPointsPerLot *float64 `json:"sl_points_per_lot,omitempty"`

	// Minute-end normalized risk controls.
	SpotStopLossBps             *float64 `json:"spot_stop_loss_bps,omitempty"`
	TakeProfitPointsPerStraddle *float64 `json:"take_profit_points_per_straddle,omitempty"`
	AutoRiskExecutionEnabled    *bool    `json:"auto_risk_execution_enabled,omitempty"`

	// Autonomous bps-of-spot SL/TP, wired in runMonitorCycle (see
	// bpsOfSpotThreshold): exits via a real, verified SquareOff when
	// pnlPerStraddle crosses spot*bps/10,000 in either direction.
	SLPnLBpsOfSpot *float64 `json:"sl_pnl_bps_of_spot,omitempty"`
	TPPnLBpsOfSpot *float64 `json:"tp_pnl_bps_of_spot,omitempty"`

	// Autonomous time-based hard exit, wired in runMonitorCycle -- unlike
	// SquareOffTime below (still alert-only in tickRuntime), reaching
	// this time triggers a real, verified SquareOff(reason="TIME").
	SquareOffHardTime *string `json:"square_off_hard_time,omitempty"` // "15:15:00"

	StraddleDiv                  *float64 `json:"straddle_div,omitempty"`
	HedgeDiv                     *float64 `json:"hedge_div,omitempty"`
	HedgeThresholdDelta          *float64 `json:"hedge_threshold_delta,omitempty"`
	SquareOffTime                *string  `json:"square_off_time,omitempty"` // "15:37:00"
	ForceOneLotHedgeTest         *bool    `json:"force_one_lot_hedge_test,omitempty"`
	HedgePointsFloor             *float64 `json:"hedge_points_floor,omitempty"`
	ForceHedgeRegardlessOfPoints *bool    `json:"force_hedge_regardless_of_points,omitempty"`
	HedgeMinThresholdBps         *float64 `json:"hedge_min_threshold_bps,omitempty"`

	// MTM exit (mtm_exit.go): a level (may be negative) sets it; MTMExitOff
	// puts it back to infinity. Unit: rs (default) / pts / bps.
	MTMExitLevel *float64 `json:"mtm_exit_level,omitempty"`
	MTMExitUnit  *string  `json:"mtm_exit_unit,omitempty"`
	MTMExitOff   bool     `json:"mtm_exit_off,omitempty"`
	MTMExitPct   *float64 `json:"mtm_exit_pct,omitempty"` // % of the original position (0/100 = complete)

	// ATM-straddle exit (combined_sqf.go): close StraddleExitPct of the
	// original position when the live ATM straddle LTP < StraddleExitBelow.
	StraddleExitBelow *float64 `json:"straddle_exit_below,omitempty"`
	StraddleExitPct   *float64 `json:"straddle_exit_pct,omitempty"`
	StraddleExitOff   bool     `json:"straddle_exit_off,omitempty"`

	// Extra steps (exit_tiers.go): the full list each time (empty = none).
	MTMExitTiers      *[]ExitTier `json:"mtm_exit_tiers,omitempty"`
	StraddleExitTiers *[]ExitTier `json:"straddle_exit_tiers,omitempty"`
}

func (h *Handlers) ModifyTrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "trade" || parts[3] != "modify" {
		http.Error(w, "invalid modify trade path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]

	var req ModifyTradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

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

	// Only allow modification of active trades
	if tr.Status != "ACTIVE" && tr.Status != "BUILDING" && tr.Status != "PARTIAL" && tr.Status != "RECONCILIATION_REQUIRED" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "cannot modify closed trade",
		})
		return
	}

	before := tr.Config

	// Update config fields
	if req.SLPointsPerLot != nil {
		tr.Config.SLPointsPerLot = *req.SLPointsPerLot
	}

	if req.SpotStopLossBps != nil {
		if *req.SpotStopLossBps < 0 {
			http.Error(w, "spot_stop_loss_bps must be >= 0", http.StatusBadRequest)
			return
		}
		tr.Config.SpotStopLossBps = *req.SpotStopLossBps
	}

	if req.TakeProfitPointsPerStraddle != nil {
		if *req.TakeProfitPointsPerStraddle < 0 {
			http.Error(w, "take_profit_points_per_straddle must be >= 0", http.StatusBadRequest)
			return
		}
		tr.Config.TakeProfitPointsPerStraddle =
			*req.TakeProfitPointsPerStraddle
	}

	if req.AutoRiskExecutionEnabled != nil {
		tr.Config.AutoRiskExecutionEnabled = *req.AutoRiskExecutionEnabled
	}

	if req.SLPnLBpsOfSpot != nil {
		if *req.SLPnLBpsOfSpot < 0 {
			http.Error(w, "sl_pnl_bps_of_spot must be >= 0", http.StatusBadRequest)
			return
		}
		tr.Config.SLPnLBpsOfSpot = *req.SLPnLBpsOfSpot
	}

	if req.TPPnLBpsOfSpot != nil {
		if *req.TPPnLBpsOfSpot < 0 {
			http.Error(w, "tp_pnl_bps_of_spot must be >= 0", http.StatusBadRequest)
			return
		}
		tr.Config.TPPnLBpsOfSpot = *req.TPPnLBpsOfSpot
	}

	if req.MTMExitUnit != nil {
		switch u := strings.TrimSpace(*req.MTMExitUnit); u {
		case "", "rs", "pts", "bps":
			tr.Config.MTMExitUnit = u
		default:
			http.Error(w, "mtm_exit_unit must be rs, pts or bps", http.StatusBadRequest)
			return
		}
	}
	if req.MTMExitOff {
		tr.Config.MTMExitLevel = nil // back to infinity
	} else if req.MTMExitLevel != nil {
		if math.IsNaN(*req.MTMExitLevel) || math.IsInf(*req.MTMExitLevel, 0) {
			http.Error(w, "mtm_exit_level must be a finite number (use mtm_exit_off for infinity)", http.StatusBadRequest)
			return
		}
		v := *req.MTMExitLevel
		tr.Config.MTMExitLevel = &v
	}
	pctOK := func(p *float64, name string) bool {
		if p != nil && (math.IsNaN(*p) || *p < 0 || *p > 100) {
			http.Error(w, name+" must be 0-100 (0 or 100 = complete)", http.StatusBadRequest)
			return false
		}
		return true
	}
	if !pctOK(req.MTMExitPct, "mtm_exit_pct") || !pctOK(req.StraddleExitPct, "straddle_exit_pct") {
		return
	}
	if req.MTMExitPct != nil {
		tr.Config.MTMExitPct = *req.MTMExitPct
	}
	if req.StraddleExitOff {
		tr.Config.StraddleExitBelow = nil
	} else if req.StraddleExitBelow != nil {
		if math.IsNaN(*req.StraddleExitBelow) || *req.StraddleExitBelow <= 0 {
			http.Error(w, "straddle_exit_below must be > 0 (use straddle_exit_off to remove it)", http.StatusBadRequest)
			return
		}
		v := *req.StraddleExitBelow
		tr.Config.StraddleExitBelow = &v
	}
	if req.StraddleExitPct != nil {
		tr.Config.StraddleExitPct = *req.StraddleExitPct
	}
	if req.MTMExitTiers != nil {
		if err := validTiers(*req.MTMExitTiers, false, "MTM steps"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tr.Config.MTMExitTiers = mergeTiers(tr.Config.MTMExitTiers, *req.MTMExitTiers)
	}
	if req.StraddleExitTiers != nil {
		if err := validTiers(*req.StraddleExitTiers, true, "ATM straddle steps"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tr.Config.StraddleExitTiers = mergeTiers(tr.Config.StraddleExitTiers, *req.StraddleExitTiers)
	}

	if req.StraddleDiv != nil {
		tr.Config.StraddleDiv = *req.StraddleDiv
	}
	if req.HedgeDiv != nil {
		tr.Config.HedgeDiv = *req.HedgeDiv
	}
	if req.HedgeThresholdDelta != nil {
		tr.Config.HedgeThresholdDelta = *req.HedgeThresholdDelta
	}

	if req.ForceOneLotHedgeTest != nil {
		tr.Config.ForceOneLotHedgeTest = *req.ForceOneLotHedgeTest
		tr.Config.HedgeTestExecuted = false
	}

	if req.HedgePointsFloor != nil {
		tr.Config.HedgePointsFloor = *req.HedgePointsFloor
	}
	if req.SquareOffTime != nil && req.SquareOffHardTime == nil {
		// square_off_time used to set an alert-only time that never exited
		// (live 2026-09-30: 15:38 saved there, trades still exited at 15:37).
		// An exit time is an exit time: it now sets the real, verified exit.
		v := strings.TrimSpace(*req.SquareOffTime)
		req.SquareOffHardTime = &v
	}

	if req.ForceHedgeRegardlessOfPoints != nil {
		tr.Config.ForceHedgeRegardlessOfPoints = *req.ForceHedgeRegardlessOfPoints
		tr.Config.HedgeTestExecuted = false
	}

	if req.HedgeMinThresholdBps != nil {
		if *req.HedgeMinThresholdBps < 0 {
			http.Error(w, "hedge_min_threshold_bps must be >= 0", http.StatusBadRequest)
			return
		}
		v := *req.HedgeMinThresholdBps
		tr.Config.HedgeMinThresholdBps = &v
	}

	if req.SquareOffHardTime != nil {
		// Accepts "15:15:00" or "15:15" for convenience.
		layout := "15:04:05"
		if len(strings.TrimSpace(*req.SquareOffHardTime)) <= len("15:04") {
			layout = "15:04"
		}
		t, err := time.Parse(layout, *req.SquareOffHardTime)
		if err != nil {
			http.Error(w, "square_off_hard_time must be HH:MM or HH:MM:SS", http.StatusBadRequest)
			return
		}
		now := time.Now()
		tr.Config.SquareOffHardTime = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
	}

	changes := diffMonitorConfig(before, tr.Config)
	tr.Config.Modifications = append(append([]ConfigChange(nil), before.Modifications...), changes...)
	for _, c := range changes {
		log.Printf("[MODIFY] trade=%s %s: %s -> %s", tradeUID, c.Field, c.From, c.To)
	}
	if len(changes) == 0 {
		log.Printf("[MODIFY] trade=%s submitted with no effective change", tradeUID)
	}

	tr.LastUpdateTime = time.Now()
	h.Store.UpdateTrade(tr)
	log.Printf("[MODIFY] trade=%s status=%s hard_exit=%s square_off_time=%s sl_bps=%.2f tp_bps=%.2f straddle_div=%.2f hedge_div=%.2f",
		tradeUID, tr.Status, tr.Config.SquareOffHardTime.Format("15:04:05"), tr.Config.SquareOffTime.Format("15:04:05"),
		tr.Config.SLPnLBpsOfSpot, tr.Config.TPPnLBpsOfSpot, tr.Config.StraddleDiv, tr.Config.HedgeDiv)

	// Reload from DB and refresh runtime to ensure in-memory cache
	// matches the persisted state. This prevents stale configs after
	// modify and avoids needing a restart.
	if refreshed, ok := h.Store.LoadTrade(tradeUID); ok {
		if rt, ok := h.Store.LoadRuntime(tradeUID); ok {
			rt.Trade = refreshed
			h.Store.SaveRuntime(rt)
		}
		// Return the fresh DB version in the response
		tr = refreshed
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "trade updated",
		"trade":   tr,
	})
}

func (h *Handlers) ResetTradingData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed",
		})
		return
	}

	if strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_DB_RESET"))) != "true" {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "database reset is disabled; set ENABLE_DB_RESET=true temporarily",
		})
		return
	}

	var req struct {
		Confirmation string `json:"confirmation"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid request body",
		})
		return
	}

	if strings.TrimSpace(req.Confirmation) != "DELETE ALL TRADES" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "confirmation must be exactly DELETE ALL TRADES",
		})
		return
	}

	resetter, ok := h.Store.(interface {
		ResetTradingData() (map[string]int64, error)
	})
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "configured store does not support reset",
		})
		return
	}

	deleted, err := resetter.ResetTradingData()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "trading data cleared",
		"deleted": deleted,
	})
}

// ManualOrderRequest defines the request structure for manual order execution
// PortfolioToday lists the day's trades (open and closed) with what executed
// and each trade's realized PnL. GET /api/portfolio/today[?date=YYYY-MM-DD],
// dates in IST.
func (h *Handlers) PortfolioToday(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	pg, ok := h.Store.(*PostgresBackedStore)
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "postgres-backed store is unavailable"})
		return
	}

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}
	day := time.Now().In(loc)
	if raw := strings.TrimSpace(r.URL.Query().Get("date")); raw != "" {
		parsed, perr := time.ParseInLocation("2006-01-02", raw, loc)
		if perr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "date must be YYYY-MM-DD"})
			return
		}
		day = parsed
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	trades, err := pg.TradeSummaries(ctx, from, from.Add(24*time.Hour))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	var realized, wingRealized float64
	open, closed := 0, 0
	for _, t := range trades {
		realized += t.RealizedPnL
		wingRealized += t.WingRealizedPnL
		if closeReasonForStatus(t.Status) != "" {
			closed++
		} else {
			open++
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"date":    from.Format("2006-01-02"),
		"totals": map[string]interface{}{
			"trades":       len(trades),
			"open":         open,
			"closed":       closed,
			"realized_pnl": round2(realized),
			// Wings (margin-only legs), separate -- not in realized_pnl.
			"wing_realized_pnl": round2(wingRealized),
			"note":              "realized PnL is gross of brokerage and charges",
		},
		"trades": trades,
	})
}
