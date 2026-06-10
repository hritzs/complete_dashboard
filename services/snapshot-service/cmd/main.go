package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"trading-platform/services/snapshot-service/internal/websocket"
	"trading-platform/services/snapshot-service/internal/zmq"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("🚀 Starting Go Snapshot Service (WebSocket Hub)")

	// 1. Initialize WebSocket Server
	wsServer := websocket.NewServer()
	go wsServer.HandleMessages()

	// 2. Initialize ZMQ Subscriber (Connecting to C++ Feed Decoder)
	zmqEndpoint := "tcp://localhost:5556"
	zmqSub, err := zmq.NewSubscriber(zmqEndpoint, wsServer)
	if err != nil {
		slog.Error("Failed to start ZMQ Subscriber", "error", err)
		os.Exit(1)
	}
	go zmqSub.Listen()

	// 3. Start HTTP Server for WebSockets
	http.HandleFunc("/ws/snapshots", wsServer.HandleConnections)
	http.HandleFunc("/ws/data", wsServer.HandleConnections)

	port := "8003"
	slog.Info("🌐 WebSocket Server listening", "port", port)

	go func() {
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("🛑 Shutting down Snapshot Service")
}
