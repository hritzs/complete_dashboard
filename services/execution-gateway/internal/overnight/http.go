package overnight

// One endpoint for the Overnight tab.
//
//	GET  /api/overnight                     everything the page shows
//	POST /api/overnight {"action": ...}     config | entry | exit | partial | roll | stop | refresh | delete

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type dayRow struct {
	DayMark
	DayPnL float64 `json:"day_pnl"`
}

type legView struct {
	Leg
	BrokerQty *int64 `json:"broker_qty,omitempty"`
}

func (s *Service) view(ctx context.Context) map[string]interface{} {
	s.refreshBrokerPositions()
	live := s.liveNow(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]interface{}{
		"mode": s.st.Mode, "delta": s.st.Delta, "delta_key": deltaKey(s.st.Delta),
		"now": time.Now().In(ist()).Format("15:04:05"), "in_session": inSession(time.Now()),
		"feed_error": s.feedErr, "chain_error": s.chainErr, "broker_error": s.brokerErr,
		"lot_size": s.lot, "max_order_qty": s.maxQ - s.maxQ%max64(s.lot, 1), "live": live,
		"data_dir": s.dir,
	}
	if c := s.chain; c != nil {
		out["symbol"], out["expiry"] = c.Symbol, c.Expiry
		out["days_to_expiry"] = round2(c.ExpiryAt.Sub(time.Now()).Hours() / 24)
	}
	if n := len(s.rows); n > 0 {
		out["last"] = s.rows[n-1]
		from := n - 60
		if from < 0 {
			from = 0
		}
		rows := make([]*Scenario, 0, n-from)
		for i := n - 1; i >= from; i-- {
			rows = append(rows, s.rows[i])
		}
		out["rows"] = rows
	}
	if p := s.st.Position; p != nil {
		out["position"] = s.positionView(p, true)
	}
	if a := s.activeJob(); a != nil {
		out["job"] = a
	}
	n := len(s.st.Jobs)
	jobs := make([]*Job, 0, 8)
	for i := n - 1; i >= 0 && len(jobs) < 8; i-- {
		jobs = append(jobs, s.st.Jobs[i])
	}
	out["jobs"] = jobs
	n = len(s.st.Orders)
	orders := make([]*OrderRec, 0, 150)
	for i := n - 1; i >= 0 && len(orders) < 150; i-- {
		orders = append(orders, s.st.Orders[i])
	}
	out["orders"] = orders
	hist := make([]map[string]interface{}, 0, len(s.st.History))
	for i := len(s.st.History) - 1; i >= 0 && len(hist) < 20; i-- {
		hist = append(hist, s.positionView(s.st.History[i], false))
	}
	out["history"] = hist
	return out
}

// positionView is one position for the page: every strike it ever held
// (current and previous), the day marks and what was executed each day.
// Caller holds s.mu.
func (s *Service) positionView(p *Position, open bool) map[string]interface{} {
	legs := make([]legView, 0, len(p.Legs))
	for _, l := range p.Legs {
		lv := legView{Leg: *l}
		if open && p.Mode == ModeLive && s.brokerPos != nil {
			q := s.brokerPos[l.Token]
			lv.BrokerQty = &q
		}
		legs = append(legs, lv)
	}
	days := make([]dayRow, 0, len(p.Days))
	prev := 0.0
	for _, m := range p.Days {
		days = append(days, dayRow{DayMark: *m, DayPnL: round2(m.Cum - prev)})
		prev = m.Cum
	}
	acts := make([]*DayAct, 0, len(p.Acts))
	for i := len(p.Acts) - 1; i >= 0; i-- {
		acts = append(acts, p.Acts[i])
	}
	return map[string]interface{}{
		"id": p.ID, "mode": p.Mode, "expiry": p.Expiry, "delta": p.Delta, "lot_size": p.LotSize, "opened_at": p.OpenedAt, "closed_at": p.ClosedAt,
		"rolls": p.Rolls, "user_tag": p.userTag(), "legs": legs, "days": days, "acts": acts, "realized": round2(p.realized()), "qty": p.qty(),
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Handler serves /api/overnight.
func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	reply := func(code int, v interface{}) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if r.Method == http.MethodGet {
		reply(http.StatusOK, s.view(ctx))
		return
	}
	if r.Method != http.MethodPost {
		reply(http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
		return
	}
	var b struct {
		Action  string  `json:"action"`
		Delta   float64 `json:"delta"`
		Mode    string  `json:"mode"`
		Confirm string  `json:"confirm"`
		Qty     int64   `json:"qty"`
		Minutes int     `json:"minutes"`
		At      string  `json:"at"`
		ID      string  `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		reply(http.StatusBadRequest, map[string]string{"error": "bad JSON body"})
		return
	}
	var err error
	msg := "ok"
	switch strings.ToLower(strings.TrimSpace(b.Action)) {
	case "config":
		err = s.configure(b.Delta, b.Mode, b.Confirm)
	case "refresh": // recompute the last finished minute now
		now := time.Now().In(ist())
		end := now.Truncate(time.Minute)
		if !inMarket(now) {
			end = time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, ist())
			if now.Before(end) {
				end = end.AddDate(0, 0, -1)
			}
		}
		s.mu.Lock()
		if n := len(s.rows); n > 0 && s.rows[n-1].Ts == end.Unix() {
			s.rows = s.rows[:n-1]
		}
		s.mu.Unlock()
		_, err = s.minuteEnd(end)
	case "entry", "exit", "partial", "roll":
		var j *Job
		j, err = s.Submit(JobReq{Kind: b.Action, Delta: b.Delta, Qty: b.Qty, Minutes: b.Minutes, At: b.At})
		if err == nil {
			msg = fmt.Sprintf("%s job %s %s", j.Kind, j.ID, strings.ToLower(j.Status))
		}
	case "stop":
		err = s.Stop()
	case "delete":
		err = s.deletePosition(b.ID)
	default:
		err = fmt.Errorf("unknown action %q", b.Action)
	}
	if err != nil {
		reply(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	v := s.view(ctx)
	v["message"] = msg
	reply(http.StatusOK, v)
}

// configure sets the delta the scenarios follow and the mode. Switching to
// LIVE needs the word LIVE typed, and no position / job in the other mode.
func (s *Service) configure(delta float64, mode, confirm string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if delta != 0 {
		if delta <= 0 || delta >= 1 {
			return fmt.Errorf("delta must be between 0 and 1 (e.g. 0.25)")
		}
		s.st.Delta = round4(delta)
		s.liveView = nil
	}
	if mode = strings.ToUpper(strings.TrimSpace(mode)); mode != "" && mode != s.st.Mode {
		if mode != ModeLive && mode != ModePaper {
			return fmt.Errorf("mode must be PAPER or LIVE")
		}
		if s.st.Position != nil || s.activeJob() != nil {
			return fmt.Errorf("close the open %s position / job before changing the mode", s.st.Mode)
		}
		if mode == ModeLive && strings.TrimSpace(confirm) != "LIVE" {
			return fmt.Errorf("type LIVE to confirm real broker orders")
		}
		s.st.Mode = mode
	}
	return s.st.save(s.dir)
}

// deletePosition removes one position, with its orders, from the tab so it
// is not shown again. Allowed for a closed position and for an open PAPER
// one; an open LIVE position is real quantity at the broker and has to be
// exited first. What is removed is appended to deleted.jsonl in the data
// directory, never lost.
func (s *Service) deletePosition(id string) error {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return fmt.Errorf("position id is required")
	}
	if s.activeJob() != nil {
		return fmt.Errorf("a job is running -- stop it or let it finish first")
	}
	var gone *Position
	if p := s.st.Position; p != nil && p.ID == id {
		if p.Mode == ModeLive && p.qty() > 0 {
			return fmt.Errorf("position %s is LIVE and still holds %d -- exit it first", id, p.qty())
		}
		gone, s.st.Position = p, nil
	} else {
		for i, h := range s.st.History {
			if h.ID == id {
				gone = h
				s.st.History = append(s.st.History[:i:i], s.st.History[i+1:]...)
				break
			}
		}
	}
	if gone == nil {
		return fmt.Errorf("position %s not found", id)
	}
	var orders, keep []*OrderRec
	for _, o := range s.st.Orders {
		if o.UserTag == gone.userTag() {
			orders = append(orders, o)
		} else {
			keep = append(keep, o)
		}
	}
	s.st.Orders = keep
	appendJSONL(filepath.Join(s.dir, "deleted.jsonl"), map[string]interface{}{
		"deleted_at": time.Now().In(ist()).Format("2006-01-02 15:04:05"), "position": gone, "orders": orders})
	s.liveView = nil
	log.Printf("[OVERNIGHT] position %s (%s) removed from the tab with %d orders; copy kept in deleted.jsonl", gone.ID, gone.Mode, len(orders))
	return s.st.save(s.dir)
}
