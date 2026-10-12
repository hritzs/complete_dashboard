package main

// Live GreekSoft quotes for other local services (the Overnight tab).
//
// The bridge owns the ONE Apollo market-data websocket of the account, so
// anything else that needs GreekSoft live prices asks the bridge instead of
// opening a second socket (a second login kicks this one out). The cache
// keeps the latest marketPicture tick of every subscribed gtoken and the
// latest index tick; two small HTTP endpoints on the health port serve it:
//
//	POST /api/subscribe  {"tokens":["102044618", ...]}   subscribe more gtokens
//	GET  /api/quotes?tokens=102044618,102044619          latest ticks (all when empty)
//
// Read-only towards the broker apart from the subscribe frame itself.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	greeksoft "trading-platform/libs/broker-greeksoft"
)

// apolloWriteMu serialises every write on the Apollo websocket: the socket
// allows one writer at a time and subscribe frames now come from three
// places (reconnect, the feed recorder, /api/subscribe).
var apolloWriteMu sync.Mutex

type liveQuote struct {
	Token string  `json:"token"`
	LTP   float64 `json:"ltp"`
	Bid   float64 `json:"bid"`
	Ask   float64 `json:"ask"`
	LTT   string  `json:"ltt,omitempty"`
	AtMs  int64   `json:"at_ms"` // when the bridge received the tick
}

type quoteCache struct {
	mu     sync.RWMutex
	quotes map[string]liveQuote
	index  map[string]liveQuote
	want   map[string]struct{}
	client *greeksoft.ApolloMarketDataClient
}

func newQuoteCache() *quoteCache {
	return &quoteCache{quotes: map[string]liveQuote{}, index: map[string]liveQuote{}, want: map[string]struct{}{}}
}

func quoteNum(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// observe stores a tick. Only tokens asked for through /api/subscribe are
// decoded fully; everything else costs one map lookup.
func (q *quoteCache) observe(frame greeksoft.ApolloFrame) {
	if q == nil {
		return
	}
	switch frame.StreamingType {
	case greeksoft.StreamingTypeMarketPicture:
		q.mu.RLock()
		n := len(q.want)
		q.mu.RUnlock()
		if n == 0 {
			return
		}
		var t struct {
			Symbol string `json:"symbol"`
			LTP    string `json:"ltp"`
			Bid    string `json:"bid"`
			Ask    string `json:"ask"`
			LTT    string `json:"ltt"`
		}
		if err := decodeApolloData(frame.Raw, &t); err != nil || t.Symbol == "" {
			return
		}
		q.mu.Lock()
		if _, ok := q.want[t.Symbol]; ok {
			q.quotes[t.Symbol] = liveQuote{Token: t.Symbol, LTP: quoteNum(t.LTP), Bid: quoteNum(t.Bid), Ask: quoteNum(t.Ask), LTT: t.LTT, AtMs: time.Now().UnixMilli()}
		}
		q.mu.Unlock()
	case greeksoft.StreamingTypeIndex:
		var t struct {
			Name string `json:"name"`
			LTP  string `json:"ltp"`
		}
		if err := decodeApolloData(frame.Raw, &t); err != nil || t.Name == "" {
			return
		}
		q.mu.Lock()
		q.index[t.Name] = liveQuote{Token: t.Name, LTP: quoteNum(t.LTP), AtMs: time.Now().UnixMilli()}
		q.mu.Unlock()
	}
}

// attach is called on every (re)connect: remembers the live client and
// returns the tokens to subscribe again.
func (q *quoteCache) attach(c *greeksoft.ApolloMarketDataClient) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.client = c
	out := make([]string, 0, len(q.want))
	for t := range q.want {
		out = append(out, t)
	}
	return out
}

// subscribe adds tokens and subscribes the new ones on the live socket.
func (q *quoteCache) subscribe(tokens []string) (added int, err error) {
	q.mu.Lock()
	var fresh []string
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := q.want[t]; !ok {
			q.want[t] = struct{}{}
			fresh = append(fresh, t)
		}
	}
	c := q.client
	q.mu.Unlock()
	if len(fresh) == 0 || c == nil {
		return len(fresh), nil // not connected yet: sent on connect
	}
	for i := 0; i < len(fresh); i += 50 {
		j := i + 50
		if j > len(fresh) {
			j = len(fresh)
		}
		apolloWriteMu.Lock()
		e := c.Subscribe(fresh[i:j])
		apolloWriteMu.Unlock()
		if e != nil {
			return len(fresh), e
		}
	}
	return len(fresh), nil
}

func (q *quoteCache) register(mux *http.ServeMux) {
	mux.HandleFunc("/api/subscribe", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Tokens []string `json:"tokens"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, `{"error":"POST {\"tokens\":[...]}"}`, http.StatusBadRequest)
			return
		}
		added, err := q.subscribe(b.Tokens)
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{"added": added}
		if err != nil {
			resp["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/quotes", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]liveQuote{}
		q.mu.RLock()
		if raw := strings.TrimSpace(r.URL.Query().Get("tokens")); raw != "" {
			for _, t := range strings.Split(raw, ",") {
				if v, ok := q.quotes[strings.TrimSpace(t)]; ok {
					out[v.Token] = v
				}
			}
		} else {
			for k, v := range q.quotes {
				out[k] = v
			}
		}
		idx := make(map[string]liveQuote, len(q.index))
		for k, v := range q.index {
			idx[k] = v
		}
		connected := q.client != nil
		q.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"quotes": out, "index": idx, "connected": connected, "now_ms": time.Now().UnixMilli()})
	})
}
