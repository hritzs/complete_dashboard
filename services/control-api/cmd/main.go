package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	zmq "github.com/pebbe/zmq4"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// 1. Setup ZeroMQ Publisher (Control API -> C++ Trade Worker)
	zctx, _ := zmq.NewContext()
	pub, _ := zctx.NewSocket(zmq.PUB)
	pub.Bind("tcp://*:5570")
	defer pub.Close()

	// 2. Setup HTTP Route for the React UI
	http.HandleFunc("/api/straddle/sell", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Parse into a dynamic map to seamlessly forward all UI configuration fields to C++
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		symbol, _ := req["symbol"].(string)

		// Generate a unique Trade UID
		tradeUID := fmt.Sprintf("ny%d", time.Now().Unix())

		// Construct the internal TradeCommand
		cmd := map[string]interface{}{
			"command_id":  fmt.Sprintf("CMD_%d", time.Now().UnixNano()),
			"trade_id":    tradeUID,
			"strategy_id": "SHORT_STRADDLE",
			"action":      "START",
			"params":      req,
			"timestamp":   time.Now().UnixMilli(),
		}

		cmdBytes, _ := json.Marshal(cmd)

		// Fire it to C++ instantly
		pub.SendMessage("TRADE_CMD", string(cmdBytes))
		slog.Info("🚀 Sent Trade Command to C++ Worker", "trade_id", tradeUID, "symbol", symbol)

		// Respond to UI
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"trade_uid": tradeUID,
			"message":   "Straddle command dispatched to C++ engine",
		})
	})

	// 3. Setup HTTP Route for Square-Off
	http.HandleFunc("/api/trade/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		parts := strings.Split(r.URL.Path, "/")
		// Expected path: /api/trade/{tradeUid}/square-off
		if len(parts) >= 5 && parts[4] == "square-off" {
			tradeUID := parts[3]

			cmd := map[string]interface{}{
				"command_id": fmt.Sprintf("CMD_%d", time.Now().UnixNano()),
				"trade_id":   tradeUID,
				"action":     "SQUARE_OFF",
				"timestamp":  time.Now().UnixMilli(),
			}
			cmdBytes, _ := json.Marshal(cmd)
			pub.SendMessage("TRADE_CMD", string(cmdBytes))
			slog.Info("🚀 Sent Square-Off Command to C++ Worker", "trade_id", tradeUID)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "Square-off command dispatched to C++ engine",
			})
			return
		}
		http.NotFound(w, r)
	})

	slog.Info("🌐 Control API listening on Port 8005 (Command Bus: ZMQ 5570)")
	http.ListenAndServe(":8005", nil)
}
