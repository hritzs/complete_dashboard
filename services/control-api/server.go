package main

import (
	"log"
	"net/http"

	"trading-platform/services/control-api/internal/client"
	"trading-platform/services/control-api/internal/config"
	"trading-platform/services/control-api/internal/handlers"
	"trading-platform/services/control-api/internal/router"
	"trading-platform/services/control-api/internal/session"
)

func newServer() *http.Server {
	cfg := config.Load()

	httpClient := &http.Client{
		Timeout: cfg.RequestTimeout,
	}
	gatewayClient := client.NewGatewayClient(cfg.GatewayURL, httpClient)
	// As per the architecture plan, create a dedicated client for the broker.
	greeksoftClient := client.NewGreeksoftClient(cfg.Greeksoft.URL, httpClient, &cfg.Greeksoft)

	store := session.NewStore()
	h := handlers.New(cfg, gatewayClient, greeksoftClient, store)
	r := router.New(h)

	return &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: withCORS(r),
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func run() error {
	srv := newServer()
	log.Printf("control-api listening on %s", srv.Addr)
	return srv.ListenAndServe()
}
