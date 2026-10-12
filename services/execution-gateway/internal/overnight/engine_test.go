package overnight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMarket prices a monthly chain off one future level with a flat vol,
// serves it as candle closes (broker) and as ticks (bridge).
type fakeMarket struct {
	mu     sync.Mutex
	f, vol float64
	expiry time.Time
	chain  *Chain

	seq      int
	orders   map[string]*Order
	needPoll map[string]int // polls before an order fills
	modifies int
	placed   []PlaceReq
	reject   string // option type whose orders the broker rejects
}

func (m *fakeMarket) px(tok int64) float64 {
	in, ok := m.chain.byToken(tok)
	if !ok {
		return 0
	}
	if in.Opt == "FUT" {
		return m.f
	}
	t := time.Until(m.expiry).Hours() / (365 * 24)
	return tick(black76(m.f, in.Strike, t, m.vol, in.Opt == "CE"))
}

func (m *fakeMarket) MinuteCloses(_ context.Context, tok int64, _ string) (map[int64]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[int64]float64{1000: m.px(tok)}, nil
}
func (m *fakeMarket) Limits(context.Context, int64) (int64, int64, error) { return 65, 3510, nil }
func (m *fakeMarket) Positions(context.Context) (map[int64]int64, error)  { return nil, nil }
func (m *fakeMarket) FillPrice(context.Context, string) (float64, bool)   { return 0, false }
func (m *fakeMarket) Place(_ context.Context, r PlaceReq) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := strconv.Itoa(m.seq)
	in, _ := m.chain.byToken(r.Token)
	o := &Order{ID: id, Status: "OPEN", Qty: r.Qty, Price: r.Price}
	if m.reject != "" && in.Opt == m.reject {
		o.Status, o.Reason = "REJECTED", "RMS Rejected test"
	}
	m.orders[id] = o
	m.needPoll[id] = 2
	m.placed = append(m.placed, r)
	return id, nil
}
func (m *fakeMarket) Modify(_ context.Context, id string, price float64, _, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modifies++
	m.orders[id].Price = price
	return nil
}
func (m *fakeMarket) Cancel(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o := m.orders[id]; o.Status == "OPEN" {
		o.Status = "CANCELLED"
	}
	return nil
}
func (m *fakeMarket) Orders(context.Context) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for id, o := range m.orders {
		if o.Status == "OPEN" {
			if m.needPoll[id]--; m.needPoll[id] <= 0 {
				o.Status, o.Filled = "FILLED", o.Qty
			} else if o.Filled == 0 {
				o.Filled = o.Qty / 2 / 65 * 65 // a partial fill first
			}
		}
		out = append(out, *o)
	}
	return out, nil
}

func (m *fakeMarket) bridge() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/subscribe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"added":1}`)) })
	mux.HandleFunc("/api/quotes", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		q := map[string]Quote{}
		for _, t := range strings.Split(r.URL.Query().Get("tokens"), ",") {
			g, _ := strconv.ParseInt(t, 10, 64)
			if g == 0 {
				continue
			}
			p := m.px(g - 100000000) // gtoken = 100000000 + token in the test file
			q[t] = Quote{LTP: p, Bid: p - 0.05, Ask: p + 0.05, AtMs: time.Now().UnixMilli()}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"quotes": q, "index": map[string]Quote{"Nifty 50": {LTP: m.f - 40}}})
	})
	return httptest.NewServer(mux)
}

func newTestService(t *testing.T) (*Service, *fakeMarket) {
	t.Helper()
	dir := t.TempDir()
	exp := time.Now().In(ist()).AddDate(0, 0, 15)
	raw := strings.ToUpper(exp.Format("02-Jan-06"))
	var sb strings.Builder
	sb.WriteString("xx.Token,Exchange,Type,Symbol,Expiry,Strike,OpType,Token\n")
	sb.WriteString(fmt.Sprintf("100000001,NSE,FUTIDX,NIFTY,%s,0,XX,1\n", raw))
	tok := 10
	for k := 20000; k <= 27000; k += 50 {
		sb.WriteString(fmt.Sprintf("%d,NSE,OPTIDX,NIFTY,%s,%d,CE,%d\n", 100000000+tok, raw, k, tok))
		sb.WriteString(fmt.Sprintf("%d,NSE,OPTIDX,NIFTY,%s,%d,PE,%d\n", 100000000+tok+1, raw, k, tok+1))
		tok += 2
	}
	csvPath := filepath.Join(dir, "IndexTokens.csv")
	if err := os.WriteFile(csvPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INDEX_TOKENS_PATH", csvPath)
	t.Setenv("OVERNIGHT_DATA_DIR", filepath.Join(dir, "data"))

	m := &fakeMarket{f: 23512, vol: 0.14, orders: map[string]*Order{}, needPoll: map[string]int{}}
	srv := m.bridge()
	t.Cleanup(srv.Close)
	t.Setenv("OVERNIGHT_QUOTE_URL", srv.URL)
	s := New(m)
	s.loadChain(time.Now().In(ist()))
	if s.chain == nil {
		t.Fatalf("chain not loaded: %s", s.chainErr)
	}
	m.chain, m.expiry = s.chain, s.chain.ExpiryAt
	old := sessionOpen
	sessionOpen = func(time.Time) bool { return true }
	t.Cleanup(func() { sessionOpen = old })
	return s, m
}

func waitJob(t *testing.T, s *Service) *Job {
	t.Helper()
	for i := 0; i < 400; i++ {
		s.mu.Lock()
		a := s.activeJob()
		last := s.st.Jobs[len(s.st.Jobs)-1]
		s.mu.Unlock()
		if a == nil {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}

func TestPicksNearestDeltaLowerStrikeOnTie(t *testing.T) {
	s, m := newTestService(t)
	sc, err := s.compute(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sc.ATM != 23500 {
		t.Fatalf("ATM = %v, want 23500 (synthetic %v)", sc.ATM, sc.Synth)
	}
	pk := sc.picks(0.25)
	if pk == nil || pk.CE == nil || pk.PE == nil {
		t.Fatalf("no picks: %+v", sc.Picks)
	}
	if pk.CE.Strike <= sc.ATM || pk.PE.Strike >= sc.ATM {
		t.Fatalf("picks are not OTM: CE %v PE %v", pk.CE.Strike, pk.PE.Strike)
	}
	// Brute force over the whole chain must agree.
	tt := m.expiry.Sub(time.Now()).Hours() / (365 * 24)
	for _, call := range []bool{true, false} {
		best, bestD := 0.0, 9.0
		for _, k := range s.chain.Strikes {
			if (call && k < sc.ATM) || (!call && k > sc.ATM) {
				continue
			}
			opt := "PE"
			if call {
				opt = "CE"
			}
			in, _ := s.chain.leg(k, opt)
			iv, ok := impliedVol(m.px(in.Token), sc.Synth, k, tt, call)
			if !ok {
				continue
			}
			d := delta76(sc.Synth, k, tt, iv, call)
			if d < 0 {
				d = -d
			}
			if gap := round4(abs(round4(d) - 0.25)); gap < bestD { // ascending strikes: first (lower) wins a tie
				best, bestD = k, gap
			}
		}
		got := pk.PE.Strike
		if call {
			got = pk.CE.Strike
		}
		if got != best {
			t.Fatalf("call=%v picked %v, brute force says %v", call, got, best)
		}
	}
	if sc.Requests > 14 {
		t.Fatalf("first minute used %d candle requests", sc.Requests)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestPaperEntryRollExit(t *testing.T) {
	s, m := newTestService(t)
	if _, err := s.minuteEnd(time.Now().In(ist()).Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(JobReq{Kind: "entry", Delta: 0.25, Qty: 10010}); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, s)
	if j.Status != "DONE" {
		t.Fatalf("entry %s: %s %v", j.Status, j.Note, j.Log)
	}
	p := s.st.Position
	if p == nil || len(p.Legs) != 2 || p.Legs[0].Qty != 10010 || p.Legs[1].Qty != 10010 {
		t.Fatalf("position after entry: %+v", p)
	}
	if j.Chunk != 3 { // 10010 = 3510 + 3510 + 2990
		t.Fatalf("entry chunks = %d, want 3", j.Chunk)
	}
	oldCE, oldPE := p.held("CE").Strike, p.held("PE").Strike
	if len(m.placed) != 0 {
		t.Fatalf("PAPER sent %d orders to the broker", len(m.placed))
	}

	// Market moves 500 up: the 0.25-delta strikes move with it.
	m.mu.Lock()
	m.f += 500
	m.mu.Unlock()
	s.mu.Lock()
	s.rows = nil
	s.mu.Unlock()
	if _, err := s.minuteEnd(time.Now().In(ist()).Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(JobReq{Kind: "roll"}); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, s)
	if j.Status != "DONE" {
		t.Fatalf("roll %s: %s %v", j.Status, j.Note, j.Log)
	}
	ce, pe := p.held("CE"), p.held("PE")
	if ce == nil || pe == nil || ce.Qty != 10010 || pe.Qty != 10010 || ce.Strike == oldCE || pe.Strike == oldPE {
		t.Fatalf("after roll CE %+v PE %+v (old %v / %v)", ce, pe, oldCE, oldPE)
	}
	if p.leg(s.chain.CE[oldCE].Token).Qty != 0 || p.Rolls != 1 {
		t.Fatalf("old CE not flat or rolls=%d", p.Rolls)
	}

	if _, err := s.Submit(JobReq{Kind: "partial", Qty: 3000}); err != nil { // 3000 -> 46 lots = 2990
		t.Fatal(err)
	}
	waitJob(t, s)
	if ce.Qty != 10010-2990 || pe.Qty != 10010-2990 {
		t.Fatalf("after partial CE %d PE %d", ce.Qty, pe.Qty)
	}
	if _, err := s.Submit(JobReq{Kind: "exit"}); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, s)
	if j.Status != "DONE" || s.st.Position != nil || len(s.st.History) != 1 {
		t.Fatalf("after exit: job %s position %+v history %d", j.Status, s.st.Position, len(s.st.History))
	}
}

func TestLiveChunkWaitsModifiesAndTags(t *testing.T) {
	s, m := newTestService(t)
	if err := s.configure(0, ModeLive, "LIVE"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.minuteEnd(time.Now().In(ist()).Truncate(time.Minute)); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.needPoll = map[string]int{}
	m.mu.Unlock()
	if _, err := s.Submit(JobReq{Kind: "entry", Delta: 0.25, Qty: 4000}); err != nil { // 61 lots = 3965 -> 3510 + 455
		t.Fatal(err)
	}
	j := waitJob(t, s)
	if j.Status != "DONE" {
		t.Fatalf("entry %s: %s %v", j.Status, j.Note, j.Log)
	}
	p := s.st.Position
	if p.Legs[0].Qty != 3965 || p.Legs[1].Qty != 3965 {
		t.Fatalf("legs %+v %+v", p.Legs[0], p.Legs[1])
	}
	if len(m.placed) != 4 {
		t.Fatalf("placed %d orders, want 4 (2 chunks x 2 legs)", len(m.placed))
	}
	tags := map[string]bool{}
	for _, r := range m.placed {
		if r.UserTag != "OVN-"+p.ID || !strings.HasPrefix(r.Tag, p.ID+"-ENT-") || tags[r.Tag] || r.Side != "BUY" {
			t.Fatalf("bad tags / side: %+v", r)
		}
		tags[r.Tag] = true
	}
	if s.HeldQty()[p.Legs[0].Token] != 3965 {
		t.Fatalf("HeldQty = %v", s.HeldQty())
	}

	// A broker rejection on one leg stops the job and cancels the other.
	m.mu.Lock()
	m.reject = "PE"
	m.mu.Unlock()
	if _, err := s.Submit(JobReq{Kind: "exit"}); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, s)
	if j.Status != "FAILED" || !strings.Contains(j.Note, "rejected") {
		t.Fatalf("exit with a rejection: %s %s", j.Status, j.Note)
	}
	if pe := p.held("PE"); pe == nil || pe.Qty != 3965 {
		t.Fatalf("PE changed on a rejected order: %+v", pe)
	}
}
