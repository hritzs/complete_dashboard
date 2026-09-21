package trading

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// expirySnapshot knows a fixed set of expiries; asking for any other errors.
type expirySnapshot struct{ valid map[string]bool }

func (e expirySnapshot) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	if !e.valid[expiry] {
		return nil, fmt.Errorf("no chain for %s %s", symbol, expiry)
	}
	return &OptionChainSnapshot{Symbol: symbol, Expiry: expiry}, nil
}
func (expirySnapshot) PushSnapshot(ctx context.Context, s TradeSnapshot) error { return nil }

// mismatchSnapshot ignores the requested expiry and returns another one, the
// way a fallback would.
type mismatchSnapshot struct{}

func (mismatchSnapshot) GetOptionChain(ctx context.Context, symbol, expiry string) (*OptionChainSnapshot, error) {
	return &OptionChainSnapshot{Symbol: symbol, Expiry: "22-SEP-26"}, nil
}
func (mismatchSnapshot) PushSnapshot(ctx context.Context, s TradeSnapshot) error { return nil }

func postConfigBuild(h *Handlers, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	rec := httptest.NewRecorder()
	h.ConfigBuild(rec, httptest.NewRequest(http.MethodPost, "/api/trade/straddle/automated", strings.NewReader(body)))
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestConfigBuild_RequiresAnExplicitValidExpiry(t *testing.T) {
	entry := time.Now().Add(2 * time.Hour).Format("15:04:05")
	// keep the entry inside today so ParseTodayIST accepts it
	if time.Now().Hour() >= 21 {
		t.Skip("too late in the day for a future entry time today")
	}
	base := func(expiry string) string {
		return fmt.Sprintf(`{"broker_name":"GREEKSOFT","account_id":"147","symbol":"NIFTY","size":1,"entry_time":%q,"target_expiry":%q}`, entry, expiry)
	}

	h := NewHandlers(&Service{Snapshot: expirySnapshot{valid: map[string]bool{"22-SEP-26": true, "23-NOV-26": true}}}, NewMemoryStore())

	rec, out := postConfigBuild(h, base(""))
	if rec.Code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "target_expiry is required") {
		t.Fatalf("missing expiry: code=%d body=%v", rec.Code, out)
	}
	if len(h.Scheduler.List()) != 0 {
		t.Fatal("a build was scheduled without an expiry")
	}

	rec, out = postConfigBuild(h, base("99-XXX-99"))
	if rec.Code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "not available") {
		t.Fatalf("unknown expiry: code=%d body=%v", rec.Code, out)
	}

	rec, out = postConfigBuild(h, base("22-SEP-26"))
	if rec.Code != 200 || out["expiry"] != "22-SEP-26" {
		t.Fatalf("valid expiry: code=%d body=%v", rec.Code, out)
	}
	jobs := h.Scheduler.List()
	if len(jobs) != 1 || jobs[0].Request.TargetExpiry != "22-SEP-26" {
		t.Fatalf("scheduled jobs = %+v, want one for 22-SEP-26", jobs)
	}
	h.Scheduler.Cancel(jobs[0].ID)
}

func TestConfigBuild_RejectsAFallbackExpiry(t *testing.T) {
	if time.Now().Hour() >= 21 {
		t.Skip("too late in the day for a future entry time today")
	}
	entry := time.Now().Add(2 * time.Hour).Format("15:04:05")
	h := NewHandlers(&Service{Snapshot: mismatchSnapshot{}}, NewMemoryStore())
	rec, out := postConfigBuild(h, fmt.Sprintf(`{"broker_name":"GREEKSOFT","account_id":"147","symbol":"NIFTY","size":1,"entry_time":%q,"target_expiry":"23-NOV-26"}`, entry))
	if rec.Code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "not scheduling") {
		t.Fatalf("code=%d body=%v -- asking for NOV and getting the weekly chain back must not schedule", rec.Code, out)
	}
	if len(h.Scheduler.List()) != 0 {
		t.Fatal("scheduled despite an expiry mismatch")
	}
}
