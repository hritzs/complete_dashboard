package trading

// Automatic broker sync -- so a fill whose confirmation was lost never needs
// a manual sync again (2026-10-06: a CE lot filled at the broker while the
// reconciler's feed reconnected; the platform never knew).
//
//  1. Every 30 s in the session: any order of today still not terminal in
//     our DB 20 s after it was sent (no FILLED / CANCELLED / REJECTED came
//     back) -> its trade gets BrokerSyncTrade(apply): matched against the
//     broker's own order book on broker order id AND our order id (tag);
//     a missed fill is recorded under THAT trade; a closed trade left with
//     an open leg is reopened (RECONCILIATION_REQUIRED) and flagged.
//  2. Every 60 s: the broker's real net position per contract is compared
//     with the sum of our open legs across all trades; any difference is
//     logged loudly and shown in the Portfolio tab.
// Read + record only: it NEVER sends, modifies or cancels an order.

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	autoSyncEvery     = 30 * time.Second
	autoSyncMinAge    = 20 * time.Second // younger orders may still get their confirmation
	autoSyncMaxAge    = 3 * time.Hour
	positionCheckEach = 60 * time.Second
)

// stuckOrderTrades returns the trades that have an order of today which
// was sent (has a broker id) but never reached a terminal status.
func (s *PostgresBackedStore) stuckOrderTrades(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT o.trade_uid
		FROM orders o
		WHERE COALESCE(o.broker_order_id, '') <> ''
		  AND COALESCE(o.trade_uid, '') <> ''
		  AND UPPER(COALESCE(o.status, '')) NOT IN ('FILLED', 'CANCELLED', 'CANCELED', 'REJECTED')
		  AND o.created_at < NOW() - make_interval(secs => $1)
		  AND o.created_at > NOW() - make_interval(secs => $2)`,
		autoSyncMinAge.Seconds(), autoSyncMaxAge.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

// ourOpenByToken sums every OPEN trade leg by contract token (all trades).
func (s *PostgresBackedStore) ourOpenByToken(ctx context.Context) (map[int64]int64, map[int64][]string, error) {
	if s == nil || s.db == nil {
		return nil, nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.broker_token, tl.current_quantity, t.trade_uid
		FROM trade_legs tl
		JOIN trades t ON t.id = tl.trade_id
		JOIN contracts c ON c.id = tl.contract_id
		WHERE tl.status = 'OPEN' AND tl.current_quantity <> 0
		  -- only contracts still alive (old trades from expired series can
		  -- carry stale OPEN legs; the broker has nothing there)
		  AND c.expiry_date >= (NOW() AT TIME ZONE 'Asia/Kolkata')::date`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	qty := map[int64]int64{}
	trades := map[int64][]string{}
	for rows.Next() {
		var tok, q int64
		var uid string
		if err := rows.Scan(&tok, &q, &uid); err != nil {
			return nil, nil, err
		}
		qty[tok] += q
		trades[tok] = append(trades[tok], uid)
	}
	return qty, trades, rows.Err()
}

// positionDiff is one contract where the broker and the platform disagree.
type positionDiff struct {
	Token     int64    `json:"token"`
	Symbol    string   `json:"symbol"`
	BrokerQty int64    `json:"broker_qty"`
	OurQty    int64    `json:"our_qty"`
	Trades    []string `json:"trades,omitempty"`
}

type positionCheck struct {
	At       string         `json:"at"`
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Diffs    []positionDiff `json:"diffs"`
	Synced   []string       `json:"synced,omitempty"` // trades the auto sync recorded fills for (today)
	Reopened []string       `json:"reopened,omitempty"`
}

var (
	posCheckMu   sync.Mutex
	posCheckLast = positionCheck{OK: true}
)

func autoSyncWindow(now time.Time) bool {
	hm := now.Hour()*100 + now.Minute()
	return hm >= 915 && hm <= 1545
}

// StartBrokerAutoSync runs the automatic sync and the position check.
func (s *Service) StartBrokerAutoSync() {
	go func() {
		syncT := time.NewTicker(autoSyncEvery)
		posT := time.NewTicker(positionCheckEach)
		defer syncT.Stop()
		defer posT.Stop()
		for {
			select {
			case <-syncT.C:
				if autoSyncWindow(time.Now().In(lutIST())) {
					s.autoSyncStuckOrders()
				}
			case <-posT.C:
				if autoSyncWindow(time.Now().In(lutIST())) {
					s.checkBrokerPositions()
				}
			}
		}
	}()
}

func (s *Service) autoSyncStuckOrders() {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	uids, err := pg.stuckOrderTrades(ctx)
	if err != nil {
		log.Printf("[BROKER-SYNC] auto: stuck-order query failed: %v", err)
		return
	}
	for _, uid := range uids {
		res, err := s.BrokerSyncTrade(ctx, uid, true)
		if err != nil {
			log.Printf("[BROKER-SYNC] auto: trade=%s: %v", uid, err)
			continue
		}
		if res.ToAdd == 0 {
			continue
		}
		log.Printf("[BROKER-SYNC] ⚠ auto: trade=%s recorded %d broker fill(s) the platform had missed (matched on broker order id + our order id); status %s -> %s; open legs %d",
			uid, res.ToAdd, res.StatusFrom, res.StatusTo, len(res.OpenLegs))
		posCheckMu.Lock()
		posCheckLast.Synced = appendUnique(posCheckLast.Synced, uid)
		if res.StatusTo != res.StatusFrom {
			posCheckLast.Reopened = appendUnique(posCheckLast.Reopened, uid)
		}
		posCheckMu.Unlock()
	}
}

func (s *Service) checkBrokerPositions() {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok || s.BrokerFactory == nil {
		return
	}
	res := positionCheck{At: time.Now().In(lutIST()).Format("15:04:05"), OK: true}
	defer func() {
		posCheckMu.Lock()
		res.Synced = posCheckLast.Synced
		// A reopened trade stays flagged only while it is still open.
		res.Reopened = nil
		for _, uid := range posCheckLast.Reopened {
			if tr, ok := s.Store.LoadTrade(uid); ok && !strings.HasPrefix(strings.ToUpper(tr.Status), "CLOSED") {
				res.Reopened = append(res.Reopened, uid)
			}
		}
		posCheckLast = res
		posCheckMu.Unlock()
	}()
	executor, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		res.OK, res.Error = false, err.Error()
		return
	}
	bp, ok := executor.(BrokerPositionsProvider)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	positions, err := bp.GetBrokerPositions(ctx)
	if err != nil {
		res.OK, res.Error = false, "broker positions: "+err.Error()
		return
	}
	ours, trades, err := pg.ourOpenByToken(ctx)
	if err != nil {
		res.OK, res.Error = false, "our open legs: "+err.Error()
		return
	}
	broker := map[int64]int64{}
	names := map[int64]string{}
	for _, p := range positions {
		tok := p.ExchToken
		if tok == 0 {
			tok = p.Token % 1000000 // 102040710 -> 40710 (fallback)
		}
		broker[tok] += p.NetQty // a contract can appear once per product type
		names[tok] = p.Symbol
	}
	seen := map[int64]bool{}
	for tok := range broker {
		seen[tok] = true
	}
	for tok := range ours {
		seen[tok] = true
	}
	for tok := range seen {
		if broker[tok] == ours[tok] {
			continue
		}
		res.OK = false
		d := positionDiff{Token: tok, Symbol: names[tok], BrokerQty: broker[tok], OurQty: ours[tok], Trades: trades[tok]}
		res.Diffs = append(res.Diffs, d)
		log.Printf("[POSITION-CHECK] ⚠ MISMATCH %s token=%d broker net %d vs platform open %d (trades %s) -- use Sync with Broker on the trade, or check the broker",
			d.Symbol, tok, d.BrokerQty, d.OurQty, strings.Join(d.Trades, ","))
	}
	sort.Slice(res.Diffs, func(i, j int) bool { return res.Diffs[i].Token < res.Diffs[j].Token })
}

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// PositionCheck: GET /api/positions/check -- the last broker-vs-platform
// position comparison (refresh=1 runs it now). Read-only.
func (h *Handlers) PositionCheck(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" {
		h.Service.checkBrokerPositions()
	}
	posCheckMu.Lock()
	out := posCheckLast
	posCheckMu.Unlock()
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "check": out})
}
