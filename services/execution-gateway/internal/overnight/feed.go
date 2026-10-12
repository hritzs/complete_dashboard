package overnight

// Live GreekSoft ticks, read from the feed bridge -- the process that owns
// the account's one Apollo websocket (services/greeksoft-feed-bridge,
// quotes.go). Used for the live screen and for pricing orders; minute-end
// numbers never come from here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Quote struct {
	LTP  float64 `json:"ltp"`
	Bid  float64 `json:"bid"`
	Ask  float64 `json:"ask"`
	AtMs int64   `json:"at_ms"`
}

type feedClient struct {
	base string
	http *http.Client

	mu   sync.Mutex
	sent map[int64]bool // gtokens already asked for in this run
}

func newFeedClient() *feedClient {
	base := strings.TrimSpace(os.Getenv("OVERNIGHT_QUOTE_URL"))
	if base == "" {
		port := strings.TrimSpace(os.Getenv("BRIDGE_HEALTH_PORT"))
		if port == "" {
			port = "8022"
		}
		base = "http://127.0.0.1:" + port
	}
	return &feedClient{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 1500 * time.Millisecond}, sent: map[int64]bool{}}
}

// subscribe asks the bridge for ticks of these gtokens. force re-sends all
// of them (the bridge forgets its list when it restarts).
func (f *feedClient) subscribe(ctx context.Context, gtokens []int64, force bool) error {
	f.mu.Lock()
	var toks []string
	for _, g := range gtokens {
		if g > 0 && (force || !f.sent[g]) {
			toks = append(toks, strconv.FormatInt(g, 10))
		}
	}
	f.mu.Unlock()
	if len(toks) == 0 {
		return nil
	}
	body, _ := json.Marshal(map[string]interface{}{"tokens": toks})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.base+"/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("GreekSoft feed bridge not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GreekSoft feed bridge subscribe: HTTP %d (bridge not restarted since the quote endpoint was added?)", resp.StatusCode)
	}
	f.mu.Lock()
	for _, g := range gtokens {
		f.sent[g] = true
	}
	f.mu.Unlock()
	return nil
}

// quotes returns the latest tick per gtoken plus the NIFTY spot (0 when the
// bridge has not seen an index tick).
func (f *feedClient) quotes(ctx context.Context, gtokens []int64) (map[int64]Quote, float64, error) {
	toks := make([]string, 0, len(gtokens))
	for _, g := range gtokens {
		if g > 0 {
			toks = append(toks, strconv.FormatInt(g, 10))
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.base+"/api/quotes?tokens="+strings.Join(toks, ","), nil)
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GreekSoft feed bridge not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("GreekSoft feed bridge quotes: HTTP %d", resp.StatusCode)
	}
	var b struct {
		Quotes map[string]Quote `json:"quotes"`
		Index  map[string]Quote `json:"index"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return nil, 0, err
	}
	out := make(map[int64]Quote, len(b.Quotes))
	for k, v := range b.Quotes {
		if g, err := strconv.ParseInt(k, 10, 64); err == nil {
			out[g] = v
		}
	}
	spot := 0.0
	for name, v := range b.Index {
		n := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(name, " ", ""), "_", ""))
		if n == "NIFTY50" || n == "NIFTY" {
			spot = v.LTP
			break
		}
	}
	return out, spot, nil
}
