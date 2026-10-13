package trading

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGSBarCloseAt(t *testing.T) {
	at := func(hms string) int64 {
		x, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-13 "+hms, lutIST())
		return x.Unix()
	}
	b := at("09:37:00")
	// Busy strike: the candle is stamped exactly at the boundary.
	if px, fresh := gsBarCloseAt(map[int64]float64{at("09:36:00"): 160.5, b: 161.4, at("09:38:00"): 162.2, at("09:38:40"): 163}, b); px != 161.4 || !fresh {
		t.Fatalf("exact: %v %v", px, fresh)
	}
	// Quiet strike: the 09:36-09:37 bar was only closed at 09:37:22.
	if px, fresh := gsBarCloseAt(map[int64]float64{at("09:36:04"): 160.5, at("09:37:22"): 161.4, at("09:39:10"): 163}, b); px != 161.4 || !fresh {
		t.Fatalf("late stamp: %v %v", px, fresh)
	}
	// Cut-off reply ending at the open: not fresh (this is what priced
	// 22550 PE at 289.10 for 09:37 on 2026-10-13).
	if _, fresh := gsBarCloseAt(map[int64]float64{at("09:15:00"): 290.2, at("09:16:00"): 289.1}, b); fresh {
		t.Fatal("a reply ending at 09:16 is not a 09:37 price")
	}
	if _, fresh := gsBarCloseAt(nil, b); fresh {
		t.Fatal("empty reply")
	}
}

// A backfilled minute whose future is off the recorded minutes around it is
// dropped from the import file (kept in .rejected); a sane one stays.
func TestGSBackfillPurge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LUT_DATA_DIR", dir)
	day := "2026-10-13"
	rec := `{"t":"09:36","f":22549.85,"atm":22550,"exp":"19-OCT-26","rows":[{"k":22550,"ce":160.5,"pe":160.65}]}` + "\n" +
		`{"t":"09:39","f":22575.45,"atm":22600,"exp":"19-OCT-26","rows":[{"k":22600,"ce":147.1,"pe":171.65}]}` + "\n"
	imp := `{"t":"09:37","f":22375.65,"atm":22400,"exp":"19-OCT-26","src":"GreekSoft backfill","rows":[{"k":22550,"ce":114.75,"pe":289.1}]}` + "\n" +
		`{"t":"09:38","f":22548.45,"atm":22550,"exp":"19-OCT-26","src":"GreekSoft backfill","rows":[{"k":22550,"ce":162.2,"pe":163.75}]}` + "\n"
	if err := os.WriteFile(lutChainFile(day, "next"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lutChainImportFile(day, "next"), []byte(imp), 0o644); err != nil {
		t.Fatal(err)
	}
	gsBackfillPurge(day, "next")
	left, _ := os.ReadFile(lutChainImportFile(day, "next"))
	if strings.Contains(string(left), `"09:37"`) || !strings.Contains(string(left), `"09:38"`) {
		t.Fatalf("import file after purge: %s", left)
	}
	rej, _ := os.ReadFile(filepath.Join(dir, day+"_chain_next_import.jsonl.rejected"))
	if !strings.Contains(string(rej), `"09:37"`) {
		t.Fatalf("rejected file: %q", rej)
	}
	if _, ok := lutLoadChainSet(day, "next")["09:37"]; ok {
		t.Fatal("09:37 still loaded")
	}
}
