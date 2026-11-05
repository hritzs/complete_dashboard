package greeksoft

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real rows of GreekSoft's getAllContract CSV (2026-11-03).
const contractMasterSample = `GreekToken,ExchangeToken,ExchangeSegMent,Series/InstType,Symbol,Description,ExpiryDate,OptionType,StrikePrice,TickSize,LotSize,TradeSymbol,Name
202013547,1134228,BSEFO,OPTIDX,SENSEX,SENSEX 05NOV26 CE 76400,05NOV2026,CE,76400.00,0.05,20,SENSEX26N0576400CE,SENSEX05N
202013903,1134459,BSEFO,OPTIDX,SENSEX,SENSEX 05NOV26 PE 76400,05NOV2026,PE,76400.00,0.05,20,SENSEX26N0576400PE,SENSEX05N
202013478,1134393,BSEFO,OPTIDX,SENSEX,SENSEX 05NOV26 CE 73300,05NOV2026,CE,73300.00,0.05,20,SENSEX26N0573300CE,SENSEX05N
202016345,1101886,BSEFO,OPTIDX,BANKEX,BANKEX 26NOV26 CE 63000,26NOV2026,CE,63000.00,0.05,30,BANKEX26N2663000CE,BANKEX26N
102039951,39951,NSEFO,OPTIDX,NIFTY,NIFTY 09NOV26 CE 23800,09NOV2026,CE,23800.00,0.05,65,NIFTY26N0923800CE,NIFTY09N
`

func TestParseBSETokens(t *testing.T) {
	m, err := parseBSETokens(strings.NewReader(contractMasterSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 4 {
		t.Fatalf("want the 4 BSEFO rows only, got %d: %v", len(m), m)
	}
	// The case from 2026-11-03: the old formula sent 203134393 for this one.
	if m[1134393] != 202013478 || m[1101886] != 202016345 {
		t.Fatalf("lookup wrong: %v", m)
	}
	if _, nse := m[39951]; nse {
		t.Fatal("NSE rows must not enter the BSE map")
	}
	if _, err := parseBSETokens(strings.NewReader("a,b,c\n1,2,3\n")); err == nil {
		t.Fatal("a file without the GreekSoft header must be refused")
	}
}

func TestResolveBSEFromContractMaster(t *testing.T) {
	loadGreeksoftTokenMapOnce()
	defer setBSETokens(nil, "")
	// Not loaded yet: the exchange token goes through unchanged, with a warning.
	setBSETokens(nil, "")
	if g := resolveGreeksoftGToken("BSEFO", 1134393); g != 1134393 {
		t.Fatalf("before the master loads: %d", g)
	}
	m, _ := parseBSETokens(strings.NewReader(contractMasterSample))
	setBSETokens(m, "test")
	if g := resolveGreeksoftGToken("BSEFO", 1134393); g != 202013478 {
		t.Fatalf("SENSEX 1134393 -> %d, want 202013478", g)
	}
	// The freeze-qty / quote path passes no segment: known BSE tokens still resolve.
	bseExchangeToken[1101886] = true
	defer delete(bseExchangeToken, 1101886)
	if g := resolveGreeksoftGToken("", 1101886); g != 202016345 {
		t.Fatalf("BANKEX 1101886 (no segment) -> %d, want 202016345", g)
	}
	// Not in the master: unchanged (GreekSoft refuses it; logged), never a made-up token.
	if g := resolveGreeksoftGToken("BSEFO", 1999999); g != 1999999 {
		t.Fatalf("unknown BSE token -> %d", g)
	}
	// A token sent as NSE is never looked up in the BSE map.
	if g := resolveGreeksoftGToken("NSEFO", 1134393); g == 202013478 {
		t.Fatal("an NSE-segment token must not be looked up in the BSE map")
	}
	// Fills / order updates come back with GreekSoft's token: mapped back to ours.
	if e := resolveGreeksoftShortToken(202013478); e != 1134393 {
		t.Fatalf("reverse 202013478 -> %d, want 1134393", e)
	}
}

func TestBSECacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 5, 12, 30, 0, 0, time.Local)
	path := bseCachePath(dir, now)
	if err := writeBSECache([]byte(contractMasterSample), dir, path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "NSEFO") || !strings.HasPrefix(string(b), "GreekToken,") {
		t.Fatalf("cache must keep the header and BSEFO rows only:\n%s", b)
	}
	defer setBSETokens(nil, "")
	if err := EnsureBSETokens(context.Background(), nil, dir, now); err != nil { // from the cache, no client needed
		t.Fatal(err)
	}
	if g, ok, loaded := bseTokenFor(1134393); !loaded || !ok || g != 202013478 {
		t.Fatalf("from cache: %d %v %v", g, ok, loaded)
	}
	if filepath.Base(path) != "greeksoft_bsefo_20261105.csv" {
		t.Fatalf("cache name %s", path)
	}
	// Tomorrow there is no cache and no client: an error, not a stale map.
	if err := EnsureBSETokens(context.Background(), nil, dir, now.AddDate(0, 0, 1)); err == nil {
		t.Fatal("no cache for the day and no client must be an error")
	}
}
