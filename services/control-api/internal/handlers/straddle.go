package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"trading-platform/services/control-api/internal/session"
)

type optionChainResponse struct {
	Success bool            `json:"success"`
	Data    optionChainData `json:"data"`
	Error   string          `json:"error"`
}

type optionChainData struct {
    Symbol            string           `json:"symbol"`
    SyntheticFuture   float64          `json:"synthetic_future"`
    FutureLtp         float64          `json:"future_ltp"`
    ATM               float64          `json:"atm"`
    Expiry            string           `json:"expiry"`
    AvailableExpiries []string         `json:"available_expiries,omitempty"`
    LotSize           int              `json:"lot_size"`
    Chain             []optionChainRow `json:"chain"`
}

type optionChainRow struct {
    Strike  float64 `json:"strike"`
    CEToken int64   `json:"ce_token"`
    PEToken int64   `json:"pe_token"`
    CELtp   float64 `json:"ce_ltp"`
    PELtp   float64 `json:"pe_ltp"`
    CEDelta float64 `json:"ce_delta"`
    PEDelta float64 `json:"pe_delta"`
    IsATM   bool    `json:"is_atm"`
}

type sellStraddleRequest struct {
	UserID        string `json:"userId,omitempty"`
	BrokerName    string `json:"brokerName"`
	Symbol        string `json:"symbol"`
	Lots          int    `json:"lots"`
	DeltaNeutral  bool   `json:"deltaNeutral"`
	ProductType   string `json:"productType"`
	TargetExpiry  string `json:"targetExpiry"`
	CEStrikePrice int    `json:"ceStrikePrice"`
	PEStrikePrice int    `json:"peStrikePrice"`
	CEToken       int64  `json:"ceToken"`
	PEToken       int64  `json:"peToken"`
	LotSize       int    `json:"lotSize"`
	ExecutionMode string `json:"executionMode,omitempty"`
}

type orderResult struct {
	Leg      string                 `json:"leg"`
	Token    int64                  `json:"token"`
	Quantity int                    `json:"quantity"`
	Price    float64                `json:"price"`
	Response map[string]interface{} `json:"response,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

type sellStraddleResponse struct {
	Success      bool          `json:"success"`
	Status       string        `json:"status"`
	Message      string        `json:"message,omitempty"`
	Symbol       string        `json:"symbol,omitempty"`
	Expiry       string        `json:"expiry,omitempty"`
	Strike       float64       `json:"strike,omitempty"`
	CEToken      int64         `json:"ce_token,omitempty"`
	PEToken      int64         `json:"pe_token,omitempty"`
	CEQuantity   int           `json:"ce_quantity,omitempty"`
	PEQuantity   int           `json:"pe_quantity,omitempty"`
	CEEntryPrice float64       `json:"ce_entry_price,omitempty"`
	PEEntryPrice float64       `json:"pe_entry_price,omitempty"`
	NetDelta     float64       `json:"net_delta,omitempty"`
	Results      []orderResult `json:"results,omitempty"`
	Error        string        `json:"error,omitempty"`
	CreatedAt    string        `json:"created_at,omitempty"`
}

var symbolDetails = map[string]struct {
	Token   int
	Gap     int
	LotSize int
}{
	"NIFTY":      {Token: 26000, Gap: 50, LotSize: 50},
	"BANKNIFTY":  {Token: 26001, Gap: 100, LotSize: 15},
	"FINNIFTY":   {Token: 26034, Gap: 50, LotSize: 40},
	"MIDCPNIFTY": {Token: 26121, Gap: 25, LotSize: 75},
	"SENSEX":     {Token: 26065, Gap: 100, LotSize: 10},
	"BANKEX":     {Token: 26118, Gap: 100, LotSize: 15},
}

func inferExchangeSegment(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	switch symbol {
	case "SENSEX", "BANKEX":
		return "BSEFO"
	default:
		return "NSEFO"
	}
}

func (h *Handlers) SellStraddleHandler(w http.ResponseWriter, r *http.Request) {
	sess, _, err := h.getSessionFromCookie(r)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message":"%v"}`, err), http.StatusUnauthorized)
		return
	}

	var req sellStraddleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	req.UserID = strings.TrimSpace(req.UserID)
	req.BrokerName = strings.ToUpper(strings.TrimSpace(req.BrokerName))
	req.ProductType = strings.ToUpper(strings.TrimSpace(req.ProductType))
	req.TargetExpiry = strings.TrimSpace(req.TargetExpiry)
	req.ExecutionMode = strings.ToUpper(strings.TrimSpace(req.ExecutionMode))

	if req.Symbol == "" {
		http.Error(w, `{"message":"symbol is required"}`, http.StatusBadRequest)
		return
	}
	if req.BrokerName == "" {
		req.BrokerName = "XTS"
	}
	if req.Lots <= 0 {
		req.Lots = 1
	}
	if req.ProductType == "" {
		req.ProductType = "MIS"
	}
	if req.UserID == "" {
		req.UserID = strings.TrimSpace(sess.Username)
		if req.UserID == "" {
			req.UserID = "U001"
		}
	}

	accountID := strings.TrimSpace(sess.GCID)
	if accountID == "" && req.BrokerName == "XTS" {
		http.Error(w, `{"message":"JLogin required before trading"}`, http.StatusBadRequest)
		return
	}

	exchangeSegment := inferExchangeSegment(req.Symbol)

	slog.Info("Received sell straddle request",
		"user", req.UserID,
		"session_user", sess.Username,
		"broker", req.BrokerName,
		"account_id", accountID,
		"exchange_segment", exchangeSegment,
		"symbol", req.Symbol,
		"lots", req.Lots,
		"target_expiry", req.TargetExpiry,
		"delta_neutral", req.DeltaNeutral,
	)

	chain, err := h.fetchATMData(req.Symbol, req.TargetExpiry)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(sellStraddleResponse{
			Success: false,
			Status:  "error",
			Error:   fmt.Sprintf("failed to fetch option chain: %v", err),
		})
		return
	}

	slog.Info("Chain snapshot from snapshot-service",
		"symbol", chain.Symbol,
		"expiry", chain.Expiry,
		"synthetic_future", chain.SyntheticFuture,
		"future_ltp", chain.FutureLtp,
		"atm", chain.ATM,
	)

	var ceRow, peRow *optionChainRow
	var selectedStrike float64

	useCustom := req.CEStrikePrice > 0 || req.PEStrikePrice > 0
	if useCustom {
		if req.CEStrikePrice <= 0 || req.PEStrikePrice <= 0 {
			http.Error(w, `{"message":"both ceStrikePrice and peStrikePrice are required for custom sell"}`, http.StatusBadRequest)
			return
		}

		ceRow, err = findRowByStrike(chain, req.CEStrikePrice)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"message":"CE custom strike lookup failed: %v"}`, err), http.StatusBadRequest)
			return
		}
		peRow, err = findRowByStrike(chain, req.PEStrikePrice)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"message":"PE custom strike lookup failed: %v"}`, err), http.StatusBadRequest)
			return
		}

		if req.CEStrikePrice == req.PEStrikePrice {
			selectedStrike = float64(req.CEStrikePrice)
		} else {
			selectedStrike = (float64(req.CEStrikePrice) + float64(req.PEStrikePrice)) / 2
		}
	} else {
		atmRow, err := findATMRow(chain)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"message":"ATM row not found: %v"}`, err), http.StatusBadRequest)
			return
		}
		ceRow = atmRow
		peRow = atmRow
		selectedStrike = atmRow.Strike
	}

	ceToken := req.CEToken
	peToken := req.PEToken
	if ceToken == 0 && ceRow != nil {
		ceToken = ceRow.CEToken
	}
	if peToken == 0 && peRow != nil {
		peToken = peRow.PEToken
	}
	if ceToken == 0 || peToken == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(sellStraddleResponse{
			Success: false,
			Status:  "error",
			Error:   "CE/PE tokens not available",
		})
		return
	}

	lotSize := req.LotSize
	if lotSize == 0 && chain.LotSize > 0 {
		lotSize = chain.LotSize
	}
	if lotSize == 0 {
		if ls, err := h.getLotSize(req.Symbol, chain.Expiry); err == nil && ls > 0 {
			lotSize = ls
		}
	}
	if lotSize == 0 {
		if details, ok := symbolDetails[req.Symbol]; ok && details.LotSize > 0 {
			lotSize = details.LotSize
		} else {
			lotSize = 1
		}
	}

	ceLots := req.Lots
	peLots := req.Lots

	ceDelta := 0.0
	peDelta := 0.0

	if ceRow != nil {
		ceDelta = ceRow.CEDelta
	}
	if peRow != nil {
		peDelta = peRow.PEDelta
	}

	if req.DeltaNeutral && ceDelta != 0 && peDelta != 0 {
		ratio := math.Abs(ceDelta / peDelta)
		peLots = int(math.Round(float64(ceLots) * ratio))
		if peLots < 1 {
			peLots = 1
		}
	}

	ceQty := ceLots * lotSize
	peQty := peLots * lotSize
	netDelta := float64(ceQty)*ceDelta + float64(peQty)*peDelta

	ceBid, err := h.getBestBidPrice(sess.UpstreamToken, sess.GCID, int(ceToken))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message":"Failed to get CE bid: %v"}`, err), http.StatusBadGateway)
		return
	}
	peBid, err := h.getBestBidPrice(sess.UpstreamToken, sess.GCID, int(peToken))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message":"Failed to get PE bid: %v"}`, err), http.StatusBadGateway)
		return
	}

	var wg sync.WaitGroup
	resultsCh := make(chan orderResult, 2)

	if ceQty > 0 {
		wg.Add(1)
		go h.placeOrder(&wg, resultsCh, sess, exchangeSegment, "CE", ceQty, ceToken, "SELL", ceBid, req.ProductType)
	}
	if peQty > 0 {
		wg.Add(1)
		go h.placeOrder(&wg, resultsCh, sess, exchangeSegment, "PE", peQty, peToken, "SELL", peBid, req.ProductType)
	}

	wg.Wait()
	close(resultsCh)

	var finalResults []orderResult
	okAll := true
	for res := range resultsCh {
		if res.Error != "" {
			okAll = false
		}
		finalResults = append(finalResults, res)
	}

	status := "submitted"
	msg := "sell straddle orders submitted"
	if !okAll {
		status = "partial"
		msg = "one or more legs failed"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(sellStraddleResponse{
		Success:      okAll,
		Status:       status,
		Message:      msg,
		Symbol:       req.Symbol,
		Expiry:       chain.Expiry,
		Strike:       selectedStrike,
		CEToken:      ceToken,
		PEToken:      peToken,
		CEQuantity:   ceQty,
		PEQuantity:   peQty,
		CEEntryPrice: ceBid,
		PEEntryPrice: peBid,
		NetDelta:     netDelta,
		Results:      finalResults,
		CreatedAt:    time.Now().Format(time.RFC3339),
	})
}

func (h *Handlers) fetchATMData(symbol, targetExpiry string) (optionChainData, error) {
	u := fmt.Sprintf("%s/api/option-chain/%s", h.Config.SnapshotServiceBaseURL, url.PathEscape(symbol))
	if strings.TrimSpace(targetExpiry) != "" {
		u += "?expiry=" + url.QueryEscape(targetExpiry)
	}

	res, err := h.Client.Get(u)
	if err != nil {
		return optionChainData{}, fmt.Errorf("failed to connect to snapshot service: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return optionChainData{}, fmt.Errorf("snapshot service returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	var out optionChainResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return optionChainData{}, fmt.Errorf("failed to parse option chain JSON: %w", err)
	}
	if !out.Success {
		if out.Error != "" {
			return optionChainData{}, fmt.Errorf(out.Error)
		}
		return optionChainData{}, fmt.Errorf("snapshot service did not return success")
	}
	return out.Data, nil
}

func findATMRow(chain optionChainData) (*optionChainRow, error) {
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if row.IsATM || row.Strike == chain.ATM {
			return row, nil
		}
	}
	return nil, fmt.Errorf("ATM row not found")
}

func findRowByStrike(chain optionChainData, strike int) (*optionChainRow, error) {
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if int(math.Round(row.Strike)) == strike {
			return row, nil
		}
	}
	return nil, fmt.Errorf("strike %d not found in chain", strike)
}

func (h *Handlers) getLotSize(symbol, expiry string) (int, error) {
	u := fmt.Sprintf("%s/api/lot-size/%s?expiry=%s", h.Config.ContractMasterBaseURL, url.PathEscape(symbol), url.QueryEscape(expiry))
	res, err := h.Client.Get(u)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	var body struct {
		Success bool   `json:"success"`
		LotSize int    `json:"lotsize"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return 0, err
	}
	if !body.Success || body.LotSize <= 0 {
		if body.Error == "" {
			body.Error = "invalid lot size response"
		}
		return 0, fmt.Errorf(body.Error)
	}
	return body.LotSize, nil
}

func (h *Handlers) getQuote(token, gcid string, instrumentToken int) (map[string]interface{}, error) {
	u := fmt.Sprintf("%s/getQuoteForSingleSymbol_V2", h.Config.GreekRestApiBaseURL)
	body := fmt.Sprintf(`{"request":{"data":{"token":"%d","gscid":"%s","gcid":"%s"},"svcName":"getQuoteForSingleSymbol_V2","svcGroup":"Markets"}}`, instrumentToken, gcid, gcid)

	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read quote response body: %w", err)
	}

	var quoteRes struct {
		Response struct {
			Data map[string]interface{} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(bodyBytes, &quoteRes); err != nil {
		return nil, err
	}

	return quoteRes.Response.Data, nil
}

func (h *Handlers) getBestBidPrice(token, gcid string, instrumentToken int) (float64, error) {
	quote, err := h.getQuote(token, gcid, instrumentToken)
	if err != nil {
		return 0, err
	}

	bidInfo, ok := quote["BidInfo"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("BidInfo not found in quote")
	}

	price, ok := bidInfo["Price"].(float64)
	if !ok || price <= 0 {
		return 0, fmt.Errorf("invalid bid price")
	}
	return price, nil
}

func (h *Handlers) getExpiryDate(token, symbol string) (string, error) {
	u := fmt.Sprintf("%s/getExpiryDate", h.Config.GreekRestApiBaseURL)
	body := fmt.Sprintf(`{"exchangeSegment":"NSEFO","series":"OPTIDX","symbol":"%s"}`, symbol)

	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var expiryRes struct {
		Result []string `json:"result"`
	}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read expiry response body: %w", err)
	}
	if err := json.Unmarshal(bodyBytes, &expiryRes); err != nil || len(expiryRes.Result) == 0 {
		return "", fmt.Errorf("could not parse expiry date response or no dates found")
	}
	return expiryRes.Result[0], nil
}

func (h *Handlers) getOptionToken(token, symbol, expiry, optionType string, strike float64) (int64, error) {
	u := fmt.Sprintf("%s/getOptionSymbol", h.Config.GreekRestApiBaseURL)
	body := fmt.Sprintf(`{"exchangeSegment":"NSEFO","series":"OPTIDX","symbol":"%s","expiryDate":"%s","optionType":"%s","strikePrice":%.0f}`, symbol, expiry, optionType, strike)

	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	var symbolRes struct {
		Result []struct {
			ExchangeInstrumentID int64 `json:"ExchangeInstrumentID"`
		} `json:"result"`
	}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read option symbol response body: %w", err)
	}
	if err := json.Unmarshal(bodyBytes, &symbolRes); err != nil || len(symbolRes.Result) == 0 {
		return 0, fmt.Errorf("could not parse option symbol response or no symbol found")
	}
	return symbolRes.Result[0].ExchangeInstrumentID, nil
}

func (h *Handlers) placeOrder(
	wg *sync.WaitGroup,
	results chan<- orderResult,
	sess *session.Session,
	exchangeSegment string,
	leg string,
	quantity int,
	gtoken int64,
	side string,
	price float64,
	productType string,
) {
	defer wg.Done()

	u := fmt.Sprintf("%s/NewOrderRequest", h.Config.GreekRestApiBaseURL)

	orderSideCode := "1"
	if strings.EqualFold(side, "SELL") {
		orderSideCode = "2"
	}

	exchange := "NSE"
	if strings.EqualFold(strings.TrimSpace(exchangeSegment), "BSEFO") {
		exchange = "BSE"
	}

	body := fmt.Sprintf(
		`{"request":{"data":{"gtoken":"%d","side":"%s","gcid":"%s","price":"%.2f","order_type":"1","qty":"%d","validity":"0","exchange":"%s","product":"0","productType":"%s"}}}`,
		gtoken, orderSideCode, sess.GCID, price, quantity, exchange, productType,
	)

	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	req.Header.Set("Authorization", sess.UpstreamToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(req)
	if err != nil {
		results <- orderResult{
			Leg:      leg,
			Token:    gtoken,
			Quantity: quantity,
			Price:    price,
			Error:    err.Error(),
		}
		return
	}
	defer res.Body.Close()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		results <- orderResult{
			Leg:      leg,
			Token:    gtoken,
			Quantity: quantity,
			Price:    price,
			Error:    "failed to read order response",
		}
		return
	}

	var resBody map[string]interface{}
	_ = json.Unmarshal(bodyBytes, &resBody)

	results <- orderResult{
		Leg:      leg,
		Token:    gtoken,
		Quantity: quantity,
		Price:    price,
		Response: resBody,
	}
}
