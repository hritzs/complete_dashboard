package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "trading-platform/libs/contracts/session"
	"trading-platform/services/session-manager/internal/config"
	"trading-platform/services/session-manager/internal/manager"
	"trading-platform/services/session-manager/internal/server"
)

const (
	defaultPort       = "50051"
	defaultConfigPath = "session-manager.yaml"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = defaultConfigPath
	}

	// 1. Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Error("Failed to load configuration", "path", configPath, "error", err)
		os.Exit(1)
	}
	slog.Info("Configuration loaded successfully", "path", configPath, "accounts", len(cfg.Accounts))

	// 2. Start the Session Manager
	sessionManager, err := manager.NewSessionManager(cfg)
	if err != nil {
		slog.Error("Failed to start session manager", "error", err)
		os.Exit(1)
	}

	// 3. Start the gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		slog.Error("Failed to listen for gRPC", "port", port, "error", err)
		os.Exit(1)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterSessionServiceServer(grpcServer, server.NewGrpcServer(sessionManager))

	slog.Info("gRPC server listening", "address", lis.Addr())
	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("Failed to serve gRPC", "error", err)
		os.Exit(1)
	}
}
