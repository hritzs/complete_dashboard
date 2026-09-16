// Package httpapi exposes the latency-dashboard's read-only REST API.
package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"trading-platform/services/latency-dashboard/internal/store"
)

// DataStore is the subset of *store.Store the handlers need, so tests can
// swap in a fake without a real Postgres connection.
type DataStore interface {
	Recent(ctx context.Context, limit int) ([]store.Sample, error)
	Stats(ctx context.Context, window time.Duration) (store.Stats, error)
}

type Handlers struct {
	store DataStore
}

func NewHandlers(s DataStore) *Handlers {
	return &Handlers{store: s}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/latency/recent", h.handleRecent)
	mux.HandleFunc("/api/latency/stats", h.handleStats)
}

func (h *Handlers) handleRecent(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	if limit > 2000 {
		limit = 2000
	}

	samples, err := h.store.Recent(r.Context(), limit)
	if err != nil {
		log.Printf("[LATENCY-DASHBOARD] Recent failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if samples == nil {
		samples = []store.Sample{}
	}
	writeJSON(w, samples)
}

func (h *Handlers) handleStats(w http.ResponseWriter, r *http.Request) {
	window := 5 * time.Minute
	if raw := r.URL.Query().Get("window"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			http.Error(w, "invalid window", http.StatusBadRequest)
			return
		}
		window = parsed
	}

	stats, err := h.store.Stats(r.Context(), window)
	if err != nil {
		log.Printf("[LATENCY-DASHBOARD] Stats failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, stats)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[LATENCY-DASHBOARD] encode response failed: %v", err)
	}
}
