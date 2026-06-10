package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"trading-platform/services/market-data-gateway/internal/chain_fetcher"
	"trading-platform/services/market-data-gateway/internal/publisher"
	"trading-platform/services/market-data-gateway/internal/socketio"

	"github.com/gorilla/websocket"
	zmq "github.com/pebbe/zmq4"
)

const (
	mdBaseURL = "https://developers.symphonyfintech.in"
)

// XTS Login Schemas
type LoginRequest struct {
	SecretKey string `json:"secretKey"`
	AppKey    string `json:"appKey"`
	Source    string `json:"source"`
}

type LoginResponse struct {
	Type   string `json:"type"`
	Result struct {
		Token  string `json:"token"`
		UserID string `json:"userID"`
	} `json:"result"`
	Description string `json:"description"`
}

// C++ Decoder Cache
type DecoderCache struct {
	mu     sync.RWMutex
	Chains map[string][]byte
}

// WebSocket connections for the UI
var (
	wsClients   = make(map[*websocket.Conn]bool)
	wsClientsMu sync.Mutex
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func extractSymbol(data []byte) string {
	prefix := []byte(`"symbol":"`)
	idx := bytes.Index(data, prefix)
	if idx == -1 {
		return ""
	}
	start := idx + len(prefix)
	end := bytes.IndexByte(data[start:], '"')
	if end == -1 {
		return ""
	}
	return string(data[start : start+end])
}

func broadcastToWS(symbol string, rawJSON []byte) {
	wsClientsMu.Lock()
	if len(wsClients) == 0 {
		wsClientsMu.Unlock()
		return // 🔥 Zero allocation if no React UI is connected!
	}
	wsClientsMu.Unlock()

	// 🔥 DIRECT PASSTHROUGH: If C++ already wrapped it, send exactly as-is!
	if bytes.Contains(rawJSON, []byte(`"type":"option_chain"`)) {
		wsClientsMu.Lock()
		for client := range wsClients {
			_ = client.WriteMessage(websocket.TextMessage, rawJSON)
		}
		wsClientsMu.Unlock()
		return
	}

	var builder bytes.Buffer
	builder.WriteString(`{"type":"option_chain","symbol":"`)
	builder.WriteString(symbol)
	builder.WriteString(`","data":`)
	builder.Write(rawJSON)
	builder.WriteString(`}`)
	msg := builder.Bytes()

	wsClientsMu.Lock()
	defer wsClientsMu.Unlock()
	for client := range wsClients {
		if err := client.WriteMessage(websocket.TextMessage, msg); err != nil {
			client.Close()
			delete(wsClients, client)
		}
	}
}

var cache = &DecoderCache{
	Chains: make(map[string][]byte),
}

func listenToDecoder() {
	zctx, _ := zmq.NewContext()
	sock, _ := zctx.NewSocket(zmq.SUB)
	sock.SetConflate(true) // 🔥 Keep ONLY the absolute latest tick. Drops backlog and prevents OOM!
	sock.Connect("tcp://localhost:5556")
	sock.SetSubscribe("")
	for {
		msgBytes, err := sock.RecvBytes(0)
		if err == nil {
			sym := extractSymbol(msgBytes)
			if sym == "" {
				sym = "NIFTY" // Default fallback if missing
			}
			if sym != "" {
				cache.mu.Lock()
				cache.Chains[sym] = msgBytes // Store raw bytes
				cache.mu.Unlock()

				// 🔥 Instantly broadcast C++ raw bytes directly to React!
				broadcastToWS(sym, msgBytes)

				// 🔥 Give the Garbage Collector a microsecond to breathe (Cap at 100 FPS max per instrument)
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
}

func optionChainHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	parts := strings.Split(r.URL.Path, "/")
	symbol := parts[len(parts)-1]

	// TAKE FROM DECODER FIRST
	cache.mu.RLock()
	chainBytes, exists := cache.Chains[symbol]
	cache.mu.RUnlock()

	if exists {
		w.Write([]byte(`{"success":true,"source":"cpp_decoder","data":`))
		w.Write(chainBytes)
		w.Write([]byte(`}`))
		return
	}

	// IF NOT IN DECODER YET, return empty to allow retry
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   "Option chain not decoded by C++ yet",
	})
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS Upgrade error", "error", err)
		return
	}

	wsClientsMu.Lock()
	wsClients[ws] = true
	wsClientsMu.Unlock()
	slog.Info("🟢 React UI Connected to Go WebSocket for Live Ticks!")

	// Keep alive loop
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			wsClientsMu.Lock()
			delete(wsClients, ws)
			wsClientsMu.Unlock()
			slog.Info("🔴 React UI Disconnected from Go WebSocket")
			break
		}
	}
}

// QuoteResponse schemas for dynamic spot price fetching
type QuoteResponse struct {
	Type   string `json:"type"`
	Result struct {
		ListQuotes []interface{} `json:"listQuotes"`
	} `json:"result"`
}

func parseQuote(quote interface{}) map[string]interface{} {
	var data map[string]interface{}
	switch v := quote.(type) {
	case string:
		json.Unmarshal([]byte(v), &data)
	case map[string]interface{}:
		data = v
	}
	return data
}

// fetchSpotPrice dynamically gets the real-time cash index price from XTS
func fetchSpotPrice(token, userID string, segment, instrumentID int, symbol string) float64 {
	// Take from decoder first!
	cache.mu.RLock()
	chainBytes, exists := cache.Chains[symbol]
	cache.mu.RUnlock()
	if exists {
		var partial struct {
			FutLtp float64 `json:"fut_ltp"`
		}
		if json.Unmarshal(chainBytes, &partial) == nil && partial.FutLtp > 0 {
			slog.Info("Fetched Spot Price directly from C++ Decoder Cache!", "symbol", symbol, "price", partial.FutLtp)
			return partial.FutLtp
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	url := mdBaseURL + "/apimarketdata/instruments/quotes"

	payload := map[string]interface{}{
		"instruments": []map[string]interface{}{
			{"exchangeSegment": segment, "exchangeInstrumentID": instrumentID},
		},
		"xtsMessageCode": 1502,
		"publishFormat":  "JSON",
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var qResp QuoteResponse
	json.Unmarshal(respBody, &qResp)

	if len(qResp.Result.ListQuotes) > 0 {
		qd := parseQuote(qResp.Result.ListQuotes[0])
		if val, ok := qd["IndexValue"].(float64); ok && val > 0 {
			return val
		}
		if vs, ok := qd["IndexValue"].(string); ok {
			if v, _ := strconv.ParseFloat(vs, 64); v > 0 {
				return v
			}
		}
	}

	// Fallback to 1512
	payload["xtsMessageCode"] = 1512
	body2, _ := json.Marshal(payload)
	req2, _ := http.NewRequest("POST", url, bytes.NewBuffer(body2))
	req2.Header.Set("Authorization", token)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err == nil {
		respBody2, _ := io.ReadAll(resp2.Body)
		var qResp2 QuoteResponse
		json.Unmarshal(respBody2, &qResp2)
		resp2.Body.Close()
		if len(qResp2.Result.ListQuotes) > 0 {
			qd := parseQuote(qResp2.Result.ListQuotes[0])
			if val, ok := qd["LastTradedPrice"].(float64); ok && val > 0 {
				return val
			}
			if vs, ok := qd["LastTradedPrice"].(string); ok {
				if v, _ := strconv.ParseFloat(vs, 64); v > 0 {
					return v
				}
			}
		}
	}
	return 0
}

// login authenticates with XTS Market Data API to retrieve the WebSocket token
func login(appKey, secretKey string) (string, string, error) {
	url := mdBaseURL + "/apimarketdata/auth/login"
	reqBody, _ := json.Marshal(LoginRequest{
		SecretKey: secretKey,
		AppKey:    appKey,
		Source:    "WEBAPI",
	})

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var loginRes LoginResponse
	if err := json.Unmarshal(body, &loginRes); err != nil {
		return "", "", err
	}

	if loginRes.Type != "success" {
		return "", "", fmt.Errorf("login failed: %s", loginRes.Description)
	}

	return loginRes.Result.Token, loginRes.Result.UserID, nil
}

// getNearestExpiry dynamically fetches the closest upcoming expiry
func getActiveExpiries(fetcher *chain_fetcher.Client, symbol string) ([]string, error) {
	expiries, err := fetcher.FetchExpiryDates(2, "OPTIDX", symbol)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var validDates []time.Time
	for _, exp := range expiries {
		dateStr := strings.Split(exp, "T")[0]
		parsed, err := time.Parse("2006-01-02", dateStr)
		// Ensure we pick an expiry that hasn't passed yet
		if err == nil && !parsed.Before(now.Truncate(24*time.Hour)) {
			validDates = append(validDates, parsed)
		}
	}
	if len(validDates) > 0 {
		sort.Slice(validDates, func(i, j int) bool {
			return validDates[i].Before(validDates[j])
		})
		var res []string
		for _, d := range validDates {
			res = append(res, d.Format("02Jan2006"))
		}
		return res, nil
	}
	return nil, fmt.Errorf("no expiries found")
}

func main() {
	// Setup JSON logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// 0. Start C++ Decoder Listener & REST API
	go listenToDecoder()
	http.HandleFunc("/api/option-chain/", optionChainHandler)
	http.HandleFunc("/ws/data", wsHandler)
	go func() {
		slog.Info("🌐 Market Data REST API & WebSocket listening on Port 8001")
		http.ListenAndServe(":8001", nil)
	}()

	slog.Info("🚀 Starting Go Market Data Gateway")

	appKey := os.Getenv("XTS_MD_API_KEY")
	secretKey := os.Getenv("XTS_MD_API_SECRET")
	if appKey == "" || secretKey == "" {
		slog.Warn("⚠️ XTS_MD_API_KEY or XTS_MD_API_SECRET not set in environment!")
	}

	// 1. Authenticate to XTS
	token, userID, err := login(appKey, secretKey)
	if err != nil {
		slog.Error("Failed to login to XTS Market Data", "error", err)
		os.Exit(1)
	}
	slog.Info("✅ Authenticated with XTS Market Data", "userID", userID)

	// 2. Initialize the ZeroMQ Publisher
	zmqEndpoint := "tcp://*:5555" // The C++ Feed Decoder connects here
	pub, err := publisher.NewPublisher(zmqEndpoint)
	if err != nil {
		slog.Error("Failed to bind ZMQ Publisher", "error", err)
		os.Exit(1)
	}
	defer pub.Close()

	// 3. Fetch the Option Chain Mapping FIRST (This takes ~14 seconds)
	fetcher := chain_fetcher.NewClient(mdBaseURL+"/apimarketdata", token, userID)

	tempCsvPath := "/home/ubuntu/Desktop/api_gs/trading-platform/GreekTokens.tmp.csv"
	finalCsvPath := "/home/ubuntu/Desktop/api_gs/trading-platform/GreekTokens.csv"
	var foInstruments []map[string]interface{}
	var cashInstruments []map[string]interface{}
	var csvLines []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	csvLines = append(csvLines, "Token,Symbol,Strike,OptionType\n")

	// Loop through ALL supported indices concurrently
	for symbol, cfg := range chain_fetcher.SymbolConfigs {
		sym := symbol
		config := cfg
		wg.Add(1)
		go func() {
			defer wg.Done()
			spotPrice := fetchSpotPrice(token, userID, config.CashSegment, config.CashToken, sym)
			if spotPrice <= 0 {
				slog.Warn("Failed to fetch live Spot Price, using default ATM", "symbol", sym)
				// Use a reasonable default ATM; C++ will correct it with synthetic spot later
				switch sym {
				case "BANKNIFTY":
					spotPrice = 50000
				case "FINNIFTY":
					spotPrice = 22000
				case "MIDCPNIFTY":
					spotPrice = 12000
				case "SENSEX", "BANKEX":
					spotPrice = 75000
				default: // NIFTY
					spotPrice = 23000
				}
			}

			// Calculate ATM Strike based on dynamic gap
			atmStrike := float64(int((spotPrice+(config.Gap/2))/config.Gap) * int(config.Gap))

			expiries, _ := getActiveExpiries(fetcher, sym)
			var validChain *chain_fetcher.OptionChain
			var finalExpiry string

			// Loop through dates until we find one that actually has Option tokens (Fixes 2029 bug)
			for _, exp := range expiries {
				slog.Info("Testing Expiry", "symbol", sym, "expiry", exp)
				chain, err := fetcher.BuildChain(sym, exp, atmStrike, 40)
				if err == nil && chain != nil && len(chain.Strikes) > 0 && chain.Strikes[0].CEToken != 0 {
					validChain = chain
					finalExpiry = exp
					break
				}
			}

			if validChain != nil {
				slog.Info("✅ Dynamic Chain Built successfully!", "symbol", sym, "expiry", finalExpiry, "spot", spotPrice)
				mu.Lock()
				for _, row := range validChain.Strikes {
					if row.CEToken != 0 {
						csvLines = append(csvLines, fmt.Sprintf("%d,%s,%.0f,CE\n", row.CEToken, sym, row.Strike))
						foInstruments = append(foInstruments, map[string]interface{}{"exchangeSegment": validChain.ExchangeSeg, "exchangeInstrumentID": row.CEToken})
					}
					if row.PEToken != 0 {
						csvLines = append(csvLines, fmt.Sprintf("%d,%s,%.0f,PE\n", row.PEToken, sym, row.Strike))
						foInstruments = append(foInstruments, map[string]interface{}{"exchangeSegment": validChain.ExchangeSeg, "exchangeInstrumentID": row.PEToken})
					}
				}
				// Subscribe the Cash Index correctly
				csvLines = append(csvLines, fmt.Sprintf("%d,%s,0,SPOT\n", config.CashToken, sym))
				cashInstruments = append(cashInstruments, map[string]interface{}{"exchangeSegment": config.CashSegment, "exchangeInstrumentID": config.CashToken})
				mu.Unlock()
			}
		}()
	}

	wg.Wait() // Wait for all chains to be built concurrently

	csvFile, err := os.Create(tempCsvPath)
	if err == nil {
		for _, line := range csvLines {
			csvFile.WriteString(line)
		}
		csvFile.Close()
		os.Rename(tempCsvPath, finalCsvPath)
		slog.Info("✅ Generated GreekTokens.csv for all supported indices")
	}

	// 4. Initialize and Connect the Socket.IO Client (The Internet Bridge)
	xtsClient, err := socketio.NewXTSClient(mdBaseURL, token, userID, pub)
	if err != nil {
		slog.Error("Failed to create Socket.IO client", "error", err)
		os.Exit(1)
	}

	go func() {
		for {
			var wg sync.WaitGroup
			wg.Add(1)

			go func() {
				defer wg.Done()
				if err := xtsClient.Connect(); err != nil {
					slog.Error("Socket.IO disconnected", "error", err)
				}
			}()

			time.Sleep(2 * time.Second) // Negotiate transport

			// 5. Subscribe to tokens via Socket.IO
			if len(foInstruments) > 0 {
				chunkSize := 45
				for i := 0; i < len(foInstruments); i += chunkSize {
					end := i + chunkSize
					if end > len(foInstruments) {
						end = len(foInstruments)
					}
					_ = xtsClient.Subscribe(foInstruments[i:end], 1512)
					time.Sleep(300 * time.Millisecond)
				}
			}
			if len(cashInstruments) > 0 {
				_ = xtsClient.Subscribe(cashInstruments, 1510) // Market Data for Cash Indices
				time.Sleep(500 * time.Millisecond)
			}

			wg.Wait()
			slog.Info("⏳ Socket.IO dropped. Reconnecting in 3 seconds...")
			time.Sleep(3 * time.Second)
		}
	}()

	// Block until graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("🛑 Shutting down Market Data Gateway")
}
