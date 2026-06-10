package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"trading-platform/services/control-api/internal/session"
)

// symbolDetails contains the necessary information for fetching market data for a symbol.
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
}

// SellStraddleHandler orchestrates the multi-step process of selling an ATM straddle.
func (h *Handlers) SellStraddleHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Get user session
	sess, err := h.getSessionFromCookie(r)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "%v"}`, err), http.StatusUnauthorized)
		return
	}
	if sess.GCID == "" {
		http.Error(w, `{"message": "JLogin required before trading"}`, http.StatusBadRequest)
		return
	}

	// 2. Parse request
	var req struct {
		Symbol       string `json:"symbol"`
		Lots         int    `json:"lots"`
		DeltaNeutral bool   `json:"delta_neutral"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message": "Invalid request body"}`, http.StatusBadRequest)
		return
	}
	slog.Info("Received sell straddle request", "user", sess.Username, "symbol", req.Symbol, "lots", req.Lots)

	// 3. Get Spot Price to determine ATM
	details, ok := symbolDetails[req.Symbol]
	if !ok {
		http.Error(w, `{"message": "Unsupported symbol"}`, http.StatusBadRequest)
		return
	}
	quote, err := h.getQuote(sess.UpstreamToken, sess.GCID, details.Token)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "Failed to get spot price: %v"}`, err), http.StatusBadGateway)
		return
	}
	spotPrice, ok := quote["LastTradedPrice"].(float64)
	if !ok || spotPrice <= 0 {
		http.Error(w, `{"message": "Failed to get valid spot price from quote"}`, http.StatusBadGateway)
		return
	}
	atmStrike := math.Round(spotPrice/float64(details.Gap)) * float64(details.Gap)
	slog.Info("Determined ATM strike", "symbol", req.Symbol, "spot", spotPrice, "atm", atmStrike)

	// 4. Get Expiry Date
	expiryDate, err := h.getExpiryDate(sess.UpstreamToken, req.Symbol)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "Failed to get expiry date: %v"}`, err), http.StatusBadGateway)
		return
	}

	// 5. Get Option Tokens for CE and PE
	ceToken, err := h.getOptionToken(sess.UpstreamToken, req.Symbol, expiryDate, "CE", atmStrike)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "Failed to get CE option token: %v"}`, err), http.StatusBadGateway)
		return
	}
	peToken, err := h.getOptionToken(sess.UpstreamToken, req.Symbol, expiryDate, "PE", atmStrike)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "Failed to get PE option token: %v"}`, err), http.StatusBadGateway)
		return
	}
	slog.Info("Found option tokens", "ce_token", ceToken, "pe_token", peToken)

	// 6. Get Quotes for options to determine limit price
	ceQuote, err := h.getQuote(sess.UpstreamToken, sess.GCID, int(ceToken))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "Failed to get CE quote: %v"}`, err), http.StatusBadGateway)
		return
	}
	peQuote, err := h.getQuote(sess.UpstreamToken, sess.GCID, int(peToken))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"message": "Failed to get PE quote: %v"}`, err), http.StatusBadGateway)
		return
	}

	// 7. Calculate Quantities (Delta-Neutral or Equal)
	ceContracts := req.Lots * details.LotSize
	peContracts := req.Lots * details.LotSize

	if req.DeltaNeutral {
		ceDelta, ceOk := ceQuote["Delta"].(float64)
		peDelta, peOk := peQuote["Delta"].(float64)
		if !ceOk || !peOk {
			slog.Warn("Delta not found in quote, falling back to equal quantities", "user", sess.Username)
		} else {
			slog.Info("Calculating delta-neutral quantities", "ce_delta", ceDelta, "pe_delta", peDelta)
			totalContracts := float64(req.Lots * details.LotSize)
			ceContracts = int(math.Round(totalContracts*math.Abs(peDelta))/float64(details.LotSize)) * details.LotSize
			peContracts = int(math.Round(totalContracts*math.Abs(ceDelta))/float64(details.LotSize)) * details.LotSize
		}
	}
	slog.Info("Calculated order quantities", "ce_contracts", ceContracts, "pe_contracts", peContracts)

	// 8. For a SELL order, we place a limit order at the current BID price to increase fill probability.
	ceBidInfo, ceOk := ceQuote["BidInfo"].(map[string]interface{})
	peBidInfo, peOk := peQuote["BidInfo"].(map[string]interface{})
	if !ceOk || !peOk {
		http.Error(w, `{"message": "BidInfo not found in quote response"}`, http.StatusBadGateway)
		return
	}
	cePrice, ceOk := ceBidInfo["Price"].(float64)
	pePrice, peOk := peBidInfo["Price"].(float64)
	if !ceOk || !peOk || cePrice <= 0 || pePrice <= 0 {
		http.Error(w, `{"message": "Invalid bid price in quote response"}`, http.StatusBadGateway)
		return
	}

	// 9. Place Orders Concurrently
	var wg sync.WaitGroup
	results := make(chan map[string]interface{}, 2)
	wg.Add(2)

	if ceContracts > 0 {
		go h.placeOrder(&wg, results, sess, ceContracts, ceToken, "SELL", cePrice)
	} else {
		wg.Done() // Decrement wait group if no order is placed
	}
	if peContracts > 0 {
		go h.placeOrder(&wg, results, sess, peContracts, peToken, "SELL", pePrice)
	} else {
		wg.Done() // Decrement wait group if no order is placed
	}

	wg.Wait()
	close(results)

	// 10. Aggregate and return results
	var finalResults []map[string]interface{}
	for res := range results {
		finalResults = append(finalResults, res)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(finalResults)
}

// --- Helper functions for SellStraddleHandler ---

func (h *Handlers) getQuote(token, gcid string, instrumentToken int) (map[string]interface{}, error) {
	// This function proxies a call to the getQuoteForSingleSymbol_V2 endpoint
	url := fmt.Sprintf("%s/getQuoteForSingleSymbol_V2", h.Config.GreekRestApiBaseUrl)
	body := fmt.Sprintf(`{"request":{"data":{"token":"%d","gscid":"%s","gcid":"%s"},"svcName":"getQuoteForSingleSymbol_V2","svcGroup":"Markets"}}`, instrumentToken, gcid, gcid)

	proxyReq, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	proxyReq.Header.Set("Authorization", token) // The main session token
	proxyReq.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(proxyReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var quoteRes struct {
		Response struct {
			Data map[string]interface{} `json:"data"`
		} `json:"response"`
	}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read quote response body: %w", err)
	}
	if err := json.Unmarshal(bodyBytes, &quoteRes); err != nil {
		return nil, err
	}
	return quoteRes.Response.Data, nil
}

func (h *Handlers) getExpiryDate(token, symbol string) (string, error) {
	// This function proxies a call to get the nearest expiry date
	url := fmt.Sprintf("%s/getExpiryDate", h.Config.GreekRestApiBaseUrl)
	body := fmt.Sprintf(`{"exchangeSegment": "NSEFO", "series": "OPTIDX", "symbol": "%s"}`, symbol)

	proxyReq, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	proxyReq.Header.Set("Authorization", token) // The main session token
	proxyReq.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(proxyReq)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var expiryRes struct {
		Result []string `json:"result"`
	}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read expiry response body: %w", err)
	}
	if err := json.Unmarshal(bodyBytes, &expiryRes); err != nil || len(expiryRes.Result) == 0 {
		return "", fmt.Errorf("could not parse expiry date response or no dates found")
	}
	// Assuming the first date is the nearest weekly expiry
	return expiryRes.Result[0], nil
}

func (h *Handlers) getOptionToken(token, symbol, expiry, optionType string, strike float64) (float64, error) {
	url := fmt.Sprintf("%s/getOptionSymbol", h.Config.GreekRestApiBaseUrl)
	body := fmt.Sprintf(`{"exchangeSegment": "NSEFO", "series": "OPTIDX", "symbol": "%s", "expiryDate": "%s", "optionType": "%s", "strikePrice": %.0f}`, symbol, expiry, optionType, strike)

	proxyReq, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	proxyReq.Header.Set("Authorization", token) // The main session token
	proxyReq.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(proxyReq)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	var symbolRes struct {
		Result []struct {
			ExchangeInstrumentID float64 `json:"ExchangeInstrumentID"`
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

func (h *Handlers) placeOrder(wg *sync.WaitGroup, results chan<- map[string]interface{}, sess *session.Session, quantity int, gtoken float64, side string, price float64) {
	defer wg.Done()
	// Simplified order placement logic, adapt fields as necessary
	url := fmt.Sprintf("%s/NewOrderRequest", h.Config.GreekRestApiBaseUrl)
	orderSideCode := "1"
	if side == "SELL" {
		orderSideCode = "2"
	}
	body := fmt.Sprintf(`{"request":{"data":{"gtoken":"%.0f","side":"%s","gcid":"%s","price":"%.2f","order_type":"1","qty":"%d","validity":"0","exchange":"NSE","product":"0"}}}`, gtoken, orderSideCode, sess.GCID, price, quantity)

	proxyReq, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	proxyReq.Header.Set("Authorization", sess.UpstreamToken)
	proxyReq.Header.Set("Content-Type", "application/json")

	res, err := h.Client.Do(proxyReq)
	if err != nil {
		results <- map[string]interface{}{"error": err.Error(), "token": gtoken}
		return
	}
	defer res.Body.Close()

	var resBody map[string]interface{}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		results <- map[string]interface{}{"error": "failed to read order response", "token": gtoken}
	}
	json.Unmarshal(bodyBytes, &resBody)
	results <- resBody
}
