package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type ManualHedgeBrokerRequest struct {
	Direction    string  `json:"direction"`
	CESide       string  `json:"ce_side"`
	PESide       string  `json:"pe_side"`
	CEQuantity   int64   `json:"ce_quantity"`
	PEQuantity   int64   `json:"pe_quantity"`
	CELimitPrice float64 `json:"ce_limit_price"`
	PELimitPrice float64 `json:"pe_limit_price"`
}

type ManualHedgeBrokerResponse struct {
	Success   bool               `json:"success"`
	TradeUID  string             `json:"trade_uid"`
	Phase     string             `json:"phase"`
	DryRun    bool               `json:"dry_run"`
	Broker    string             `json:"broker"`
	AccountID string             `json:"account_id"`
	Results   []*ExecutionResult `json:"results,omitempty"`
	Error     string             `json:"error,omitempty"`
	Message   string             `json:"message,omitempty"`
}

func normalizeManualHedgeSide(side string) string {
	side = strings.ToUpper(strings.TrimSpace(side))
	switch side {
	case "BUY", "B":
		return "BUY"
	case "SELL", "S":
		return "SELL"
	default:
		return ""
	}
}

func defaultManualHedgeSides(direction string) (string, string, error) {
	direction = strings.ToUpper(strings.TrimSpace(direction))

	switch direction {
	case "NEGATIVE_DELTA", "NEGATIVE", "SHORT_DELTA":
		return "BUY", "SELL", nil
	case "POSITIVE_DELTA", "POSITIVE", "LONG_DELTA":
		return "SELL", "BUY", nil
	default:
		return "", "", fmt.Errorf("manual hedge requires direction NEGATIVE_DELTA or POSITIVE_DELTA when explicit sides are not supplied")
	}
}

func defaultManualHedgeQty(tr StoredTrade, leg string, requested int64) int64 {
	if requested > 0 {
		return requested
	}

	if leg == "CE" && tr.CEQty > 0 {
		return int64(tr.CEQty)
	}

	if leg == "PE" && tr.PEQty > 0 {
		return int64(tr.PEQty)
	}

	if tr.LotSize > 0 {
		return int64(tr.LotSize)
	}

	fallback := GetFallbackLotSize(tr.Symbol)
	if fallback > 0 {
		return int64(fallback)
	}

	return 0
}

func defaultManualHedgePrice(entry float64, requested float64) float64 {
	if requested > 0 {
		return requested
	}

	if entry > 0 {
		return entry
	}

	return 0.05
}

func defaultManualHedgeProduct(product string) string {
	product = strings.TrimSpace(product)
	if product == "" {
		return "NRML"
	}
	return product
}

func defaultManualHedgeExchangeSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "NSEFO"
	}
	return segment
}

func manualHedgeBrokerExecutionEnabled(tr StoredTrade) bool {
	if strings.EqualFold(tr.BrokerName, "SIM") {
		return true
	}

	return os.Getenv("ENABLE_MANUAL_BROKER_ACTIONS") == "1"
}

func (s *Service) ManualHedgeBrokerOrder(ctx context.Context, tradeUID string, req ManualHedgeBrokerRequest) ([]*ExecutionResult, bool, error) {
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return nil, false, fmt.Errorf("trade not found")
	}

	ceSide := normalizeManualHedgeSide(req.CESide)
	peSide := normalizeManualHedgeSide(req.PESide)

	if ceSide == "" || peSide == "" {
		defCE, defPE, err := defaultManualHedgeSides(req.Direction)
		if err != nil {
			return nil, false, err
		}

		if ceSide == "" {
			ceSide = defCE
		}
		if peSide == "" {
			peSide = defPE
		}
	}

	ceQty := defaultManualHedgeQty(tr, "CE", req.CEQuantity)
	peQty := defaultManualHedgeQty(tr, "PE", req.PEQuantity)

	if ceQty <= 0 || peQty <= 0 {
		return nil, false, fmt.Errorf("invalid hedge quantity ce=%d pe=%d", ceQty, peQty)
	}

	if tr.CEToken <= 0 || tr.PEToken <= 0 {
		return nil, false, fmt.Errorf("missing CE/PE tokens for trade %s", tradeUID)
	}

	now := time.Now()

	cePrice := defaultManualHedgePrice(tr.CELtp, req.CELimitPrice)
	pePrice := defaultManualHedgePrice(tr.PELtp, req.PELimitPrice)

	ceIntent := OrderIntent{
		IntentID:        BuildShortOrderUID(tr.Symbol, "HCE", now, 0),
		TradeUID:        tradeUID,
		Token:           tr.CEToken,
		Symbol:          tr.Symbol,
		ExchangeSegment: defaultManualHedgeExchangeSegment(tr.ExchangeSegment),
		Side:            ceSide,
		Quantity:        ceQty,
		OrderType:       "LIMIT",
		ProductType:     defaultManualHedgeProduct(tr.ProductType),
		LegType:         "CE",
		Phase:           "HEDGE",
		OrderUID:        BuildShortOrderUID(tr.Symbol, "HCE", now, 1),
		BrokerName:      tr.BrokerName,
		AccountID:       tr.AccountID,
		ExpectedPrice:   cePrice,
	}
	ceIntent.LimitPrice = &cePrice

	peIntent := OrderIntent{
		IntentID:        BuildShortOrderUID(tr.Symbol, "HPE", now, 2),
		TradeUID:        tradeUID,
		Token:           tr.PEToken,
		Symbol:          tr.Symbol,
		ExchangeSegment: defaultManualHedgeExchangeSegment(tr.ExchangeSegment),
		Side:            peSide,
		Quantity:        peQty,
		OrderType:       "LIMIT",
		ProductType:     defaultManualHedgeProduct(tr.ProductType),
		LegType:         "PE",
		Phase:           "HEDGE",
		OrderUID:        BuildShortOrderUID(tr.Symbol, "HPE", now, 3),
		BrokerName:      tr.BrokerName,
		AccountID:       tr.AccountID,
		ExpectedPrice:   pePrice,
	}
	peIntent.LimitPrice = &pePrice

	s.Store.AppendIntent(tradeUID, ceIntent)
	s.Store.AppendIntent(tradeUID, peIntent)

	if !manualHedgeBrokerExecutionEnabled(tr) {
		return []*ExecutionResult{
			{
				IntentID:      ceIntent.IntentID,
				Status:        "DRY_RUN",
				EventReason:   "MANUAL_BROKER_ACTIONS_DISABLED",
				RawResponse:   "set ENABLE_MANUAL_BROKER_ACTIONS=1 to execute non-SIM manual hedge broker orders",
				BrokerOrderID: "",
			},
			{
				IntentID:      peIntent.IntentID,
				Status:        "DRY_RUN",
				EventReason:   "MANUAL_BROKER_ACTIONS_DISABLED",
				RawResponse:   "set ENABLE_MANUAL_BROKER_ACTIONS=1 to execute non-SIM manual hedge broker orders",
				BrokerOrderID: "",
			},
		}, true, nil
	}

	if s.BrokerFactory == nil {
		return nil, false, fmt.Errorf("broker factory is nil")
	}

	executor, err := s.BrokerFactory.GetExecutor(tr.UserID, tr.BrokerName, tr.AccountID)
	if err != nil {
		return nil, false, err
	}

	intents := []OrderIntent{ceIntent, peIntent}
	results := make([]*ExecutionResult, 0, len(intents))

	for _, intent := range intents {
		res, err := executor.ExecuteOrderIntent(ctx, intent)
		if err != nil {
			return results, false, fmt.Errorf("manual hedge %s failed: %w", intent.LegType, err)
		}

		results = append(results, res)

		if updater, ok := s.Store.(interface {
			MarkOrderExecution(intentID string, brokerOrderID string, status string, filledQty int64, pendingQty int64, fillPrice float64, rawResponse string)
		}); ok {
			updater.MarkOrderExecution(intent.IntentID, res.BrokerOrderID, res.Status, res.FilledQty, res.PendingQty, res.FillPrice, res.RawResponse)
		} else if updater, ok := s.Store.(interface {
			MarkOrderSubmitted(intentID string, brokerOrderID string, status string, rawResponse string)
		}); ok {
			updater.MarkOrderSubmitted(intent.IntentID, res.BrokerOrderID, res.Status, res.RawResponse)
		}
	}

	return results, false, nil
}

func (h *Handlers) ManualHedgeBroker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ManualHedgeBrokerResponse{
			Success: false,
			Error:   "method not allowed",
		})
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualHedgeBrokerResponse{
			Success: false,
			Error:   "invalid trade path",
		})
		return
	}

	tradeUID := parts[2]

	var req ManualHedgeBrokerRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	results, dryRun, err := h.Service.ManualHedgeBrokerOrder(r.Context(), tradeUID, req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ManualHedgeBrokerResponse{
			Success:  false,
			TradeUID: tradeUID,
			Phase:    "HEDGE",
			Error:    err.Error(),
			DryRun:   dryRun,
		})
		return
	}

	tr, _ := h.Store.LoadTrade(tradeUID)

	_ = json.NewEncoder(w).Encode(ManualHedgeBrokerResponse{
		Success:   true,
		TradeUID:  tradeUID,
		Phase:     "HEDGE",
		DryRun:    dryRun,
		Broker:    tr.BrokerName,
		AccountID: tr.AccountID,
		Results:   results,
		Message:   "manual hedge broker order completed",
	})
}
