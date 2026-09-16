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

type fakeStore struct {
	recent      []store.Sample
	recentErr   error
	recentLimit int // captures the limit passed in, for assertions

	stats       store.Stats
	statsErr    error
	statsWindow time.Duration // captures the window passed in
}

func (f *fakeStore) Recent(ctx context.Context, limit int) ([]store.Sample, error) {
	f.recentLimit = limit
	return f.recent, f.recentErr
}

func (f *fakeStore) Stats(ctx context.Context, window time.Duration) (store.Stats, error) {
	f.statsWindow = window
	return f.stats, f.statsErr
}

func TestHandleRecent_DefaultLimit(t *testing.T) {
	fake := &fakeStore{recent: []store.Sample{{ID: 1, BrokerOrderID: "X"}}}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/recent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.recentLimit != 200 {
		t.Fatalf("limit passed to store = %d, want default 200", fake.recentLimit)
	}
	var got []store.Sample
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0].BrokerOrderID != "X" {
		t.Fatalf("got %+v, want one sample with BrokerOrderID=X", got)
	}
}

func TestHandleRecent_CustomLimitCappedAt2000(t *testing.T) {
	fake := &fakeStore{}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/recent?limit=999999", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.recentLimit != 2000 {
		t.Fatalf("limit passed to store = %d, want capped 2000", fake.recentLimit)
	}
}

func TestHandleRecent_InvalidLimit(t *testing.T) {
	fake := &fakeStore{}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/recent?limit=notanumber", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRecent_NilSliceEncodesAsEmptyArray(t *testing.T) {
	fake := &fakeStore{recent: nil}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/recent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Body.String() != "[]\n" {
		t.Fatalf("body = %q, want empty JSON array (not null)", rec.Body.String())
	}
}

func TestHandleStats_DefaultWindow(t *testing.T) {
	fake := &fakeStore{stats: store.Stats{Count: 5, P99US: 1234}}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.statsWindow != 5*time.Minute {
		t.Fatalf("window passed to store = %v, want default 5m", fake.statsWindow)
	}
	var got store.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Count != 5 || got.P99US != 1234 {
		t.Fatalf("got %+v, want Count=5 P99US=1234", got)
	}
}

func TestHandleStats_CustomWindow(t *testing.T) {
	fake := &fakeStore{}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/stats?window=1h", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.statsWindow != time.Hour {
		t.Fatalf("window passed to store = %v, want 1h", fake.statsWindow)
	}
}

func TestHandleStats_InvalidWindow(t *testing.T) {
	fake := &fakeStore{}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/stats?window=notaduration", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleStats_StoreError(t *testing.T) {
	fake := &fakeStore{statsErr: errTest}
	h := NewHandlers(fake)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/latency/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

var errTest = errTestType("boom")

type errTestType string

func (e errTestType) Error() string { return string(e) }
