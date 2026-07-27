package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	zmq "github.com/pebbe/zmq4"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Hub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mutex      sync.Mutex
}

func newHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte, 5000),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		clients:    make(map[*websocket.Conn]bool),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.mutex.Lock()
			h.clients[client] = true
			h.mutex.Unlock()
			slog.Info("🌐 UI Client connected to Go Snapshot Service")

		case client := <-h.unregister:
			h.mutex.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
			}
			h.mutex.Unlock()

		case message := <-h.broadcast:
			h.mutex.Lock()
			for client := range h.clients {
				if err := client.WriteMessage(websocket.TextMessage, message); err != nil {
					client.Close()
					delete(h.clients, client)
				}
			}
			h.mutex.Unlock()
		}
	}
}

func normalizeSymbol(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

type ChainCacheEntry struct {
	Payload     map[string]interface{}
	LastUpdated time.Time
}

var chainCache sync.Map

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	hub := newHub()
	go hub.run()

	handleWS := func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Error("WS Upgrade Error", "error", err)
			return
		}

		hub.register <- conn

		go func() {
			defer func() { hub.unregister <- conn }()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					break
				}
			}
		}()
	}

	http.HandleFunc("/ws/snapshots", handleWS)
	http.HandleFunc("/ws/data", handleWS)
	http.HandleFunc("/ws", handleWS)

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":           "ok",
			"db_status":        "connected",
			"socket_connected": true,
		})
	})

	// Receive live PnL and Greeks from Execution Gateway and broadcast to UI
	http.HandleFunc("/api/push-snapshots", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		var payload struct {
			Updates []map[string]interface{} `json:"updates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		for _, upd := range payload.Updates {
			msg := map[string]interface{}{
				"type": "straddle_update",
				"data": upd,
			}
			outBytes, _ := json.Marshal(msg)
			hub.broadcast <- outBytes
		}
		w.WriteHeader(http.StatusOK)
	})

	// -------------------------------------------------------
	// REST fallback: /api/option-chain/{SYMBOL}?expiry=DDMMMYYYY
	// If expiry param given  → return that exact expiry from cache
	// If no expiry param     → return nearest (smallest) expiry
	// -------------------------------------------------------
	http.HandleFunc("/api/option-chain/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		symbol := normalizeSymbol(strings.TrimPrefix(r.URL.Path, "/api/option-chain/"))
		targetExpiry := r.URL.Query().Get("expiry")

		if targetExpiry != "" {
			// Exact expiry requested
			if val, ok := chainCache.Load(symbol + "|" + targetExpiry); ok {
				entry := val.(ChainCacheEntry)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success":     true,
					"cached_at":   entry.LastUpdated.Format(time.RFC3339),
					"data":        entry.Payload["data"],
					"data_source": "go_cache",
				})
				return
			}
		} else {
			// No expiry given — find nearest upcoming expiry by actual date.
			// Do NOT compare expiry strings lexicographically.
			// Example bug fixed:
			// "24DEC29" was being selected before "28JUL26" because "24" < "28".
			var nearestEntry *ChainCacheEntry
			var nearestExpiry string
			var nearestExpiryDate time.Time

			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

			chainCache.Range(func(k, v interface{}) bool {
				key := k.(string)
				if !strings.HasPrefix(key, symbol+"|") {
					return true
				}

				exp := strings.TrimSpace(strings.TrimPrefix(key, symbol+"|"))
				expiryDate, ok := parseSnapshotExpiryDate(exp)
				if !ok {
					slog.Warn("Skipping unparsable expiry", "symbol", symbol, "expiry", exp)
					return true
				}

				if expiryDate.Before(today) {
					return true
				}

				entry := v.(ChainCacheEntry)

				if nearestEntry == nil || expiryDate.Before(nearestExpiryDate) {
					e := entry
					nearestEntry = &e
					nearestExpiry = exp
					nearestExpiryDate = expiryDate
				}

				return true
			})

			if nearestEntry != nil {
				slog.Info(
					"Selected nearest expiry from snapshot cache",
					"symbol", symbol,
					"expiry", nearestExpiry,
					"expiry_date", nearestExpiryDate.Format("2006-01-02"),
				)

				json.NewEncoder(w).Encode(map[string]interface{}{
					"success":     true,
					"cached_at":   nearestEntry.LastUpdated.Format(time.RFC3339),
					"data":        nearestEntry.Payload["data"],
					"data_source": "go_cache",
				})
				return
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Chain not yet built by C++ Feed Decoder",
		})
	})

	go func() {
		slog.Info("🚀 Go Snapshot Service listening", "port", "8003")
		if err := http.ListenAndServe(":8003", nil); err != nil {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Subscribe to C++ chain publisher
	zctx, err := zmq.NewContext()
	if err != nil {
		slog.Error("Failed to create ZMQ context", "error", err)
		os.Exit(1)
	}
	defer zctx.Term()

	sub, err := zctx.NewSocket(zmq.SUB)
	if err != nil {
		slog.Error("Failed to create ZMQ SUB socket", "error", err)
		os.Exit(1)
	}
	defer sub.Close()

	if err := sub.Connect("tcp://127.0.0.1:5556"); err != nil {
		slog.Error("Failed to connect ZMQ", "error", err)
		os.Exit(1)
	}

	if err := sub.SetSubscribe(""); err != nil {
		slog.Error("Failed to subscribe ZMQ", "error", err)
		os.Exit(1)
	}

	slog.Info("📥 ZMQ Connected to C++ Feed Decoder", "endpoint", "tcp://127.0.0.1:5556")

	for {
		msg, err := sub.Recv(0)
		if err != nil {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(msg), &raw); err != nil {
			continue
		}

		chainArray, ok := raw["chain"].([]interface{})
		if !ok {
			continue
		}

		symbol, _ := raw["symbol"].(string)
		expiry, _ := raw["expiry"].(string)

		futureLTP, _ := raw["future_ltp"].(float64)
		if futureLTP == 0 {
			if v, ok := raw["fut_ltp"].(float64); ok {
				futureLTP = v
			}
		}
		if futureLTP == 0 {
			if v, ok := raw["spot"].(float64); ok {
				futureLTP = v
			}
		}

		syntheticFuture, _ := raw["synthetic_future"].(float64)
		if syntheticFuture == 0 {
			if v, ok := raw["synthetic_spot"].(float64); ok {
				syntheticFuture = v
			}
		}

		atm, _ := raw["atm"].(float64)

		availableExpiries, ok := raw["available_expiries"].([]interface{})
		if !ok {
			availableExpiries = []interface{}{}
		}

		cleanSymbol := normalizeSymbol(symbol)

		uiPayload := map[string]interface{}{
			"type":   "option_chain_update",
			"symbol": cleanSymbol,
			"data": map[string]interface{}{
				"symbol":             cleanSymbol,
				"expiry":             expiry,
				"future_ltp":         futureLTP,
				"fut_ltp":            futureLTP,
				"synthetic_future":   syntheticFuture,
				"synthetic_spot":     syntheticFuture,
				"atm":                atm,
				"available_expiries": availableExpiries,
				"chain":              chainArray,
			},
		}

		// FIX: key is "SYMBOL|expiry" so each expiry is stored separately.
		// Previously "SYMBOL" alone meant last writer (December) always won.
		chainCache.Store(cleanSymbol+"|"+expiry, ChainCacheEntry{
			Payload:     uiPayload,
			LastUpdated: time.Now(),
		})

		outBytes, err := json.Marshal(uiPayload)
		if err != nil {
			continue
		}

		hub.broadcast <- outBytes
	}
}

func parseSnapshotExpiryDate(expiry string) (time.Time, bool) {
	clean := strings.ToUpper(strings.TrimSpace(expiry))
	if clean == "" {
		return time.Time{}, false
	}

	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ReplaceAll(clean, "_", "")
	clean = strings.ReplaceAll(clean, " ", "")
	clean = strings.ReplaceAll(clean, "/", "")

	if len(clean) != 7 && len(clean) != 9 {
		return time.Time{}, false
	}

	dayPart := clean[0:2]
	monPart := clean[2:5]
	yearPart := clean[5:]

	monthMap := map[string]time.Month{
		"JAN": time.January,
		"FEB": time.February,
		"MAR": time.March,
		"APR": time.April,
		"MAY": time.May,
		"JUN": time.June,
		"JUL": time.July,
		"AUG": time.August,
		"SEP": time.September,
		"OCT": time.October,
		"NOV": time.November,
		"DEC": time.December,
	}

	month, ok := monthMap[monPart]
	if !ok {
		return time.Time{}, false
	}

	day, err := strconv.Atoi(dayPart)
	if err != nil || day <= 0 || day > 31 {
		return time.Time{}, false
	}

	year, err := strconv.Atoi(yearPart)
	if err != nil {
		return time.Time{}, false
	}

	if year < 100 {
		year += 2000
	}

	t := time.Date(year, month, day, 0, 0, 0, 0, time.Local)

	if t.Day() != day || t.Month() != month || t.Year() != year {
		return time.Time{}, false
	}

	return t, true
}
