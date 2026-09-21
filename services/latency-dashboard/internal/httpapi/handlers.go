// Package httpapi exposes the latency-dashboard's read-only REST API.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
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

// StageStatsStore and OrderEvidenceStore are optional extensions of
// DataStore; the extra endpoints are only served when the store has them.
type StageStatsStore interface {
	StatsForStage(ctx context.Context, stage string, window time.Duration) (store.Stats, error)
}

type OrderEvidenceStore interface {
	Orders(ctx context.Context, window time.Duration, limit int) ([]store.OrderEvidence, error)
}

type Handlers struct {
	store         DataStore
	reconcilerURL string
	httpClient    *http.Client
}

func NewHandlers(s DataStore) *Handlers {
	return &Handlers{
		store:         s,
		reconcilerURL: "http://127.0.0.1:8021",
		httpClient:    &http.Client{Timeout: 2 * time.Second},
	}
}

// WithReconcilerURL sets where /api/latency/iris reads the Iris status from.
func (h *Handlers) WithReconcilerURL(u string) *Handlers {
	h.reconcilerURL = u
	return h
}

// parseWindow accepts a Go duration ("5m", "1h") or "today" (since local
// midnight).
func parseWindow(raw string, now time.Time) (time.Duration, error) {
	if raw == "today" {
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		d := now.Sub(midnight)
		if d < time.Second {
			d = time.Second
		}
		return d, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid window")
	}
	return d, nil
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/latency/recent", h.handleRecent)
	mux.HandleFunc("/api/latency/stats", h.handleStats)
	mux.HandleFunc("/api/latency/iris", h.handleIris)
	if _, ok := h.store.(OrderEvidenceStore); ok {
		mux.HandleFunc("/api/latency/orders", h.handleOrders)
	}
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
		parsed, err := parseWindow(raw, time.Now())
		if err != nil {
			http.Error(w, "invalid window", http.StatusBadRequest)
			return
		}
		window = parsed
	}

	var (
		stats store.Stats
		err   error
	)
	if stage := r.URL.Query().Get("stage"); stage != "" {
		ss, ok := h.store.(StageStatsStore)
		if !ok {
			http.Error(w, "stage not supported", http.StatusBadRequest)
			return
		}
		stats, err = ss.StatsForStage(r.Context(), stage, window)
	} else {
		stats, err = h.store.Stats(r.Context(), window)
	}
	if err != nil {
		log.Printf("[LATENCY-DASHBOARD] Stats failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, stats)
}

func (h *Handlers) handleOrders(w http.ResponseWriter, r *http.Request) {
	window := 24 * time.Hour
	if raw := r.URL.Query().Get("window"); raw != "" {
		parsed, err := parseWindow(raw, time.Now())
		if err != nil {
			http.Error(w, "invalid window", http.StatusBadRequest)
			return
		}
		window = parsed
	}
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

	orders, err := h.store.(OrderEvidenceStore).Orders(r.Context(), window, limit)
	if err != nil {
		log.Printf("[LATENCY-DASHBOARD] Orders failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, orders)
}

// handleIris relays the reconciler's live Iris websocket status, so the UI
// needs only this one service (already proxied) to show it.
func (h *Handlers) handleIris(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.reconcilerURL+"/api/iris/status", nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		writeJSON(w, map[string]interface{}{"reachable": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var status json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil || resp.StatusCode != http.StatusOK {
		writeJSON(w, map[string]interface{}{"reachable": false, "error": fmt.Sprintf("reconciler status HTTP %d", resp.StatusCode)})
		return
	}
	writeJSON(w, map[string]interface{}{"reachable": true, "status": status})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[LATENCY-DASHBOARD] encode response failed: %v", err)
	}
}
