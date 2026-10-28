package trading

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestActionAlert_KeptAndServed(t *testing.T) {
	var s *Service // no snapshot client: printed and kept, nothing pushed
	s.actionAlert("T1", "HEDGE_PARTIAL", "AUTO hedge asked 48 lot(s) at 22700: filled CE BUY 1625 / PE SELL 1625 -- capped", "hedge the remaining delta?")
	rec := httptest.NewRecorder()
	(&Handlers{}).ActionAlertsHandler(rec, httptest.NewRequest("GET", "/api/alerts", nil))
	var out struct {
		Alerts []ActionAlert `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if n := len(out.Alerts); n == 0 || out.Alerts[n-1].Kind != "HEDGE_PARTIAL" || out.Alerts[n-1].Ask == "" {
		t.Fatalf("alert not served: %+v", out.Alerts)
	}
}
