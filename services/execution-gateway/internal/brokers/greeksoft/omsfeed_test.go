package greeksoft

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOMSFeed_GetVerifiedFills_NilDB(t *testing.T) {
	f := NewOMSFeed(nil, "")
	_, err := f.GetVerifiedFills(context.Background())
	if err == nil {
		t.Fatal("expected an error for a nil db")
	}
}

func TestOMSFeed_ReconcilerHealthy_NoURLConfigured(t *testing.T) {
	f := NewOMSFeed(nil, "")
	if !f.reconcilerHealthy(context.Background()) {
		t.Fatal("expected reconcilerHealthy to return true when no health URL is configured (trust the DB path)")
	}
}

func TestOMSFeed_ReconcilerHealthy_HealthyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := NewOMSFeed(nil, server.URL)
	if !f.reconcilerHealthy(context.Background()) {
		t.Fatal("expected reconcilerHealthy = true for a 200 OK endpoint")
	}
}

func TestOMSFeed_ReconcilerHealthy_UnhealthyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	f := NewOMSFeed(nil, server.URL)
	if f.reconcilerHealthy(context.Background()) {
		t.Fatal("expected reconcilerHealthy = false for a 500 endpoint")
	}
}

func TestOMSFeed_ReconcilerHealthy_UnreachableEndpoint(t *testing.T) {
	f := NewOMSFeed(nil, "http://127.0.0.1:1") // nothing listens on port 1
	if f.reconcilerHealthy(context.Background()) {
		t.Fatal("expected reconcilerHealthy = false for an unreachable endpoint")
	}
}

func TestOMSFeed_ReconcilerHealthy_ResultIsCached(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	f := NewOMSFeed(nil, server.URL)
	f.healthCache = time.Hour // never expires within this test

	for i := 0; i < 5; i++ {
		if !f.reconcilerHealthy(context.Background()) {
			t.Fatal("expected reconcilerHealthy = true")
		}
	}
	if calls != 1 {
		t.Fatalf("health endpoint called %d times, want 1 (cached result should avoid re-checking every call)", calls)
	}
}
