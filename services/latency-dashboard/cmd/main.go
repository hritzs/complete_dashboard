// latency-dashboard serves read-only latency stats/recent samples from
// Postgres (written by services/reconciler) to the UI's Latency tab. It
// never writes latency_samples itself. See
// docs/greeksoft-integration-architecture.md and docs/TODO.md for why
// this is REST-only (no NATS, no websocket) at this platform's order
// volume.
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"trading-platform/services/latency-dashboard/internal/httpapi"
	"trading-platform/services/latency-dashboard/internal/store"
)

func requiredEnv(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		log.Fatalf("required environment variable %s is empty", name)
	}
	return v
}

func envOrDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func main() {
	dsn := requiredEnv("POSTGRES_DSN")
	port := envOrDefault("LATENCY_DASHBOARD_PORT", "8023")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping postgres: %v", err)
	}

	dataStore := store.NewStore(db)
	handlers := httpapi.NewHandlers(dataStore).
		WithReconcilerURL("http://127.0.0.1:" + envOrDefault("RECONCILER_HEALTH_PORT", "8021"))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"latency-dashboard"}`))
	})
	handlers.Register(mux)

	server := &http.Server{Addr: ":" + port, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("[LATENCY-DASHBOARD] listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server exited: %v", err)
	}
	log.Printf("[LATENCY-DASHBOARD] shutting down")
}
