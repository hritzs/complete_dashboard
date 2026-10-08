package main

// Apollo per-packet recorder, for comparing GreekSoft's feed against the
// NSE multicast feed (see feed-decoder) update by update. Every
// marketPicture frame is written as one CSV line:
//
//	APOLLO,rx_unix_ns,exchange_token,bcast_unix_s,ltt_unix_s,lut_unix_s,ltp,ltq,tot_vol,bid,ask
//
// to $FEEDREC_DIR (default ~/.trading-platform/feedrec)/YYYY-MM-DD_apollo.csv.
// Apollo's own times are whole seconds; tot_vol (cumulative traded
// volume) changes on every trade, so (token, tot_vol) identifies the same
// trade in both feeds. Writing is off the read path: a full buffer drops
// lines rather than ever slowing the Apollo read loop.
//
// Tokens: the ATM +/- FEEDREC_STRIKES strikes (CE+PE) of FEEDREC_SYMBOL's
// nearest expiry, read once from the snapshot service when it has live
// prices -- subscribed on the bridge's existing Apollo connection (a
// second Apollo connection kicks this one and forces a fresh login).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	greeksoft "trading-platform/libs/broker-greeksoft"
)

var istLoc = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Kolkata"); err == nil {
		return l
	}
	return time.FixedZone("IST", 19800)
}()

type apolloRecorder struct {
	lines chan string

	mu      sync.Mutex
	tokens  []string // gtokens, set once resolved
	current *greeksoft.ApolloMarketDataClient
}

func feedrecDir() string {
	if d := strings.TrimSpace(os.Getenv("FEEDREC_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".trading-platform", "feedrec")
}

// newApolloRecorder starts the writer; nil when FEEDREC=0.
func newApolloRecorder() *apolloRecorder {
	if strings.TrimSpace(os.Getenv("FEEDREC")) == "0" {
		return nil
	}
	r := &apolloRecorder{lines: make(chan string, 8192)}
	go r.write()
	return r
}

func (r *apolloRecorder) write() {
	var (
		day string
		f   *os.File
		w   *bufio.Writer
	)
	flush := time.NewTicker(time.Second)
	defer flush.Stop()
	for {
		select {
		case line := <-r.lines:
			if d := time.Now().In(istLoc).Format("2006-01-02"); d != day || f == nil {
				if f != nil {
					w.Flush()
					f.Close()
				}
				day = d
				_ = os.MkdirAll(feedrecDir(), 0o755)
				var err error
				if f, err = os.OpenFile(filepath.Join(feedrecDir(), day+"_apollo.csv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
					log.Printf("[FEEDREC] cannot open apollo record file: %v", err)
					f = nil
					continue
				}
				w = bufio.NewWriterSize(f, 1<<16)
			}
			w.WriteString(line)
		case <-flush.C:
			if w != nil {
				w.Flush()
			}
		}
	}
}

// record queues one marketPicture frame (never blocks).
func (r *apolloRecorder) record(rx time.Time, frame greeksoft.ApolloFrame) {
	if r == nil || frame.StreamingType != greeksoft.StreamingTypeMarketPicture {
		return
	}
	var d struct {
		Token  string `json:"exchange_token"`
		LTT    string `json:"ltt"`
		LUT    string `json:"lut"`
		LTP    string `json:"ltp"`
		LTQ    string `json:"ltq"`
		TotVol string `json:"tot_vol"`
		Bid    string `json:"bid"`
		Ask    string `json:"ask"`
	}
	if err := decodeApolloData(frame.Raw, &d); err != nil || d.Token == "" {
		return
	}
	line := fmt.Sprintf("APOLLO,%d,%s,%d,%d,%d,%s,%s,%s,%s,%s\n", rx.UnixNano(), d.Token, frame.BCastTime,
		apolloClock(d.LTT), apolloClock(d.LUT), d.LTP, d.LTQ, d.TotVol, d.Bid, d.Ask)
	select {
	case r.lines <- line:
	default:
	}
}

// apolloClock parses "DD-MM-YYYY HH:MM:SS" (IST) to unix seconds, 0 if not.
func apolloClock(s string) int64 {
	t, err := time.ParseInLocation("02-01-2006 15:04:05", strings.TrimSpace(s), istLoc)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// attach remembers the live client (called from resubscribe) and returns
// the recorder's tokens to subscribe on it.
func (r *apolloRecorder) attach(c *greeksoft.ApolloMarketDataClient) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = c
	return append([]string(nil), r.tokens...)
}

// resolveTokens polls the snapshot chain until it has live prices, then
// subscribes the ATM +/- N strikes on the current Apollo connection.
func (r *apolloRecorder) resolveTokens(snapshotURL, symbol string, strikes int, stop <-chan struct{}) {
	if r == nil {
		return
	}
	for {
		toks, atm, err := chainTokens(snapshotURL, symbol, strikes)
		if err == nil && len(toks) > 0 {
			r.mu.Lock()
			r.tokens = toks
			c := r.current
			r.mu.Unlock()
			if c != nil {
				if err := c.Subscribe(toks); err != nil {
					log.Printf("[FEEDREC] subscribe failed (will retry on reconnect): %v", err)
				}
			}
			log.Printf("[FEEDREC] recording Apollo %s ATM %.0f +/-%d strikes (%d tokens) to %s", symbol, atm, strikes, len(toks), feedrecDir())
			return
		}
		select {
		case <-stop:
			return
		case <-time.After(15 * time.Second):
		}
	}
}

func chainTokens(snapshotURL, symbol string, strikes int) ([]string, float64, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(snapshotURL, "/") + "/api/option-chain/" + symbol)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			ATM   float64 `json:"atm"`
			Chain []struct {
				Strike  float64 `json:"strike"`
				CEToken int64   `json:"ce_token"`
				PEToken int64   `json:"pe_token"`
				CELtp   float64 `json:"ce_ltp"`
				PELtp   float64 `json:"pe_ltp"`
			} `json:"chain"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}
	atm := body.Data.ATM
	idx := -1
	for i, row := range body.Data.Chain {
		if row.Strike == atm && (row.CELtp > 0 || row.PELtp > 0) {
			idx = i
		}
	}
	if idx < 0 {
		return nil, atm, fmt.Errorf("no live ATM row yet")
	}
	var out []string
	for i := idx - strikes; i <= idx+strikes; i++ {
		if i < 0 || i >= len(body.Data.Chain) {
			continue
		}
		for _, t := range []int64{body.Data.Chain[i].CEToken, body.Data.Chain[i].PEToken} {
			if t > 0 {
				out = append(out, strconv.FormatInt(nseFOGToken(t), 10)) // GreekSoft NSE F&O token
			}
		}
	}
	return out, atm, nil
}

// nseFOGToken maps an NSE F&O exchange token to GreekSoft's gtoken
// (IndexTokens.csv: 102044620 <-> 44620).
func nseFOGToken(t int64) int64 { return 102000000 + t }
