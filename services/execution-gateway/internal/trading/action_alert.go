package trading

// Action alerts: something a trade did not complete -- a hedge left
// unfilled, a build short of its size, an exit with legs still open, live
// out of step with the Paper Sim. Each one is printed, recorded as an
// "action_alert" event, pushed to the UI banner shown on every page, and
// kept for /api/alerts. Nothing is sent to the broker: the operator decides
// (owner 2026-10-28: "first print and ask").

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// ActionAlert is one entry of the banner.
type ActionAlert struct {
	Time     string `json:"time"` // HH:MM:SS IST
	Day      string `json:"day"`
	TradeUID string `json:"trade_uid"`
	Kind     string `json:"kind"`   // HEDGE_UNFILLED, BUILD_UNFILLED, EXIT_UNFILLED, SIM_MISMATCH, ...
	Reason   string `json:"reason"` // why it happened, in words
	Ask      string `json:"ask,omitempty"`
}

const maxActionAlerts = 100

var actionAlerts struct {
	mu   sync.Mutex
	list []ActionAlert
}

// actionAlert prints, records, keeps and pushes one alert.
func (s *Service) actionAlert(tradeUID, kind, reason, ask string) {
	now := time.Now().In(lutIST())
	a := ActionAlert{Time: now.Format("15:04:05"), Day: now.Format("2006-01-02"), TradeUID: tradeUID, Kind: kind, Reason: reason, Ask: ask}
	log.Printf("[ALERT][%s] %s: %s%s", tradeUID, kind, reason, map[bool]string{true: " -- " + ask, false: ""}[ask != ""])
	actionAlerts.mu.Lock()
	actionAlerts.list = append(actionAlerts.list, a)
	if n := len(actionAlerts.list); n > maxActionAlerts {
		actionAlerts.list = actionAlerts.list[n-maxActionAlerts:]
	}
	actionAlerts.mu.Unlock()
	if b, err := json.Marshal(a); err == nil {
		stateEvent("action_alert", tradeUID, a.Day, b)
	}
	if s == nil {
		return
	}
	if p, ok := s.Snapshot.(interface {
		PushEvent(ctx context.Context, eventType string, data interface{}) error
	}); ok {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = p.PushEvent(ctx, "action_alert", a)
		}()
	}
}

// ActionAlertsHandler: GET /api/alerts -- today's alerts, newest last.
func (h *Handlers) ActionAlertsHandler(w http.ResponseWriter, r *http.Request) {
	day := time.Now().In(lutIST()).Format("2006-01-02")
	actionAlerts.mu.Lock()
	out := make([]ActionAlert, 0, len(actionAlerts.list))
	for _, a := range actionAlerts.list {
		if a.Day == day {
			out = append(out, a)
		}
	}
	actionAlerts.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "alerts": out})
}
