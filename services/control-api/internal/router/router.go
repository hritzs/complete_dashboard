package router

import (
	"net/http"

	"trading-platform/services/control-api/internal/handlers"
)

// New creates and configures a new HTTP router.
func New(h *handlers.Handlers) http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("POST /login", h.LoginProxyHandler)
	mux.HandleFunc("POST /logout", h.LogoutHandler)
	mux.HandleFunc("GET /api/session/status", h.SessionStatusHandler)
	mux.HandleFunc("POST /api/straddle/sell", h.SellStraddleHandler)
	mux.HandleFunc("/api-proxy/", h.GenericProxyHandler)

	// The root handler for serving the index/login page.
	// It must be registered after more specific API routes to act as a catch-all.
	mux.HandleFunc("/", h.ServeIndexHandler)

	return mux
}
