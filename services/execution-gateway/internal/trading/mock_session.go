package trading

// Mock session (exchange mock-trading day): sell / buy ONE instrument at a
// time. On a mock day the feed is random -- many strikes have no bid, the
// "ATM" is not where options trade, and a straddle build prices its sells at
// 0.05 and is rejected by the price band (2026-10-10). So this tab does not
// build anything; it
//
//   - lists the instruments that are actually tradable right now: a bid and
//     an ask, close together, and a recent trade (mockScan);
//   - sends one LIMIT order for one instrument, priced a few ticks through
//     the book so it fills (mockPlace) -- REAL broker orders, through the
//     gateway's own session;
//   - keeps its own order list and the net position per instrument (from
//     the broker's confirmed fills), with a close button (mockClose), cancel
//     for an order still resting, and a round trip that sells then buys back
//     (mockRoundTrip);
//   - persists everything (state file + DB mirror) so a restart keeps it.
//
// Nothing here creates a trade, a monitor, a hedge or an exit rule.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	mockConfirm   = "MOCK ORDER"
	mockMaxSpread = 0.05 // |ask-bid| / bid
	mockMaxAge    = 120  // seconds since the last trade
	mockTicks     = 3    // ticks through the book for the default limit
	mockTick      = 0.05
)

// MockInstrument is one tradable instrument found by the scan.
type MockInstrument struct {
	Symbol  string  `json:"symbol"`
	Expiry  string  `json:"expiry"`
	Strike  float64 `json:"strike"`
	Leg     string  `json:"leg"`
	Token   int64   `json:"token"`
	Bid     float64 `json:"bid"`
	Ask     float64 `json:"ask"`
	LTP     float64 `json:"ltp"`
	Spread  float64 `json:"spread_pct"`
	AgeSec  int64   `json:"last_trade_age_sec"`
	LotSize int     `json:"lot_size"`
	SellAt  float64 `json:"sell_at"` // default limit for a sell
	BuyAt   float64 `json:"buy_at"`  // default limit for a buy
}

func (m MockInstrument) name() string {
	return fmt.Sprintf("%s %s %.0f %s", m.Symbol, m.Expiry, m.Strike, m.Leg)
}

// MockOrder is one order sent from the tab.
type MockOrder struct {
	Time          string  `json:"time"`
	Day           string  `json:"day"`
	Name          string  `json:"name"`
	Token         int64   `json:"token"`
	Symbol        string  `json:"symbol"`
	Expiry        string  `json:"expiry"`
	Strike        float64 `json:"strike"`
	Leg           string  `json:"leg"`
	Side          string  `json:"side"`
	Qty           int64   `json:"qty"`
	LotSize       int     `json:"lot_size"`
	Limit         float64 `json:"limit"`
	BrokerOrderID string  `json:"broker_order_id"`
	Status        string  `json:"status"`
	FilledQty     int64   `json:"filled_qty"`
	AvgPrice      float64 `json:"avg_price"`
	Note          string  `json:"note,omitempty"`
	Error         string  `json:"error,omitempty"`
}

func (o MockOrder) terminal() bool {
	switch strings.ToUpper(o.Status) {
	case "FILLED", "CANCELLED", "REJECTED", "SUBMIT_FAILED", "EXPIRED":
		return true
	}
	return false
}

// MockPosition is the net of the tab's filled orders on one instrument.
type MockPosition struct {
	Name     string  `json:"name"`
	Token    int64   `json:"token"`
	Symbol   string  `json:"symbol"`
	Expiry   string  `json:"expiry"`
	Strike   float64 `json:"strike"`
	Leg      string  `json:"leg"`
	LotSize  int     `json:"lot_size"`
	NetQty   int64   `json:"net_qty"` // >0 long, <0 short
	AvgPrice float64 `json:"avg_price"`
	Bid      float64 `json:"bid"`
	Ask      float64 `json:"ask"`
	LTP      float64 `json:"ltp"`
	Realized float64 `json:"realized"`
	MTM      float64 `json:"mtm"` // realized + open at the closing side of the book
}

var mock struct {
	mu      sync.Mutex
	loaded  bool
	orders  []MockOrder
	refresh time.Time
	busy    bool   // a round trip is running
	rtNote  string // its last note
}

func mockFile() string { return filepath.Join(lutDataDir(), "mock_orders.json") }

func mockLoadLocked() {
	if mock.loaded {
		return
	}
	mock.loaded = true
	if b, err := stateRead(mockFile()); err == nil {
		_ = json.Unmarshal(b, &mock.orders)
	}
}

func mockSaveLocked() {
	b, _ := json.MarshalIndent(mock.orders, "", " ")
	if err := os.MkdirAll(lutDataDir(), 0o755); err == nil {
		if err = sbWriteAtomic(mockFile(), b); err != nil {
			log.Printf("[MOCK] ⚠ cannot save orders: %v", err)
		}
	}
}

func mockRound(p float64) float64 { return math.Round(p/mockTick) * mockTick }

// mockScan lists the instruments of symbol's listed expiries (nearest
// first, up to 3) that have a bid, an ask within mockMaxSpread of each other
// and a trade in the last mockMaxAge seconds -- tightest spread first.
func (s *Service) mockScan(ctx context.Context, symbol string) ([]MockInstrument, error) {
	if s.Snapshot == nil {
		return nil, fmt.Errorf("no snapshot client")
	}
	first, err := s.Snapshot.GetOptionChain(ctx, symbol, "")
	if err != nil || first == nil {
		return nil, fmt.Errorf("option chain unavailable: %v", err)
	}
	expiries := []string{first.Expiry}
	type et struct {
		e string
		t time.Time
	}
	var later []et
	if cur, perr := lutParseExpiry(first.Expiry); perr == nil {
		for _, e := range first.AvailableExpiries {
			if t, err := lutParseExpiry(e); err == nil && t.After(cur) {
				later = append(later, et{e, t})
			}
		}
		sort.Slice(later, func(i, j int) bool { return later[i].t.Before(later[j].t) })
		for i := 0; i < len(later) && i < 2; i++ {
			expiries = append(expiries, later[i].e)
		}
	}
	now := time.Now().Unix()
	var out []MockInstrument
	for i, exp := range expiries {
		chain := first
		if i > 0 {
			if chain, err = s.Snapshot.GetOptionChain(ctx, symbol, exp); err != nil || chain == nil {
				continue
			}
		}
		for _, r := range chain.Chain {
			for _, leg := range []struct {
				name          string
				tok           int64
				bid, ask, ltp float64
				ltt           int64
			}{{"CE", r.CEToken, r.CEBid, r.CEAsk, r.CELtp, r.CELTT}, {"PE", r.PEToken, r.PEBid, r.PEAsk, r.PELtp, r.PELTT}} {
				if leg.tok <= 0 || leg.bid < 1 || leg.ask <= 0 {
					continue
				}
				spread := math.Abs(leg.ask-leg.bid) / leg.bid
				age := int64(-1)
				if leg.ltt > 0 {
					age = now - leg.ltt
				}
				if spread > mockMaxSpread || age < 0 || age > mockMaxAge {
					continue
				}
				lo, hi := math.Min(leg.bid, leg.ask), math.Max(leg.bid, leg.ask)
				out = append(out, MockInstrument{Symbol: symbol, Expiry: chain.Expiry, Strike: r.Strike, Leg: leg.name, Token: leg.tok,
					Bid: leg.bid, Ask: leg.ask, LTP: leg.ltp, Spread: math.Round(spread*10000) / 100, AgeSec: age, LotSize: chain.LotSize,
					SellAt: math.Max(mockTick, mockRound(lo-mockTicks*mockTick)), BuyAt: mockRound(hi + mockTicks*mockTick)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Spread != out[j].Spread {
			return out[i].Spread < out[j].Spread
		}
		return out[i].AgeSec < out[j].AgeSec
	})
	return out, nil
}

// mockQuote finds one instrument's live row (any spread / age).
func (s *Service) mockQuote(ctx context.Context, symbol, expiry string, token int64) (MockInstrument, bool) {
	chain, err := s.Snapshot.GetOptionChain(ctx, symbol, expiry)
	if err != nil || chain == nil {
		return MockInstrument{}, false
	}
	for _, r := range chain.Chain {
		m := MockInstrument{Symbol: symbol, Expiry: chain.Expiry, Strike: r.Strike, Token: token, LotSize: chain.LotSize}
		switch token {
		case r.CEToken:
			m.Leg, m.Bid, m.Ask, m.LTP = "CE", r.CEBid, r.CEAsk, r.CELtp
		case r.PEToken:
			m.Leg, m.Bid, m.Ask, m.LTP = "PE", r.PEBid, r.PEAsk, r.PELtp
		default:
			continue
		}
		lo, hi := math.Min(m.Bid, m.Ask), math.Max(m.Bid, m.Ask)
		if lo <= 0 {
			lo = hi
		}
		m.SellAt, m.BuyAt = math.Max(mockTick, mockRound(lo-mockTicks*mockTick)), mockRound(hi+mockTicks*mockTick)
		return m, true
	}
	return MockInstrument{}, false
}

// mockPlace sends one LIMIT order for one instrument (limit 0 = a few ticks
// through the live book) and records it.
func (s *Service) mockPlace(ctx context.Context, symbol, expiry string, token int64, side string, qty int64, limit float64, note string) (MockOrder, error) {
	side = strings.ToUpper(strings.TrimSpace(side))
	if side != "BUY" && side != "SELL" {
		return MockOrder{}, fmt.Errorf("side must be BUY or SELL")
	}
	q, ok := s.mockQuote(ctx, symbol, expiry, token)
	if !ok {
		return MockOrder{}, fmt.Errorf("instrument token %d not found in the %s %s chain", token, symbol, expiry)
	}
	if q.LotSize <= 0 || qty <= 0 || qty%int64(q.LotSize) != 0 {
		return MockOrder{}, fmt.Errorf("quantity %d is not a multiple of the lot size %d", qty, q.LotSize)
	}
	if maxQty := s.resolveMaxOrderQty(symbol, int64(q.LotSize)); qty > maxQty {
		return MockOrder{}, fmt.Errorf("quantity %d is above the per-order maximum %d", qty, maxQty)
	}
	if limit <= 0 {
		limit = q.SellAt
		if side == "BUY" {
			limit = q.BuyAt
		}
	}
	limit = mockRound(limit)
	if limit <= 0 || (side == "BUY" && q.Ask <= 0 && q.Bid <= 0) {
		return MockOrder{}, fmt.Errorf("%s has no live price to trade against", q.name())
	}
	executor, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		return MockOrder{}, err
	}
	now := time.Now().In(lutIST())
	o := MockOrder{Time: now.Format("15:04:05"), Day: now.Format("2006-01-02"), Name: q.name(), Token: token, Symbol: symbol,
		Expiry: q.Expiry, Strike: q.Strike, Leg: q.Leg, Side: side, Qty: qty, LotSize: q.LotSize, Limit: limit, Status: "SUBMIT_FAILED", Note: note}
	id := fmt.Sprintf("MOCK_%d_%d", token, now.UnixNano())
	lp := limit
	res, err := executor.ExecuteOrderIntent(ctx, OrderIntent{IntentID: id, OrderUID: id, Token: token, Symbol: symbol,
		ExchangeSegment: ResolveExchangeSegment(symbol, ""), Side: side, Quantity: qty, LotSize: int64(q.LotSize), OrderType: "LIMIT",
		LimitPrice: &lp, ProductType: "NRML", Phase: "MANUAL", BrokerName: strings.ToUpper(sbLiveBroker), AccountID: sbLiveAccount()})
	if err != nil {
		o.Error = err.Error()
	} else if res != nil {
		o.BrokerOrderID, o.Status = res.BrokerOrderID, res.Status
	}
	log.Printf("[MOCK] %s %s %d @%.2f (bid %.2f ask %.2f) -> order %s status %s %s", side, o.Name, qty, limit, q.Bid, q.Ask, o.BrokerOrderID, o.Status, o.Error)
	mock.mu.Lock()
	mockLoadLocked()
	mock.orders = append(mock.orders, o)
	mock.refresh = time.Time{}
	mockSaveLocked()
	mock.mu.Unlock()
	if o.Error != "" {
		return o, fmt.Errorf("order not sent: %s", o.Error)
	}
	return o, nil
}

// mockRefresh brings today's non-terminal orders up to date from the
// broker's order book (at most once every 1.5 s, only while one is open).
func (s *Service) mockRefresh(ctx context.Context, force bool) {
	mock.mu.Lock()
	mockLoadLocked()
	today := time.Now().In(lutIST()).Format("2006-01-02")
	need := false
	for _, o := range mock.orders {
		if o.Day == today && !o.terminal() && o.BrokerOrderID != "" {
			need = true
		}
	}
	if !need || (!force && time.Since(mock.refresh) < 1500*time.Millisecond) {
		mock.mu.Unlock()
		return
	}
	mock.refresh = time.Now()
	mock.mu.Unlock()

	executor, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		return
	}
	provider, ok := executor.(OrderBookProvider)
	if !ok {
		return
	}
	fills, err := provider.OrderBookOrders(ctx)
	if err != nil {
		log.Printf("[MOCK] order book unavailable: %v", err)
		return
	}
	by := map[string]BrokerFill{}
	for _, f := range fills {
		by[f.BrokerOrderID] = f
	}
	mock.mu.Lock()
	changed := false
	for i := range mock.orders {
		o := &mock.orders[i]
		f, ok := by[o.BrokerOrderID]
		if !ok || o.Day != today || o.BrokerOrderID == "" {
			continue
		}
		st := strings.ToUpper(strings.TrimSpace(f.Status))
		if st == "" || st == "SUBMITTED" {
			st = o.Status
		}
		if f.FilledQty >= o.Qty && o.Qty > 0 {
			st = "FILLED"
		}
		if st != o.Status || f.FilledQty != o.FilledQty || f.AveragePrice != o.AvgPrice {
			o.Status, o.FilledQty, o.AvgPrice, changed = st, f.FilledQty, f.AveragePrice, true
		}
	}
	if changed {
		mockSaveLocked()
	}
	mock.mu.Unlock()
}

// mockPositions nets today's filled orders per instrument and marks them.
func (s *Service) mockPositions(ctx context.Context) ([]MockPosition, float64) {
	mock.mu.Lock()
	mockLoadLocked()
	today := time.Now().In(lutIST()).Format("2006-01-02")
	orders := append([]MockOrder(nil), mock.orders...)
	mock.mu.Unlock()
	type acc struct {
		p                  MockPosition
		soldQ, boughtQ     int64
		soldVal, boughtVal float64
	}
	by := map[int64]*acc{}
	var order []int64
	for _, o := range orders {
		if o.Day != today || o.FilledQty <= 0 {
			continue
		}
		a, ok := by[o.Token]
		if !ok {
			a = &acc{p: MockPosition{Name: o.Name, Token: o.Token, Symbol: o.Symbol, Expiry: o.Expiry, Strike: o.Strike, Leg: o.Leg, LotSize: o.LotSize}}
			by[o.Token] = a
			order = append(order, o.Token)
		}
		v := float64(o.FilledQty) * o.AvgPrice
		if o.Side == "SELL" {
			a.soldQ, a.soldVal = a.soldQ+o.FilledQty, a.soldVal+v
		} else {
			a.boughtQ, a.boughtVal = a.boughtQ+o.FilledQty, a.boughtVal+v
		}
	}
	var out []MockPosition
	total := 0.0
	for _, tok := range order {
		a := by[tok]
		p := a.p
		p.NetQty = a.boughtQ - a.soldQ
		cash := a.soldVal - a.boughtVal
		if q, ok := s.mockQuote(ctx, p.Symbol, p.Expiry, p.Token); ok {
			p.Bid, p.Ask, p.LTP = q.Bid, q.Ask, q.LTP
		}
		switch {
		case p.NetQty == 0:
			p.Realized, p.MTM = cash, cash
		case p.NetQty < 0: // short: closes by buying at the ask
			p.AvgPrice = a.soldVal / float64(a.soldQ)
			px := p.Ask
			if px <= 0 {
				px = p.LTP
			}
			p.MTM = cash - float64(-p.NetQty)*px
		default: // long: closes by selling at the bid
			p.AvgPrice = a.boughtVal / float64(a.boughtQ)
			px := p.Bid
			if px <= 0 {
				px = p.LTP
			}
			p.MTM = cash + float64(p.NetQty)*px
		}
		total += p.MTM
		out = append(out, p)
	}
	return out, total
}

// mockWaitFill waits until the order is terminal (or the timeout).
func (s *Service) mockWaitFill(brokerOrderID string, timeout time.Duration) MockOrder {
	deadline := time.Now().Add(timeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		s.mockRefresh(ctx, true)
		cancel()
		mock.mu.Lock()
		var cur MockOrder
		for _, o := range mock.orders {
			if o.BrokerOrderID == brokerOrderID {
				cur = o
			}
		}
		mock.mu.Unlock()
		if cur.terminal() || time.Now().After(deadline) {
			return cur
		}
		time.Sleep(700 * time.Millisecond)
	}
}

// mockCancel cancels an order still resting at the broker.
func (s *Service) mockCancel(ctx context.Context, brokerOrderID string) error {
	executor, err := s.BrokerFactory.GetExecutor(sbLiveUser, sbLiveBroker, sbLiveAccount())
	if err != nil {
		return err
	}
	c, ok := executor.(OrderCanceller)
	if !ok {
		return fmt.Errorf("this broker cannot cancel orders")
	}
	if err := c.CancelOrder(ctx, brokerOrderID); err != nil {
		return err
	}
	log.Printf("[MOCK] cancel sent for order %s", brokerOrderID)
	s.mockRefresh(ctx, true)
	return nil
}

// mockRoundTrip sells qty of the instrument, waits for the fill, then buys
// the filled quantity back (re-pricing the buy up to 3 times on a fresh
// book). Anything unfilled at the end is cancelled and reported.
func (s *Service) mockRoundTrip(symbol, expiry string, token int64, qty int64) {
	set := func(n string) {
		mock.mu.Lock()
		mock.rtNote = time.Now().In(lutIST()).Format("15:04:05") + " " + n
		mock.mu.Unlock()
		log.Printf("[MOCK] round trip: %s", n)
	}
	defer func() {
		mock.mu.Lock()
		mock.busy = false
		mock.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sell, err := s.mockPlace(ctx, symbol, expiry, token, "SELL", qty, 0, "round trip: sell")
	if err != nil {
		set("sell failed: " + err.Error())
		return
	}
	sell = s.mockWaitFill(sell.BrokerOrderID, 12*time.Second)
	if !sell.terminal() {
		_ = s.mockCancel(ctx, sell.BrokerOrderID)
		sell = s.mockWaitFill(sell.BrokerOrderID, 5*time.Second)
	}
	if sell.FilledQty <= 0 {
		set(fmt.Sprintf("sell %s not filled (%s) -- nothing to buy back", sell.Name, sell.Status))
		return
	}
	set(fmt.Sprintf("sold %s %d @%.2f -- buying back", sell.Name, sell.FilledQty, sell.AvgPrice))
	left := sell.FilledQty
	for try := 1; try <= 3 && left > 0; try++ {
		buy, err := s.mockPlace(ctx, symbol, expiry, token, "BUY", left, 0, fmt.Sprintf("round trip: buy back (try %d)", try))
		if err != nil {
			set("buy back failed: " + err.Error() + fmt.Sprintf(" -- STILL SHORT %d, close it from the position row", left))
			return
		}
		buy = s.mockWaitFill(buy.BrokerOrderID, 10*time.Second)
		if !buy.terminal() {
			_ = s.mockCancel(ctx, buy.BrokerOrderID)
			buy = s.mockWaitFill(buy.BrokerOrderID, 5*time.Second)
		}
		left -= buy.FilledQty
		if buy.FilledQty > 0 {
			set(fmt.Sprintf("bought back %d @%.2f (%d left)", buy.FilledQty, buy.AvgPrice, left))
		}
	}
	if left > 0 {
		set(fmt.Sprintf("STILL SHORT %d of %s after 3 buy tries -- close it from the position row", left, sell.Name))
		return
	}
	set(fmt.Sprintf("done: %s sold %d @%.2f and bought back -- flat", sell.Name, sell.FilledQty, sell.AvgPrice))
}

// MockHandler serves the Mock tab.
//
//	GET  /api/mock?symbol=NIFTY  -> instruments, today's orders, positions
//	POST /api/mock {"action":"order","token":..,"expiry":..,"side":"SELL","lots":1,"price":0,"confirm":"MOCK ORDER"}
//	POST /api/mock {"action":"close","token":..,"confirm":"MOCK ORDER"}        (the net position, opposite side)
//	POST /api/mock {"action":"cancel","broker_order_id":".."}
//	POST /api/mock {"action":"roundtrip","token":..,"expiry":..,"lots":1,"confirm":"MOCK ORDER"}
func (h *Handlers) MockHandler(w http.ResponseWriter, r *http.Request) {
	s := h.Service
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if symbol == "" {
		symbol = lutSymbol
	}
	reply := func(code int, errText string, extra map[string]interface{}) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.mockRefresh(ctx, false)
		inst, serr := s.mockScan(ctx, symbol)
		pos, total := s.mockPositions(ctx)
		mock.mu.Lock()
		today := time.Now().In(lutIST()).Format("2006-01-02")
		orders := []MockOrder{}
		for i := len(mock.orders) - 1; i >= 0; i-- {
			if mock.orders[i].Day == today {
				orders = append(orders, mock.orders[i])
			}
		}
		busy, note := mock.busy, mock.rtNote
		mock.mu.Unlock()
		if inst == nil {
			inst = []MockInstrument{}
		}
		if pos == nil {
			pos = []MockPosition{}
		}
		out := map[string]interface{}{"success": errText == "", "symbol": symbol, "instruments": inst, "orders": orders,
			"positions": pos, "total_mtm": total, "confirm_text": mockConfirm, "round_trip_running": busy, "round_trip_note": note,
			"account": strings.ToUpper(sbLiveBroker) + "/" + sbLiveAccount(), "in_session": sbLiveWindow(time.Now().In(lutIST())),
			"filter": fmt.Sprintf("bid and ask within %.0f%% and a trade in the last %d s", mockMaxSpread*100, mockMaxAge)}
		if errText != "" {
			out["error"] = errText
		}
		if serr != nil && errText == "" {
			out["scan_error"] = serr.Error()
		}
		for k, v := range extra {
			out[k] = v
		}
		lutJSON(w, code, out)
	}
	if r.Method == http.MethodGet {
		reply(http.StatusOK, "", nil)
		return
	}
	if r.Method != http.MethodPost {
		reply(http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	var b struct {
		Action        string  `json:"action"`
		Symbol        string  `json:"symbol"`
		Expiry        string  `json:"expiry"`
		Token         int64   `json:"token"`
		Side          string  `json:"side"`
		Lots          int     `json:"lots"`
		Price         float64 `json:"price"`
		BrokerOrderID string  `json:"broker_order_id"`
		Confirm       string  `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		reply(http.StatusBadRequest, "bad body: "+err.Error(), nil)
		return
	}
	if sym := strings.ToUpper(strings.TrimSpace(b.Symbol)); sym != "" {
		symbol = sym
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	needConfirm := func() bool {
		if strings.TrimSpace(b.Confirm) != mockConfirm {
			reply(http.StatusBadRequest, fmt.Sprintf("this sends a REAL broker order: confirmation text %q required", mockConfirm), nil)
			return false
		}
		if !sbLiveWindow(time.Now().In(lutIST())) {
			reply(http.StatusBadRequest, "outside the broker session (09:15-15:40)", nil)
			return false
		}
		return true
	}
	switch b.Action {
	case "order":
		if !needConfirm() {
			return
		}
		q, ok := s.mockQuote(ctx, symbol, b.Expiry, b.Token)
		if !ok || b.Lots <= 0 {
			reply(http.StatusBadRequest, "instrument not found or lots <= 0", nil)
			return
		}
		o, err := s.mockPlace(ctx, symbol, q.Expiry, b.Token, b.Side, int64(b.Lots*q.LotSize), b.Price, "")
		if err != nil {
			reply(http.StatusBadRequest, err.Error(), nil)
			return
		}
		o = s.mockWaitFill(o.BrokerOrderID, 3*time.Second)
		reply(http.StatusOK, "", map[string]interface{}{"order": o})
	case "close":
		if !needConfirm() {
			return
		}
		pos, _ := s.mockPositions(ctx)
		for _, p := range pos {
			if p.Token != b.Token || p.NetQty == 0 {
				continue
			}
			// Never more than the net position; an order still resting on the
			// closing side counts as already sent.
			side, qty := "BUY", -p.NetQty
			if p.NetQty > 0 {
				side, qty = "SELL", p.NetQty
			}
			mock.mu.Lock()
			for _, o := range mock.orders {
				if o.Token == p.Token && o.Side == side && !o.terminal() && o.Day == time.Now().In(lutIST()).Format("2006-01-02") {
					qty -= o.Qty - o.FilledQty
				}
			}
			mock.mu.Unlock()
			if qty <= 0 {
				reply(http.StatusBadRequest, "a closing order for this position is already resting -- cancel it first or wait", nil)
				return
			}
			o, err := s.mockPlace(ctx, p.Symbol, p.Expiry, p.Token, side, qty, b.Price, "close position")
			if err != nil {
				reply(http.StatusBadRequest, err.Error(), nil)
				return
			}
			o = s.mockWaitFill(o.BrokerOrderID, 3*time.Second)
			reply(http.StatusOK, "", map[string]interface{}{"order": o})
			return
		}
		reply(http.StatusBadRequest, "no open mock position on that instrument", nil)
	case "cancel":
		if err := s.mockCancel(ctx, strings.TrimSpace(b.BrokerOrderID)); err != nil {
			reply(http.StatusBadRequest, err.Error(), nil)
			return
		}
		reply(http.StatusOK, "", nil)
	case "roundtrip":
		if !needConfirm() {
			return
		}
		q, ok := s.mockQuote(ctx, symbol, b.Expiry, b.Token)
		if !ok || b.Lots <= 0 {
			reply(http.StatusBadRequest, "instrument not found or lots <= 0", nil)
			return
		}
		mock.mu.Lock()
		if mock.busy {
			mock.mu.Unlock()
			reply(http.StatusBadRequest, "a round trip is already running", nil)
			return
		}
		mock.busy, mock.rtNote = true, "starting"
		mock.mu.Unlock()
		go s.mockRoundTrip(symbol, q.Expiry, b.Token, int64(b.Lots*q.LotSize))
		reply(http.StatusOK, "", nil)
	default:
		reply(http.StatusBadRequest, "unknown action", nil)
	}
}
