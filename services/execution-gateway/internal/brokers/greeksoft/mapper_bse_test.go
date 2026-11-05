package greeksoft

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTokenCSVMapsBSERows(t *testing.T) {
	gtokenMap, shortTokenByGToken, bseExchangeToken = map[int64]int64{}, map[int64]int64{}, map[int64]bool{}
	f := filepath.Join(t.TempDir(), "tokens.csv")
	body := `"xx.Token","Exchange","Type","Symbol","Expiry","Strike","OpType","Token","Blank"
1134374,"BSE","OPTION","SENSEX","05-NOV-2026",75400,"CE","IO",NA
102039913,"NSE","OPTIDX","NIFTY","09-NOV-26",23050,"PE",39913,NA
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loadGreeksoftTokenCSV(f)
	// BSE rows are only marked as BSE: GreekSoft's token comes from its
	// contract master (bse_tokens.go), never from the old 202000000 + formula.
	if got, ok := gtokenMap[1134374]; ok {
		t.Errorf("BSE token must not be mapped by formula, got %d", got)
	}
	if _, ok := shortTokenByGToken[203134374]; ok {
		t.Error("the formula's 203134374 must not be in the reverse map")
	}
	if !bseExchangeToken[1134374] {
		t.Error("BSE row not marked as BSE")
	}
	if got := gtokenMap[39913]; got != 102039913 {
		t.Errorf("NSE token mapped to %d, want 102039913", got)
	}
}
