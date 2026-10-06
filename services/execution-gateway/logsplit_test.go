package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitLogWriter_Routes(t *testing.T) {
	dir := t.TempDir()
	var base bytes.Buffer
	w := &splitLogWriter{base: &base, files: map[string]*os.File{}, dir: dir}
	lines := []string{
		"2026/10/06 10:00:00 [SBUILD-SHADOW] rule=R1 HEDGE 1 lot(s) BUY CE\n", // simulated: never trading.log
		"2026/10/06 10:00:01 [SBUILD-LIVE] trade=SB-R1 BUILD SELL CE qty=65\n",
		"2026/10/06 10:00:02 [GREEKSOFT ORDER] sending token=1 side=SELL\n",
		"2026/10/06 10:00:03 [LUT] 10:00 table=09:20+ ... -> NO\n",
		"2026/10/06 10:00:04 [PAPER] started paper trade PAPER-1000\n",
		"2026/10/06 10:00:05 Control API listening on :8005\n", // no area: main log only
	}
	for _, l := range lines {
		if _, err := w.Write([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name+".log"))
		return string(b)
	}
	if strings.Count(base.String(), "\n") != len(lines) {
		t.Fatal("every line must still reach the main log")
	}
	tr := read("trading")
	if !strings.Contains(tr, "[SBUILD-LIVE]") || !strings.Contains(tr, "[GREEKSOFT ORDER]") || strings.Contains(tr, "SBUILD-SHADOW") || strings.Contains(tr, "[LUT]") {
		t.Fatalf("trading.log:\n%s", tr)
	}
	if !strings.Contains(read("sbuild_shadow"), "HEDGE 1 lot") || !strings.Contains(read("lut"), "[LUT]") || !strings.Contains(read("paper"), "[PAPER]") {
		t.Fatal("simulated areas not routed")
	}
	for _, name := range []string{"trading", "lut", "sbuild_shadow", "paper"} {
		if strings.Contains(read(name), "Control API") {
			t.Fatalf("unrouted line leaked into %s", name)
		}
	}
}
