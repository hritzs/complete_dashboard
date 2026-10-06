package trading

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Stop building (OMS) for a regular build -- manual, automation, LUT; every
// one goes through executeBuild. Setting the flag stops new build orders
// and the leftover chase (resting orders are cancelled); the build then
// finishes the normal way: fills are verified and the filled part is
// promoted to an ACTIVE partial trade (Config.PartialFill, requested qty
// kept) that the monitor (PMS: hedge / SL / TP / exit time) runs on.

var buildStops sync.Map // trade UID -> *atomic.Bool

func registerBuildStop(uid string) *atomic.Bool {
	f := &atomic.Bool{}
	buildStops.Store(uid, f)
	return f
}

func buildStopRequested(uid string) bool {
	if v, ok := buildStops.Load(uid); ok {
		return v.(*atomic.Bool).Load()
	}
	return false
}

// StopBuild asks the running build of tradeUID to stop sending orders.
func (s *Service) StopBuild(tradeUID string) error {
	v, ok := buildStops.Load(tradeUID)
	if !ok {
		return fmt.Errorf("no build in progress for %s", tradeUID)
	}
	v.(*atomic.Bool).Store(true)
	log.Printf("[BUILD] trade=%s STOP requested by user -- OMS stops; the filled part will be monitored as a partial position", tradeUID)
	return nil
}

// StopBuild: POST /api/build/stop {"trade_uid": "..."}.
func (h *Handlers) StopBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b struct {
		TradeUID string `json:"trade_uid"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := h.Service.StopBuild(strings.TrimSpace(b.TradeUID)); err != nil {
		lutJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// BrokerPositions: GET /api/positions[?broker_name=&account_id=] -- the
// account's REAL net positions as the broker reports them (read-only).
func (h *Handlers) BrokerPositions(w http.ResponseWriter, r *http.Request) {
	broker := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("broker_name")))
	if broker == "" {
		broker = sbLiveBroker
	}
	account := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if account == "" {
		account = sbLiveAccount()
	}
	user := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if user == "" {
		user = sbLiveUser
	}
	executor, err := h.Service.BrokerFactory.GetExecutor(user, broker, account)
	if err != nil {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	p, ok := executor.(BrokerPositionsProvider)
	if !ok {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "broker positions not available for " + broker})
		return
	}
	positions, err := p.GetBrokerPositions(r.Context())
	if err != nil {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	open := make([]BrokerPosition, 0, len(positions))
	for _, x := range positions {
		if x.NetQty != 0 {
			open = append(open, x)
		}
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "account": account, "positions": open, "all": positions})
}
