package greeksoft

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTokenCSVMapsBSERows(t *testing.T) {
	gtokenMap, shortTokenByGToken = map[int64]int64{}, map[int64]int64{}
	f := filepath.Join(t.TempDir(), "tokens.csv")
	body := `"xx.Token","Exchange","Type","Symbol","Expiry","Strike","OpType","Token","Blank"
1134374,"BSE","OPTION","SENSEX","05-NOV-2026",75400,"CE","IO",NA
102039913,"NSE","OPTIDX","NIFTY","09-NOV-26",23050,"PE",39913,NA
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loadGreeksoftTokenCSV(f)
	if got := gtokenMap[1134374]; got != 203134374 {
		t.Errorf("BSE token mapped to %d, want 203134374", got)
	}
	if got := shortTokenByGToken[203134374]; got != 1134374 {
		t.Errorf("BSE reverse map %d, want 1134374", got)
	}
	if got := gtokenMap[39913]; got != 102039913 {
		t.Errorf("NSE token mapped to %d, want 102039913", got)
	}
}
