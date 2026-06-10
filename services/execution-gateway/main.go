package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	zmq "github.com/pebbe/zmq4"

	"execution-gateway/internal/router"
	xts "trading-platform/libs/broker-xts"
	broker "trading-platform/libs/go-broker"
)

// loadEnv is a simple helper to load the .env file
func loadEnv() {
	envPath := filepath.Join("..", "..", ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), `"`) // remove quotes
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

// fetchATMData calls the Market Data microservice to get live ATM tokens and lot size
func fetchATMData(symbol string) (ceToken, peToken, lotSize int, err error) {
	url := fmt.Sprintf("http://127.0.0.1:8001/api/option-chain/%s", symbol)
	resp, err := http.Get(url)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to connect to market data service: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, 0, 0, fmt.Errorf("market data service returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			LotSize int `json:"lot_size"`
			Chain   []struct {
				IsATM   bool `json:"is_atm"`
				CEToken int  `json:"ce_token"`
				PEToken int  `json:"pe_token"`
			} `json:"chain"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse JSON: %v", err)
	}

	for _, row := range result.Data.Chain {
		if row.IsATM {
			return row.CEToken, row.PEToken, result.Data.LotSize, nil
		}
	}
	return 0, 0, 0, fmt.Errorf("ATM strike not found in chain")
}

// =========================================================================
// INTEGRATED CONTROL API & TRADE MANAGER
// =========================================================================
var (
	globalRouter   *router.Router
	globalClientID string
)

// corsMiddleware handles preflight requests from the Vite UI
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleDeployStraddle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol       string `json:"symbol"`
		Lots         int    `json:"lots"`
		DeltaNeutral bool   `json:"delta_neutral"`
		Strike       int    `json:"strike"`
		CEToken      int    `json:"ce_token"`
		PEToken      int    `json:"pe_token"`
		LotSize      int    `json:"lot_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tradeUID := fmt.Sprintf("TRD_%d", time.Now().Unix())
	log.Printf("🚀 [Control API] Deploy Straddle requested: %s | UID: %s", req.Symbol, tradeUID)

	// Fallback to fetch tokens if the UI feed omitted them
	if req.CEToken == 0 || req.PEToken == 0 {
		log.Printf("⚠️ Tokens missing from UI request, fetching directly from Market Data (Port 8001)...")
		ce, pe, ls, err := fetchATMData(req.Symbol)
		if err != nil {
			log.Printf("❌ Failed to fetch fallback tokens: %v", err)
		} else {
			req.CEToken = ce
			req.PEToken = pe
			if req.LotSize == 0 {
				req.LotSize = ls
			}
			log.Printf("✅ Fallback successful! CE: %d, PE: %d, LotSize: %d", req.CEToken, req.PEToken, req.LotSize)
		}
	}

	// Fire orders asynchronously to the router
	go func(symbol string, lots int, ceToken int, peToken int, lotSize int) {
		qty := lotSize * lots
		if qty == 0 {
			// Safe fallbacks if lot size extraction failed
			switch symbol {
			case "NIFTY":
				qty = 25 * lots
			case "BANKNIFTY":
				qty = 15 * lots
			case "FINNIFTY":
				qty = 25 * lots
			case "MIDCPNIFTY":
				qty = 50 * lots
			default:
				qty = 25 * lots
			}
		}

		// Execute CE Leg
		if ceToken != 0 {
			ceIntent := broker.OrderIntent{
				ClientID:        globalClientID,
				ExchangeSegment: "NSEFO",
				InstrumentToken: ceToken,
				Side:            "SELL",
				OrderType:       "MARKET", // Convert to LIMIT eventually
				ProductType:     "MIS",
				Quantity:        qty,
			}
			if resp, err := globalRouter.RouteOrder(context.Background(), &ceIntent); err != nil {
				log.Printf("❌ CE Order Failed: %v", err)
			} else {
				log.Printf("✅ CE Order Success! AppOrderID: %s", resp.OrderID)
			}
		}

		// Execute PE Leg
		if peToken != 0 {
			peIntent := broker.OrderIntent{
				ClientID:        globalClientID,
				ExchangeSegment: "NSEFO",
				InstrumentToken: peToken,
				Side:            "SELL",
				OrderType:       "MARKET",
				ProductType:     "MIS",
				Quantity:        qty,
			}
			if resp, err := globalRouter.RouteOrder(context.Background(), &peIntent); err != nil {
				log.Printf("❌ PE Order Failed: %v", err)
			} else {
				log.Printf("✅ PE Order Success! AppOrderID: %s", resp.OrderID)
			}
		}
	}(req.Symbol, req.Lots, req.CEToken, req.PEToken, req.LotSize)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"trade_uid": tradeUID,
		"status":    "BUILDING",
		"message":   "Straddle deployment initiated",
	})
}

func handleSquareOff(w http.ResponseWriter, r *http.Request) {
	// Extracts the trade UID from the URL path: /api/trade/{trade_uid}/square-off
	parts := strings.Split(r.URL.Path, "/")
	tradeUID := parts[3]

	log.Printf("🛑 [Control API] Manual Square-Off Triggered for Trade: %s", tradeUID)
	// TODO: Trigger Layer 3 Router to close open positions (Python square_off.py logic)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func main() {
	log.Println("Starting Execution Gateway...")

	// 1. Initialize the Layer 3 Router
	r := router.New()
	globalRouter = r

	// =========================================================================
	// LIVE TEST: AUTO-LOGIN & SELL STRADDLE
	// =========================================================================
	loadEnv()
	appKey := os.Getenv("XTS_API_KEY")
	secretKey := os.Getenv("XTS_API_SECRET")
	baseURL := os.Getenv("XTS_API_BASE_URL")
	source := os.Getenv("XTS_SOURCE")
	clientID := os.Getenv("XTS_CLIENT_ID")
	globalClientID = clientID

	if appKey != "" && secretKey != "" {
		log.Printf("🔥 LIVE TEST: Logging into XTS for Client %s...", clientID)

		cfg := broker.Config{
			BrokerName: xts.BrokerName,
			BaseURL:    baseURL,
			AppKey:     appKey,
			SecretKey:  secretKey,
			Source:     source,
		}
		accCfg := broker.AccountConfig{
			ClientID: clientID,
		}

		// Create client directly via Factory
		client, err := xts.NewFactory(cfg)
		if err != nil {
			log.Fatalf("Failed to create XTS client: %v", err)
		}

		// Perform Login
		session, err := client.PerformFullLogin(context.Background(), &accCfg)
		if err != nil {
			log.Fatalf("Failed to login to XTS: %v", err)
		}
		log.Printf("✅ XTS Login Success! Session Token: %s", session.AuthToken)

		// Register client with the router so RouteOrder() can find it
		r.AddClient(clientID, client)

	} else {
		log.Println("⚠️  Skipping Live Test: Missing XTS credentials in .env")
	}
	// =========================================================================

	// Start Integrated HTTP Control API for UI requests (Port 8005)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/straddle/sell", handleDeployStraddle)
		mux.HandleFunc("/api/trade/", handleSquareOff) // Covers /api/trade/{uid}/square-off
		log.Println("🌐 Control API listening on :8005")
		http.ListenAndServe(":8005", corsMiddleware(mux))
	}()

	// 2. Setup ZeroMQ Subscriber
	subscriber, err := zmq.NewSocket(zmq.SUB)
	if err != nil {
		log.Fatalf("Failed to create ZMQ socket: %v", err)
	}
	defer subscriber.Close()

	// Bind to the port where the C++ Trade Worker publishes OrderIntents
	zmqEndpoint := "tcp://127.0.0.1:5557"
	err = subscriber.Bind(zmqEndpoint)
	if err != nil {
		log.Fatalf("Failed to bind ZMQ to %s: %v", zmqEndpoint, err)
	}
	err = subscriber.SetSubscribe("") // Subscribe to all messages
	if err != nil {
		log.Fatalf("Failed to set ZMQ subscription: %v", err)
	}

	log.Printf("Listening for OrderIntents on ZMQ %s", zmqEndpoint)

	// 3. Graceful shutdown handler
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("Shutting down Execution Gateway...")
		cancel()
	}()

	// 4. Main Event Loop
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Read message from ZMQ (blocking)
			msg, err := subscriber.Recv(0)
			if err != nil {
				continue
			}

			var intent broker.OrderIntent
			if err := json.Unmarshal([]byte(msg), &intent); err != nil {
				log.Printf("Failed to unmarshal OrderIntent: %v\nRaw: %s", err, msg)
				continue
			}

			log.Printf("Received OrderIntent for Client %q", intent.ClientID)

			// 5. Route the Order to the correct Broker Client asynchronously
			go func(orderIntent broker.OrderIntent) {
				resp, err := r.RouteOrder(context.Background(), &orderIntent)
				if err != nil {
					log.Printf("Failed to route/execute order: %v", err)
					// TODO: Publish a reject OrderUpdate back to ZMQ/Reconciler
					return
				}
				log.Printf("Order Executed Successfully! OrderID: %s", resp.OrderID)
				// TODO: Publish a success OrderUpdate back to ZMQ/Reconciler
			}(intent)
		}
	}
}
