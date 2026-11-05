package greeksoft

// BSE F&O tokens (SENSEX, BANKEX): GreekSoft's own token for a BSE contract is
// NOT derived from the exchange token. The formula used before (202000000 +
// exchange token) matched 0 of 38,499 BSEFO rows on 2026-11-03 -- e.g.
// SENSEX 05NOV26 CE 73300 is exchange token 1134393, GreekToken 202013478,
// while the formula sent 203134393 ("Contract not found Invalid Data").
// The only source is GreekSoft's contract master (getAllContract, a CSV):
// it is read once a day through the executor's existing session, its BSEFO
// rows are cached on disk, and every BSE token is looked up there.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gs "trading-platform/libs/broker-greeksoft"
)

var (
	bseMu     sync.RWMutex
	bseGToken map[int64]int64 // BSE exchange token -> GreekSoft token (BSEFO rows of getAllContract)
	bseExchOf map[int64]int64 // GreekSoft token -> BSE exchange token (order updates / fills back to ours)
	bseFrom   string          // where the map came from, for the logs
)

// bseTokenFor returns GreekSoft's token for a BSE exchange token, if the
// contract master has been loaded and lists it.
func bseTokenFor(exchangeToken int64) (int64, bool, bool) {
	bseMu.RLock()
	defer bseMu.RUnlock()
	if bseGToken == nil {
		return 0, false, false
	}
	g, ok := bseGToken[exchangeToken]
	return g, ok, true
}

// bseExchangeTokenFor is the reverse: our (exchange) token for a GreekSoft token.
func bseExchangeTokenFor(greekToken int64) (int64, bool) {
	bseMu.RLock()
	defer bseMu.RUnlock()
	e, ok := bseExchOf[greekToken]
	return e, ok
}

func setBSETokens(m map[int64]int64, from string) {
	var rev map[int64]int64
	if m != nil {
		rev = make(map[int64]int64, len(m))
		for e, g := range m {
			rev[g] = e
		}
	}
	bseMu.Lock()
	bseGToken, bseExchOf, bseFrom = m, rev, from
	bseMu.Unlock()
}

// parseBSETokens reads GreekSoft's contract master CSV (columns found by
// header name) and keeps the BSEFO rows: exchange token -> GreekSoft token.
func parseBSETokens(r io.Reader) (map[int64]int64, error) {
	cr := csv.NewReader(bufio.NewReader(r))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("contract master header: %w", err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	gi, ok1 := col["greektoken"]
	ei, ok2 := col["exchangetoken"]
	si, ok3 := col["exchangesegment"]
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("contract master header lacks GreekToken / ExchangeToken / ExchangeSegment: %v", head)
	}
	m := map[int64]int64{}
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(row) <= gi || len(row) <= ei || len(row) <= si || !strings.EqualFold(strings.TrimSpace(row[si]), "BSEFO") {
			continue
		}
		g, err1 := parseInt64Cell(row[gi])
		e, err2 := parseInt64Cell(row[ei])
		if err1 != nil || err2 != nil || g <= 0 || e <= 0 {
			continue
		}
		m[e] = g
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("contract master has no BSEFO rows")
	}
	return m, nil
}

// bseCachePath is today's cache of the BSEFO rows (IST date).
func bseCachePath(dir string, now time.Time) string {
	return filepath.Join(dir, "greeksoft_bsefo_"+now.Format("20060102")+".csv")
}

// BSECacheDir: GREEKSOFT_CONTRACT_CACHE_DIR, else ~/.cache/trading-platform/greeksoft.
func BSECacheDir() string {
	if d := strings.TrimSpace(os.Getenv("GREEKSOFT_CONTRACT_CACHE_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "trading-platform-greeksoft")
	}
	return filepath.Join(home, ".cache", "trading-platform", "greeksoft")
}

// EnsureBSETokens loads the BSE token map: today's cache if there is one,
// else GreekSoft's contract master through the client's existing session
// (no login), keeping its BSEFO rows in today's cache.
func EnsureBSETokens(ctx context.Context, client *gs.Client, dir string, now time.Time) error {
	path := bseCachePath(dir, now)
	if f, err := os.Open(path); err == nil {
		m, perr := parseBSETokens(f)
		f.Close()
		if perr == nil {
			setBSETokens(m, path)
			log.Printf("[GREEKSOFT TOKEN] BSE contract master: %d BSEFO tokens from today's cache %s", len(m), path)
			return nil
		}
		log.Printf("[GREEKSOFT TOKEN] BSE cache %s unreadable (%v) -- fetching again", path, perr)
	}
	if client == nil {
		return fmt.Errorf("no greeksoft client to fetch the contract master")
	}
	t0 := time.Now()
	raw, err := client.GetAllContractCSV(ctx)
	if err != nil {
		return fmt.Errorf("fetch contract master: %w", err)
	}
	m, err := parseBSETokens(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	setBSETokens(m, "getAllContract")
	log.Printf("[GREEKSOFT TOKEN] BSE contract master: %d BSEFO tokens from getAllContract (%.1f MB in %s)", len(m), float64(len(raw))/1e6, time.Since(t0).Round(time.Millisecond))
	if err := writeBSECache(raw, dir, path); err != nil {
		log.Printf("[GREEKSOFT TOKEN] BSE cache not written (%v); it is fetched again on the next start", err)
	}
	return nil
}

// writeBSECache keeps the header and the BSEFO rows only.
func writeBSECache(raw []byte, dir, path string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first || strings.Contains(line, ",BSEFO,") {
			out.WriteString(line)
			out.WriteByte('\n')
		}
		first = false
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
