package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trading-platform/services/latency-dashboard/internal/store"
)

type evidenceStore struct {
	fakeStore
	orders       []store.OrderEvidence
	ordersWindow time.Duration
	stage        string
}

func (e *evidenceStore) Orders(ctx context.Context, window time.Duration, limit int) ([]store.OrderEvidence, error) {
	e.ordersWindow = window
	return e.orders, nil
}

func (e *evidenceStore) StatsForStage(ctx context.Context, stage string, window time.Duration) (store.Stats, error) {
	e.stage = stage
	return store.Stats{Count: 3}, nil
}

func TestParseWindow(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 30, 0, 0, time.Local)
	if d, err := parseWindow("today", now); err != nil || d != 12*time.Hour+30*time.Minute {
		t.Fatalf("today = %v, %v, want 12h30m", d, err)
	}
	if d, err := parseWindow("1h", now); err != nil || d != time.Hour {
		t.Fatalf("1h = %v, %v", d, err)
	}
	for _, bad := range []string{"", "nonsense", "-5m", "0s"} {
		if _, err := parseWindow(bad, now); err == nil {
			t.Fatalf("parseWindow(%q) should fail", bad)
		}
	}
}

func TestOrdersAndStageEndpoints(t *testing.T) {
	fake := &evidenceStore{orders: []store.OrderEvidence{{OrderID: 7, Source: "IRIS_WS"}}}
	mux := http.NewServeMux()
	NewHandlers(fake).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/latency/orders?window=1h", nil))
	if rec.Code != 200 || fake.ordersWindow != time.Hour {
		t.Fatalf("orders: code=%d window=%v", rec.Code, fake.ordersWindow)
	}
	var got []store.OrderEvidence
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Source != "IRIS_WS" {
		t.Fatalf("orders body = %s (%v)", rec.Body.String(), err)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/latency/stats?stage=iris_fill", nil))
	if rec.Code != 200 || fake.stage != "iris_fill" {
		t.Fatalf("stage stats: code=%d stage=%q", rec.Code, fake.stage)
	}

	// A store without the optional interfaces must not serve /orders.
	plain := http.NewServeMux()
	NewHandlers(&fakeStore{}).Register(plain)
	rec = httptest.NewRecorder()
	plain.ServeHTTP(rec, httptest.NewRequest("GET", "/api/latency/orders", nil))
	if rec.Code != 404 {
		t.Fatalf("orders on a plain store = %d, want 404", rec.Code)
	}
}

func TestIrisRelay(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/iris/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"connected":true,"order_pushes":4}`))
	}))
	defer up.Close()

	mux := http.NewServeMux()
	NewHandlers(&fakeStore{}).WithReconcilerURL(up.URL).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/latency/iris", nil))
	var body struct {
		Reachable bool `json:"reachable"`
		Status    struct {
			Connected   bool  `json:"connected"`
			OrderPushes int64 `json:"order_pushes"`
		} `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Reachable || !body.Status.Connected || body.Status.OrderPushes != 4 {
		t.Fatalf("relay body = %s (%v)", rec.Body.String(), err)
	}

	down := http.NewServeMux()
	NewHandlers(&fakeStore{}).WithReconcilerURL("http://127.0.0.1:1").Register(down)
	rec = httptest.NewRecorder()
	down.ServeHTTP(rec, httptest.NewRequest("GET", "/api/latency/iris", nil))
	var dbody struct {
		Reachable bool   `json:"reachable"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dbody); err != nil || dbody.Reachable || dbody.Error == "" {
		t.Fatalf("down body = %s (%v)", rec.Body.String(), err)
	}
}
