package trading

// Broker sync for ONE trade: every order we persisted under the trade is
// looked up in the broker's own order book (REST, not the reconciler DB)
// and matched on BOTH ids -- broker order id == orders.broker_order_id AND
// broker userTag/tag == our intent_id. A broker fill we never recorded
// (e.g. its push was lost while the feed reconnected) is written under the
// trade (orders, fills, trade_legs). If that leaves a leg open, a closed
// trade is reopened as RECONCILIATION_REQUIRED so it shows open with its
// real quantity and can be squared off from the Portfolio card.
// Preview first (apply=false); nothing is written and no order is ever sent.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

type brokerSyncRow struct {
	IntentID      string  `json:"intent_id"`
	BrokerOrderID string  `json:"broker_order_id"`
	Side          string  `json:"side"`
	Leg           string  `json:"leg"`
	Strike        float64 `json:"strike"`
	Token         int64   `json:"token"`
	Qty           int64   `json:"qty"`
	Phase         string  `json:"phase"`
	OurStatus     string  `json:"our_status"`
	OurFilled     int64   `json:"our_filled"`
	BrokerFilled  int64   `json:"broker_filled"`
	BrokerPrice   float64 `json:"broker_price"`
	BrokerTag     string  `json:"broker_tag"`
	Match         string  `json:"match"`  // BOTH / NOT_IN_BROKER_BOOK / TAG_MISMATCH / NO_BROKER_ID
	Action        string  `json:"action"` // OK / ADD_FILL / SKIP
	Note          string  `json:"note,omitempty"`
}

type brokerSyncOrder struct {
	intentID, brokerOrderID, side, leg, status, phase string
	token                                             int64
	strike                                            float64
	qty, filled                                       int64
}

func (s *PostgresBackedStore) loadTradeOrdersForSync(ctx context.Context, tradeUID string) ([]brokerSyncOrder, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.intent_id, COALESCE(o.broker_order_id, ''), o.side, COALESCE(c.option_type, ''),
		       COALESCE(c.broker_token, 0), COALESCE(c.strike_price, 0), o.quantity,
		       COALESCE(o.filled_qty, 0), COALESCE(o.status, ''), COALESCE(o.phase, '')
		FROM orders o
		LEFT JOIN contracts c ON c.id = o.contract_id
		WHERE o.trade_uid = $1
		ORDER BY o.id`, strings.TrimSpace(tradeUID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []brokerSyncOrder
	for rows.Next() {
		var o brokerSyncOrder
		var strike sql.NullFloat64
		if err := rows.Scan(&o.intentID, &o.brokerOrderID, &o.side, &o.leg, &o.token, &strike, &o.qty, &o.filled, &o.status, &o.phase); err != nil {
			return nil, err
		}
		o.strike = strike.Float64
		out = append(out, o)
	}
	return out, rows.Err()
}

// reopenTradeLocked marks a closed trade open again (closed_at cleared).
func (s *PostgresBackedStore) clearTradeClosedAt(ctx context.Context, tradeUID string) {
	if s == nil || s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE trades SET closed_at = NULL WHERE trade_uid = $1`, tradeUID); err != nil {
		log.Printf("[BROKER-SYNC] trade=%s clear closed_at failed: %v", tradeUID, err)
	}
}

type brokerSyncResult struct {
	TradeUID   string          `json:"trade_uid"`
	Applied    bool            `json:"applied"`
	Rows       []brokerSyncRow `json:"rows"`
	ToAdd      int             `json:"to_add"`
	OpenLegs   []OpenLeg       `json:"open_legs"`
	StatusFrom string          `json:"status_from"`
	StatusTo   string          `json:"status_to"`
	Note       string          `json:"note,omitempty"`
}

// BrokerSyncTrade previews (apply=false) or applies the sync for one trade.
func (s *Service) BrokerSyncTrade(ctx context.Context, tradeUID string, apply bool) (brokerSyncResult, error) {
	res := brokerSyncResult{TradeUID: tradeUID, Applied: apply}
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok {
		return res, fmt.Errorf("postgres store unavailable")
	}
	tr, ok := s.Store.LoadTrade(tradeUID)
	if !ok {
		return res, fmt.Errorf("trade %s not found", tradeUID)
	}
	res.StatusFrom, res.StatusTo = tr.Status, tr.Status
	orders, err := pg.loadTradeOrdersForSync(ctx, tradeUID)
	if err != nil {
		return res, fmt.Errorf("load orders: %w", err)
	}
	user := tr.UserID
	if user == "" {
		user = "U001"
	}
	executor, err := s.BrokerFactory.GetExecutor(user, tr.BrokerName, tr.AccountID)
	if err != nil {
		return res, fmt.Errorf("executor: %w", err)
	}
	bookReader, ok := executor.(BrokerOrderBookFillsProvider)
	if !ok {
		return res, fmt.Errorf("broker %s cannot read its order book", tr.BrokerName)
	}
	bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	fills, err := bookReader.GetBrokerOrderBookFills(bctx)
	cancel()
	if err != nil {
		return res, fmt.Errorf("broker order book: %w", err)
	}
	type agg struct {
		qty   int64
		value float64
		tags  []string
	}
	book := map[string]*agg{}
	for _, f := range fills {
		id := strings.TrimSpace(f.BrokerOrderID)
		if id == "" || f.FilledQty <= 0 {
			continue
		}
		a := book[id]
		if a == nil {
			a = &agg{}
			book[id] = a
		}
		a.qty += f.FilledQty
		a.value += float64(f.FilledQty) * f.AveragePrice
		for _, t := range []string{f.BrokerUserTag, f.BrokerTag} {
			if t = strings.TrimSpace(t); t != "" {
				a.tags = append(a.tags, t)
			}
		}
	}

	for _, o := range orders {
		row := brokerSyncRow{IntentID: o.intentID, BrokerOrderID: o.brokerOrderID, Side: o.side, Leg: o.leg, Strike: o.strike,
			Token: o.token, Qty: o.qty, Phase: o.phase, OurStatus: o.status, OurFilled: o.filled, Action: "OK"}
		a := book[o.brokerOrderID]
		switch {
		case o.brokerOrderID == "":
			row.Match, row.Action, row.Note = "NO_BROKER_ID", "SKIP", "order never got a broker order id"
		case a == nil:
			row.Match = "NOT_IN_BROKER_BOOK"
			if o.filled > 0 {
				row.Action, row.Note = "SKIP", "we have a fill the broker book does not show -- check manually"
			} else {
				row.Note = "no fill at the broker (cancelled / rejected / unfilled)"
			}
		default:
			row.BrokerFilled = a.qty
			row.BrokerPrice = math.Round(a.value/float64(a.qty)*100) / 100
			tagOK := false
			for _, t := range a.tags {
				row.BrokerTag = t
				if strings.EqualFold(t, o.intentID) {
					tagOK = true
					break
				}
			}
			if !tagOK {
				row.Match, row.Action = "TAG_MISMATCH", "SKIP"
				row.Note = fmt.Sprintf("broker order %s carries tag %q, ours is %q -- not ours, not synced", o.brokerOrderID, row.BrokerTag, o.intentID)
				break
			}
			row.Match = "BOTH"
			if a.qty > o.filled {
				row.Action = "ADD_FILL"
				row.Note = fmt.Sprintf("broker filled %d @%.2f; we recorded %d", a.qty, row.BrokerPrice, o.filled)
				res.ToAdd++
			}
		}
		res.Rows = append(res.Rows, row)
	}

	if apply && res.ToAdd > 0 {
		for _, row := range res.Rows {
			if row.Action != "ADD_FILL" {
				continue
			}
			filled := row.BrokerFilled
			if filled > row.Qty {
				filled = row.Qty
			}
			raw, _ := json.Marshal(map[string]interface{}{"source": "BROKER_SYNC_ORDERBOOK", "broker_order_id": row.BrokerOrderID, "broker_tag": row.BrokerTag, "intent_id": row.IntentID})
			pg.MarkOrderExecution(row.IntentID, row.BrokerOrderID, "FILLED", filled, row.Qty-filled, row.BrokerPrice, string(raw))
			log.Printf("[BROKER-SYNC] trade=%s order %s (ours %s) %s %s %.0f: recorded broker fill %d @%.2f (was %d)",
				tradeUID, row.BrokerOrderID, row.IntentID, row.Side, row.Leg, row.Strike, filled, row.BrokerPrice, row.OurFilled)
		}
		if err := pg.RecomputeTradeLegs(ctx, tradeUID); err != nil {
			log.Printf("[BROKER-SYNC] trade=%s recompute legs: %v", tradeUID, err)
		}
	}

	open, err := pg.LoadOpenLegs(tradeUID)
	if err != nil {
		return res, fmt.Errorf("open legs: %w", err)
	}
	res.OpenLegs = open
	if !apply {
		if res.ToAdd > 0 {
			res.Note = "preview only -- apply to record the missing broker fill(s) under this trade"
		}
		return res, nil
	}

	// A leg is open after the sync: the trade must show OPEN with it.
	if len(open) > 0 && (strings.HasPrefix(strings.ToUpper(tr.Status), "CLOSED") || tr.Status == sbStatusClosed) {
		var ce, pe int64
		for _, l := range open {
			q := l.Qty
			if q < 0 {
				q = -q
			}
			switch strings.ToUpper(l.OptionType) {
			case "CE":
				ce += q
				if tr.CEToken == 0 {
					tr.CEToken = l.Token
				}
			case "PE":
				pe += q
				if tr.PEToken == 0 {
					tr.PEToken = l.Token
				}
			}
		}
		if tr.Strike == 0 {
			for _, row := range res.Rows {
				if row.Strike > 0 {
					tr.Strike = row.Strike
					break
				}
			}
		}
		tr.CEQty, tr.PEQty = int(ce), int(pe)
		tr.Status = "RECONCILIATION_REQUIRED"
		tr.ClosedAt = time.Time{}
		tr.LastUpdateTime = time.Now()
		s.Store.UpdateTrade(tr)
		pg.clearTradeClosedAt(ctx, tradeUID)
		res.StatusTo = tr.Status
		log.Printf("[BROKER-SYNC] trade=%s REOPENED %s -> %s: open CE %d / PE %d after recording broker fills -- square it off from the Portfolio card",
			tradeUID, res.StatusFrom, tr.Status, ce, pe)
	}
	return res, nil
}

// TradeBrokerSync: POST /api/trade/broker-sync {"trade_uid": "...", "apply": false|true}.
func (h *Handlers) TradeBrokerSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		lutJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "method not allowed"})
		return
	}
	var b struct {
		TradeUID string `json:"trade_uid"`
		Apply    bool   `json:"apply"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := h.Service.BrokerSyncTrade(r.Context(), strings.TrimSpace(b.TradeUID), b.Apply)
	if err != nil {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error(), "result": res})
		return
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "result": res})
}

// tradeOpenLeg is one OPEN leg of a trade from the DB (trade_legs, built
// from exchange fills) valued at the live chain price.
type tradeOpenLeg struct {
	Token      int64   `json:"token"`
	OptionType string  `json:"option_type"`
	Strike     float64 `json:"strike"`
	Qty        int64   `json:"qty"` // signed: short < 0
	Action     string  `json:"action"`
	EntryPrice float64 `json:"entry_price"`
	LTP        float64 `json:"ltp"`
	PnL        float64 `json:"pnl"`
	Delta      float64 `json:"delta"` // position delta (option delta x qty)
}

func (s *PostgresBackedStore) loadOpenLegsDetailed(ctx context.Context, tradeUID string) ([]tradeOpenLeg, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.broker_token, COALESCE(c.option_type, ''), COALESCE(c.strike_price, 0),
		       tl.current_quantity, COALESCE(tl.avg_entry_price, 0)
		FROM trade_legs tl
		JOIN trades t ON t.id = tl.trade_id
		JOIN contracts c ON c.id = tl.contract_id
		WHERE t.trade_uid = $1 AND tl.status = 'OPEN' AND tl.current_quantity <> 0
		ORDER BY c.option_type, c.strike_price`, strings.TrimSpace(tradeUID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tradeOpenLeg
	for rows.Next() {
		var l tradeOpenLeg
		var strike, entry sql.NullFloat64
		if err := rows.Scan(&l.Token, &l.OptionType, &strike, &l.Qty, &entry); err != nil {
			return nil, err
		}
		l.Strike, l.EntryPrice = strike.Float64, entry.Float64
		out = append(out, l)
	}
	return out, rows.Err()
}

// TradeOpenLegs: GET /api/trade/open-legs?trade_uid= -- the trade's open
// legs from the DB with live LTP / PnL / delta (works without a running
// monitor, e.g. a RECONCILIATION_REQUIRED trade). Read-only.
func (h *Handlers) TradeOpenLegs(w http.ResponseWriter, r *http.Request) {
	uid := strings.TrimSpace(r.URL.Query().Get("trade_uid"))
	pg, ok := h.Store.(*PostgresBackedStore)
	if !ok || uid == "" {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "trade_uid required / store unavailable"})
		return
	}
	tr, ok := h.Store.LoadTrade(uid)
	if !ok {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "trade not found"})
		return
	}
	legs, err := pg.loadOpenLegsDetailed(r.Context(), uid)
	if err != nil {
		lutJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	var total, delta float64
	if len(legs) > 0 && h.Service.Snapshot != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		chain, cerr := h.Service.Snapshot.GetOptionChain(ctx, tr.Symbol, tr.Expiry)
		cancel()
		for i := range legs {
			l := &legs[i]
			if cerr == nil && chain != nil {
				for _, row := range chain.Chain {
					if l.Token == row.CEToken {
						l.LTP, l.Delta = row.CELtp, row.CEDelta*float64(l.Qty)
					} else if l.Token == row.PEToken {
						l.LTP, l.Delta = row.PELtp, row.PEDelta*float64(l.Qty)
					}
				}
			}
			if l.LTP > 0 && l.EntryPrice > 0 {
				l.PnL = (l.LTP - l.EntryPrice) * float64(l.Qty) // qty signed: short gains when LTP falls
			}
			total += l.PnL
			delta += l.Delta
		}
	}
	for i := range legs {
		legs[i].Action = map[bool]string{true: "SELL", false: "BUY"}[legs[i].Qty < 0]
	}
	lutJSON(w, http.StatusOK, map[string]interface{}{"success": true, "trade_uid": uid, "status": tr.Status, "legs": legs,
		"unrealized_pnl": math.Round(total*100) / 100, "net_delta": math.Round(delta*100) / 100})
}
