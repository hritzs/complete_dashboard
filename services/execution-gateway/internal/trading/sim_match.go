package trading

// "Match Sim Delta" on a sim-driven LUT trade (owner, 2026-11-04). A manual
// hedge and whole-lot rounding leave the live delta away from its simulation,
// which never copies them. The button shows the synthetic lots that close the
// gap and, once confirmed, sends them; the simulation itself is not changed.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	simMatchMaxAge   = 5 * time.Second // the simulation or snapshot is too old to size an order from
	simMatchCooldown = 5 * time.Second // a double click must not send it twice
)

// hedgeSides makes ManualHedgeLots trade these sides instead of the ones that
// reduce the live delta: closing a gap to the simulation can add delta.
type hedgeSides struct {
	CE, PE, Reason string
}
type hedgeSidesKey struct{}

// SimMatch is the live delta against the simulation's and the order closing the gap.
type SimMatch struct {
	TradeUID       string  `json:"trade_uid"`
	LiveDelta      float64 `json:"live_delta"`
	SimDelta       float64 `json:"sim_delta"` // scaled to the live size, at the live future
	Gap            float64 `json:"gap"`       // live - sim
	GapLots        float64 `json:"gap_lots"`
	Lots           int64   `json:"lots"` // nearest whole lot; 0 = nothing to do
	Qty            int64   `json:"qty"`
	CESide         string  `json:"ce_side,omitempty"`
	PESide         string  `json:"pe_side,omitempty"`
	Strike         float64 `json:"strike,omitempty"`
	DeltaAfter     float64 `json:"delta_after"`
	PointsOutAfter float64 `json:"points_out_after"`
	PointsAllowed  float64 `json:"points_allowed"`
}

func simMatchOf(tr StoredTrade, snap TradeSnapshot, simPerLot, simFut float64) SimMatch {
	lot := float64(tr.LotSize)
	units := float64(int64(tr.Lots) * int64(tr.LotSize))
	want, gap, _ := simPostCheckCompare(snap.NetDelta, snap.NetGamma, snap.Underlying, simPerLot/math.Max(1, lot), simFut, units, lot)
	m := SimMatch{TradeUID: tr.TradeUID, LiveDelta: snap.NetDelta, SimDelta: want, Gap: gap, GapLots: gap / math.Max(1, lot),
		DeltaAfter: snap.NetDelta, PointsAllowed: snap.PointsAllowed}
	m.Lots = int64(math.Round(math.Abs(gap) / math.Max(1, lot)))
	m.Qty = m.Lots * int64(tr.LotSize)
	if m.Lots > 0 {
		m.CESide, m.PESide = "BUY", "SELL" // adds delta
		m.DeltaAfter += float64(m.Qty)
		if gap > 0 {
			m.CESide, m.PESide = "SELL", "BUY" // takes delta off
			m.DeltaAfter -= 2 * float64(m.Qty)
		}
	}
	if snap.NetGamma != 0 {
		m.PointsOutAfter = math.Abs(m.DeltaAfter / snap.NetGamma)
	}
	return m
}

// simGapFor: the simulation's delta and the gap in lots for a trade's snapshot,
// when this trade is the one driven by its simulation.
func simGapFor(tr StoredTrade, snap TradeSnapshot) (simDelta, gapLots float64, ok bool) {
	simDrive.mu.Lock()
	d := simDrive.d
	if d == nil || d.TradeUID != tr.TradeUID || d.SimFuture <= 0 {
		simDrive.mu.Unlock()
		return 0, 0, false
	}
	perLot, fut := d.SimDelta, d.SimFuture
	simDrive.mu.Unlock()
	m := simMatchOf(tr, snap, perLot, fut)
	return m.SimDelta, m.GapLots, true
}

// SimMatchPreview works out the order without sending anything.
func (s *Service) SimMatchPreview(ctx context.Context, tradeUID string) (SimMatch, error) {
	m, _, _, err := s.simMatchNow(ctx, tradeUID)
	return m, err
}

func (s *Service) simMatchNow(ctx context.Context, tradeUID string) (m SimMatch, perLot, simFut float64, err error) {
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return m, 0, 0, fmt.Errorf("trade not found")
	}
	if tr.Status != "ACTIVE" {
		return m, 0, 0, fmt.Errorf("match sim delta needs an ACTIVE trade, status=%s", tr.Status)
	}
	if tr.Lots <= 0 || tr.LotSize <= 0 {
		return m, 0, 0, fmt.Errorf("trade has no resolved size")
	}
	simDrive.mu.Lock()
	d := simDrive.d
	var last time.Time
	var state, simErr string
	owned := d != nil && d.TradeUID == tradeUID
	if owned {
		last, state, simErr, perLot, simFut = d.LastRun, d.SimState, d.Error, d.SimDelta, d.SimFuture
	}
	simDrive.mu.Unlock()
	switch {
	case !owned:
		return m, 0, 0, fmt.Errorf("%s is not driven by a simulation", tradeUID)
	case simErr != "":
		return m, 0, 0, fmt.Errorf("simulation error: %s", simErr)
	case simFut <= 0 || time.Since(last) > simMatchMaxAge:
		return m, 0, 0, fmt.Errorf("simulation not updating, nothing to size from")
	case state == "EXITED" || state == "NO DATA":
		return m, 0, 0, fmt.Errorf("simulation is %s", state)
	}
	snap, ok := s.Store.LoadSnapshot(tradeUID)
	if !ok {
		return m, 0, 0, fmt.Errorf("snapshot not found")
	}
	if time.Since(snap.Timestamp) > simMatchMaxAge {
		return m, 0, 0, fmt.Errorf("live snapshot is stale, nothing to size from")
	}
	m = simMatchOf(tr, snap, perLot, simFut)
	if m.Lots > int64(tr.Lots) {
		return m, 0, 0, fmt.Errorf("gap of %d lots is bigger than the trade (%d lots): check it by hand, nothing sent", m.Lots, tr.Lots)
	}
	if s.Snapshot != nil {
		if chain, cerr := s.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry); cerr == nil {
			if atm, aerr := FindATMRow(*chain); aerr == nil {
				m.Strike = atm.Strike
			}
		}
	}
	return m, perLot, simFut, nil
}

var simMatchLast = struct {
	mu sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// SimMatchExecute sends the order the owner confirmed. It is re-sized first and
// refused if the gap has since changed side or moved by more than a lot.
func (s *Service) SimMatchExecute(ctx context.Context, tradeUID string, lots int64, ceSide string) (SimMatch, error) {
	m, perLot, simFut, err := s.simMatchNow(ctx, tradeUID)
	if err != nil {
		return m, err
	}
	if lots <= 0 || m.Lots == 0 {
		return m, fmt.Errorf("nothing sent: the gap is now %.1f lot(s)", m.GapLots)
	}
	if !strings.EqualFold(ceSide, m.CESide) || math.Abs(float64(m.Lots-lots)) > 1 {
		return m, fmt.Errorf("nothing sent: the gap moved to %s CE %d lot(s), press the button again", m.CESide, m.Lots)
	}
	simMatchLast.mu.Lock()
	if t, ok := simMatchLast.at[tradeUID]; ok && time.Since(t) < simMatchCooldown {
		simMatchLast.mu.Unlock()
		return m, fmt.Errorf("nothing sent: match sim delta was just pressed on this trade")
	}
	simMatchLast.at[tradeUID] = time.Now()
	simMatchLast.mu.Unlock()

	reason := fmt.Sprintf("MATCH SIM: live delta %+.1f vs sim %+.1f, gap %+.1f lot(s)", m.LiveDelta, m.SimDelta, m.GapLots)
	log.Printf("[MATCH-SIM][%s] %s -> %s CE / %s PE %d lot(s)", tradeUID, reason, m.CESide, m.PESide, lots)
	hctx := context.WithValue(ctx, hedgeSidesKey{}, hedgeSides{CE: m.CESide, PE: m.PESide, Reason: reason})
	if err := s.ManualHedgeLots(hctx, tradeUID, int(lots)); err != nil {
		return m, err
	}
	if tr, ok := s.Store.LoadTrade(tradeUID); ok {
		m.Lots, m.Qty = lots, lots*int64(tr.LotSize)
		go s.simPostCheck(tr, "MATCH SIM", PaperSimHedge{DeltaAfter: perLot, Future: simFut})
	}
	return m, nil
}

// SimMatchHandler: GET /api/trade/{uid}/match-sim shows the order; POST with
// {"lots":N,"ce_side":"BUY","confirm":"MATCH"} sends it.
func (h *Handlers) SimMatchHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "trade" || parts[3] != "match-sim" {
		http.Error(w, "invalid match sim path", http.StatusBadRequest)
		return
	}
	tradeUID := parts[2]
	reply := func(code int, m SimMatch, err error, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		out := map[string]interface{}{"success": err == nil, "match": m, "message": msg}
		if err != nil {
			out["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(out)
	}
	switch r.Method {
	case http.MethodGet:
		m, err := h.Service.SimMatchPreview(r.Context(), tradeUID)
		if err != nil {
			reply(http.StatusBadRequest, m, err, "")
			return
		}
		reply(http.StatusOK, m, nil, "")
	case http.MethodPost:
		var req struct {
			Lots    int64  `json:"lots"`
			CESide  string `json:"ce_side"`
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(http.StatusBadRequest, SimMatch{}, err, "")
			return
		}
		if req.Confirm != "MATCH" {
			reply(http.StatusBadRequest, SimMatch{}, fmt.Errorf("confirm is required"), "")
			return
		}
		m, err := h.Service.SimMatchExecute(context.Background(), tradeUID, req.Lots, req.CESide)
		if err != nil {
			reply(http.StatusBadRequest, m, err, "")
			return
		}
		reply(http.StatusOK, m, nil, fmt.Sprintf("%s CE / %s PE %d lot(s) sent", m.CESide, m.PESide, m.Lots))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
