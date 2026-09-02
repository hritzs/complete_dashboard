package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type ManualHedgeTestRequest struct {
	TradeUID        string `json:"trade_uid,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	BrokerName      string `json:"broker_name,omitempty"`
	AccountID       string `json:"account_id,omitempty"`
	Symbol          string `json:"symbol,omitempty"`
	Expiry          string `json:"expiry,omitempty"`
	ExchangeSegment string `json:"exchange_segment,omitempty"`
	ProductType     string `json:"product_type,omitempty"`

	NetDelta float64 `json:"net_delta"`
	LotSize  int     `json:"lot_size,omitempty"`

	Quantity  int64   `json:"quantity,omitempty"`
	ATMStrike float64 `json:"atm_strike,omitempty"`
	CEToken   int64   `json:"ce_token,omitempty"`
	PEToken   int64   `json:"pe_token,omitempty"`
	CELTP     float64 `json:"ce_ltp,omitempty"`
	PELTP     float64 `json:"pe_ltp,omitempty"`

	LimitBuffer float64 `json:"limit_buffer,omitempty"`
}

type ManualHedgeTestResponse struct {
	Success    bool                         `json:"success"`
	DryRun     bool                         `json:"dry_run"`
	TradeUID   string                       `json:"trade_uid"`
	Symbol     string                       `json:"symbol"`
	Expiry     string                       `json:"expiry"`
	NetDelta   float64                      `json:"net_delta"`
	LotSize    int                          `json:"lot_size"`
	Quantity   int64                        `json:"quantity"`
	CESide     string                       `json:"ce_side"`
	PESide     string                       `json:"pe_side"`
	ATMStrike  float64                      `json:"atm_strike"`
	Intents    []OrderIntent                `json:"intents,omitempty"`
	Message    string                       `json:"message,omitempty"`
	Error      string                       `json:"error,omitempty"`
	Executions []ManualHedgeExecutionResult `json:"executions,omitempty"`
}

func HedgeQuantityFromDeltaFloor(netDelta float64, lotSize int) int {
	if lotSize <= 0 {
		return 0
	}

	deltaMagnitude := math.Abs(netDelta)
	lotsToHedge := int(deltaMagnitude / float64(lotSize))
	if lotsToHedge <= 0 {
		return 0
	}

	return lotsToHedge * lotSize
}

func hedgeSidesFromSignedDelta(netDelta float64) (string, string, bool) {
	if math.Abs(netDelta) < 1 {
		return "", "", false
	}

	if netDelta < 0 {
		return "BUY", "SELL", true
	}

	return "SELL", "BUY", true
}

func manualHedgeTestProduct(product string) string {
	product = strings.ToUpper(strings.TrimSpace(product))
	if product == "" {
		return "NRML"
	}
	return product
}

func manualHedgeTestExchangeSegment(segment string, symbol string) string {
	segment = strings.ToUpper(strings.TrimSpace(segment))
	if segment != "" {
		return segment
	}

	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	switch symbol {
	case "SENSEX", "BANKEX":
		return "BSEFO"
	default:
		return "NSEFO"
	}
}

func (s *Service) ManualHedgeTest(ctx context.Context, req ManualHedgeTestRequest) (*ManualHedgeTestResponse, error) {
	req.TradeUID = strings.TrimSpace(req.TradeUID)

	var tr StoredTrade
	hasTrade := false

	if req.TradeUID != "" {
		if loaded, ok := s.Store.LoadTrade(req.TradeUID); ok {
			tr = loaded
			hasTrade = true
		}
	}

	if hasTrade {
		if req.UserID == "" {
			req.UserID = tr.UserID
		}
		if req.BrokerName == "" {
			req.BrokerName = tr.BrokerName
		}
		if req.AccountID == "" {
			req.AccountID = tr.AccountID
		}
		if req.Symbol == "" {
			req.Symbol = tr.Symbol
		}
		if req.Expiry == "" {
			req.Expiry = tr.Expiry
		}
		if req.ExchangeSegment == "" {
			req.ExchangeSegment = tr.ExchangeSegment
		}
		if req.ProductType == "" {
			req.ProductType = tr.ProductType
		}
		if req.LotSize <= 0 {
			req.LotSize = tr.LotSize
		}
	}

	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	req.BrokerName = strings.ToUpper(strings.TrimSpace(req.BrokerName))
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.UserID = strings.TrimSpace(req.UserID)

	if req.TradeUID == "" {
		req.TradeUID = "MANUAL_HEDGE_TEST_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	if req.Symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	if req.LotSize <= 0 {
		req.LotSize = GetFallbackLotSize(req.Symbol)
	}
	if req.LotSize <= 0 {
		return nil, fmt.Errorf("invalid lot_size for symbol %s", req.Symbol)
	}

	qty := HedgeQuantityFromDeltaFloor(req.NetDelta, req.LotSize)
	if req.Quantity > 0 {
		qty = int(req.Quantity)
	}

	ceSide, peSide, shouldHedge := hedgeSidesFromSignedDelta(req.NetDelta)
	if !shouldHedge || qty <= 0 {
		return nil, fmt.Errorf("normalized quantity is 0 after floor-to-lot rounding")
	}

	if req.CEToken <= 0 || req.PEToken <= 0 || req.ATMStrike <= 0 {
		if s.Snapshot == nil {
			return nil, fmt.Errorf("snapshot client is nil and CE/PE tokens were not supplied")
		}
		if req.Expiry == "" {
			return nil, fmt.Errorf("expiry is required when tokens are not supplied")
		}

		chain, err := s.Snapshot.GetOptionChain(ctx, req.Symbol, req.Expiry)
		if err != nil {
			return nil, err
		}

		atmRow, err := FindATMRow(*chain)
		if err != nil {
			return nil, err
		}

		req.ATMStrike = atmRow.Strike
		req.CEToken = atmRow.CEToken
		req.PEToken = atmRow.PEToken
		req.CELTP = atmRow.CELtp
		req.PELTP = atmRow.PELtp
	}

	if req.CEToken <= 0 || req.PEToken <= 0 {
		return nil, fmt.Errorf("missing CE/PE tokens for manual hedge test")
	}

	buffer := req.LimitBuffer
	if buffer <= 0 {
		buffer = 2.0
	}

	ceLimit := 0.05
	if req.CELTP > 0 {
		if ceSide == "BUY" {
			ceLimit = req.CELTP + buffer
		} else {
			ceLimit = math.Max(0.05, req.CELTP-buffer)
		}
	}

	peLimit := 0.05
	if req.PELTP > 0 {
		if peSide == "BUY" {
			peLimit = req.PELTP + buffer
		} else {
			peLimit = math.Max(0.05, req.PELTP-buffer)
		}
	}

	now := time.Now()
	exchangeSegment := manualHedgeTestExchangeSegment(req.ExchangeSegment, req.Symbol)
	productType := manualHedgeTestProduct(req.ProductType)

	ceIntent := OrderIntent{
		IntentID:        BuildShortOrderUID(req.Symbol, "HTCE", now, 0),
		TradeUID:        req.TradeUID,
		Token:           req.CEToken,
		Symbol:          req.Symbol,
		ExchangeSegment: exchangeSegment,
		Side:            ceSide,
		Quantity:        int64(qty),
		OrderType:       "LIMIT",
		LimitPrice:      &ceLimit,
		ProductType:     productType,
		LegType:         "CE",
		Phase:           "HEDGE",
		OrderUID:        BuildShortOrderUID(req.Symbol, "HTCE", now, 1),
		BrokerName:      req.BrokerName,
		AccountID:       req.AccountID,
		ExpectedPrice:   req.CELTP,
	}

	peIntent := OrderIntent{
		IntentID:        BuildShortOrderUID(req.Symbol, "HTPE", now, 2),
		TradeUID:        req.TradeUID,
		Token:           req.PEToken,
		Symbol:          req.Symbol,
		ExchangeSegment: exchangeSegment,
		Side:            peSide,
		Quantity:        int64(qty),
		OrderType:       "LIMIT",
		LimitPrice:      &peLimit,
		ProductType:     productType,
		LegType:         "PE",
		Phase:           "HEDGE",
		OrderUID:        BuildShortOrderUID(req.Symbol, "HTPE", now, 3),
		BrokerName:      req.BrokerName,
		AccountID:       req.AccountID,
		ExpectedPrice:   req.PELTP,
	}

	intents := []OrderIntent{ceIntent, peIntent}

	if hasTrade {
		s.Store.AppendIntent(req.TradeUID, ceIntent)
		s.Store.AppendIntent(req.TradeUID, peIntent)
	}

	return &ManualHedgeTestResponse{
		Success:   true,
		DryRun:    true,
		TradeUID:  req.TradeUID,
		Symbol:    req.Symbol,
		Expiry:    req.Expiry,
		NetDelta:  req.NetDelta,
		LotSize:   req.LotSize,
		Quantity:  int64(qty),
		CESide:    ceSide,
		PESide:    peSide,
		ATMStrike: req.ATMStrike,
		Intents:   intents,
		Message:   "manual hedge test generated intents only; execution disabled",
	}, nil
}

type ManualHedgeExecutionResult struct {
	IntentID      string `json:"intent_id"`
	LegType       string `json:"leg_type"`
	Side          string `json:"side"`
	Token         int64  `json:"token"`
	Quantity      int64  `json:"quantity"`
	Success       bool   `json:"success"`
	BrokerOrderID string `json:"broker_order_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
	RawResponse   string `json:"raw_response,omitempty"`
}

func (s *Service) ManualHedgeExecute(ctx context.Context, req ManualHedgeTestRequest) (*ManualHedgeTestResponse, error) {
	if req.UserID == "" {
		req.UserID = "U001"
	}

	// Standalone execute requires broker/account in body.
	// Trade-scoped execute hydrates broker/account/symbol/expiry/lot_size from stored trade.
	if req.TradeUID == "" {
		if req.BrokerName == "" {
			return nil, fmt.Errorf("broker_name is required")
		}
		if req.AccountID == "" {
			return nil, fmt.Errorf("account_id is required")
		}
	}

	resp, err := s.ManualHedgeTest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp.DryRun = false
	resp.Message = "manual hedge execution completed"

	if resp.Quantity <= 0 {
		resp.Message = "manual hedge skipped because calculated quantity is zero"
		return resp, nil
	}

	gate := strings.TrimSpace(strings.ToLower(os.Getenv("ENABLE_MANUAL_BROKER_ACTIONS")))
	if gate != "true" && gate != "1" {
		executions := make([]ManualHedgeExecutionResult, 0, len(resp.Intents))
		for _, intent := range resp.Intents {
			executions = append(executions, ManualHedgeExecutionResult{
				IntentID: intent.IntentID,
				LegType:  intent.LegType,
				Side:     intent.Side,
				Token:    intent.Token,
				Quantity: intent.Quantity,
				Success:  false,
				Status:   "DRY_RUN_BLOCKED",
				Error:    "manual broker hedge execution disabled; set ENABLE_MANUAL_BROKER_ACTIONS=true to enable",
			})
		}

		resp.Success = false
		resp.Error = "manual broker hedge execution disabled; set ENABLE_MANUAL_BROKER_ACTIONS=true to enable"
		resp.Message = "manual hedge execution blocked by safety gate"
		resp.Executions = executions
		return resp, nil
	}

	brokerName := req.BrokerName
	accountID := req.AccountID
	if brokerName == "" && len(resp.Intents) > 0 {
		brokerName = resp.Intents[0].BrokerName
	}
	if accountID == "" && len(resp.Intents) > 0 {
		accountID = resp.Intents[0].AccountID
	}

	executor, err := s.BrokerFactory.GetExecutor(req.UserID, brokerName, accountID)
	if err != nil {
		return nil, err
	}

	executions := make([]ManualHedgeExecutionResult, 0, len(resp.Intents))
	allOK := true

	for _, intent := range resp.Intents {
		item := ManualHedgeExecutionResult{
			IntentID: intent.IntentID,
			LegType:  intent.LegType,
			Side:     intent.Side,
			Token:    intent.Token,
			Quantity: intent.Quantity,
		}

		res, err := executor.ExecuteOrderIntent(ctx, intent)
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			allOK = false
			executions = append(executions, item)
			continue
		}

		item.Success = true
		item.BrokerOrderID = res.BrokerOrderID
		item.Status = res.Status
		item.RawResponse = res.RawResponse

		if updater, ok := s.Store.(interface {
			MarkOrderExecution(
				intentID string,
				brokerOrderID string,
				status string,
				filledQty int64,
				pendingQty int64,
				fillPrice float64,
				rawResponse string,
			)
		}); ok {
			updater.MarkOrderExecution(
				intent.IntentID,
				res.BrokerOrderID,
				res.Status,
				res.FilledQty,
				0,
				res.FillPrice,
				res.RawResponse,
			)
		} else if updater, ok := s.Store.(interface {
			MarkOrderSubmitted(
				intentID string,
				brokerOrderID string,
				status string,
				rawResponse string,
			)
		}); ok {
			updater.MarkOrderSubmitted(
				intent.IntentID,
				res.BrokerOrderID,
				res.Status,
				res.RawResponse,
			)
		}

		executions = append(executions, item)
	}

	if !allOK {
		resp.Success = false
		resp.Error = "one or more hedge legs failed"
		resp.Message = "manual hedge execution completed with errors"
	}

	resp.Executions = executions
	return resp, nil
}

func (h *Handlers) ManualHedgeExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed",
		})
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	var req ManualHedgeTestRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "trade" {
		req.TradeUID = parts[2]
	}

	resp, err := h.Service.ManualHedgeExecute(r.Context(), req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualHedgeTestResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handlers) ManualHedgeTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "method not allowed",
		})
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	var req ManualHedgeTestRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "trade" {
		req.TradeUID = parts[2]
	}

	resp, err := h.Service.ManualHedgeTest(r.Context(), req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualHedgeTestResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(resp)
}
