package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"trading-platform/services/market-data-gateway/internal/chain_fetcher"
)

type OptionChainRequest struct {
	Symbol      string  `json:"symbol"`
	Expiry      string  `json:"expiry"`
	ATMStrike   float64 `json:"atm_strike"`
	StrikeRange int     `json:"strike_range"`
}

type OptionChainResponse struct {
	Success bool                     `json:"success"`
	Data    *chain_fetcher.OptionChain `json:"data"`
	Error   string                   `json:"error"`
}

func main() {
	contractMasterURL := strings.TrimSpace(os.Getenv("CONTRACT_MASTER_URL"))
	if contractMasterURL == "" {
		contractMasterURL = "http://localhost:8010"
	}

	port := strings.TrimSpace(os.Getenv("MARKET_DATA_GATEWAY_PORT"))
	if port == "" {
		port = "8020"
	}

	log.Printf("🚀 Market Data Gateway starting...")
	log.Printf("📡 Contract Master URL: %s", contractMasterURL)

	// Initialize chain fetcher
	chainClient := chain_fetcher.NewClient(contractMasterURL)

	writeJSON := func(w http.ResponseWriter, status int, payload interface{}) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"service": "market-data-gateway",
		})
	})

	http.HandleFunc("/api/option-chain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, OptionChainResponse{
				Success: false,
				Error:   "method not allowed",
			})
			return
		}

		var req OptionChainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, OptionChainResponse{
				Success: false,
				Error:   "invalid JSON: " + err.Error(),
			})
			return
		}

		if req.Symbol == "" || req.Expiry == "" || req.ATMStrike == 0 {
			writeJSON(w, http.StatusBadRequest, OptionChainResponse{
				Success: false,
				Error:   "symbol, expiry, and atm_strike required",
			})
			return
		}

		strikeRange := req.StrikeRange
		if strikeRange == 0 {
			strikeRange = 10
		}

		log.Printf("📊 Building option chain for %s %s ATM=%.0f range=%d",
			req.Symbol, req.Expiry, req.ATMStrike, strikeRange)

		chain, err := chainClient.BuildChain(req.Symbol, req.Expiry, req.ATMStrike, strikeRange)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, OptionChainResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, OptionChainResponse{
			Success: true,
			Data:    chain,
		})

		log.Printf("✅ Built option chain for %s %s", req.Symbol, req.Expiry)
	})

	http.HandleFunc("/api/lot-size", func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
		expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))

		if symbol == "" || expiry == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "symbol and expiry required",
			})
			return
		}

		lotSize, err := chainClient.FetchLotSize(symbol, expiry)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"symbol":   symbol,
			"expiry":   expiry,
			"lot_size": lotSize,
		})
	})

	http.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
		expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
		optionType := strings.TrimSpace(r.URL.Query().Get("option_type"))
		strikeStr := strings.TrimSpace(r.URL.Query().Get("strike"))

		if symbol == "" || expiry == "" || optionType == "" || strikeStr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "symbol, expiry, option_type, and strike required",
			})
			return
		}

		strike, err := strconv.ParseFloat(strikeStr, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid strike price",
			})
			return
		}

		token, err := chainClient.FetchOptionToken(symbol, expiry, optionType, strike)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":      true,
			"symbol":       symbol,
			"expiry":       expiry,
			"option_type":  optionType,
			"strike":       strike,
			"broker_token": token,
		})
	})

	log.Printf("🌐 Market Data Gateway running on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
