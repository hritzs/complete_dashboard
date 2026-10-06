package main

// Per-area copies of the gateway log. Every line still goes to stderr
// (logs/4_execution.log stays complete); each line is ALSO appended to the
// file of its area in $LOG_DIR/exec/:
//   trading.log        real orders, fills, builds, hedges, exits, monitors,
//                      live straddle builds (everything that touches money)
//   lut.log            LUT entry check (paper)
//   sbuild_shadow.log  straddle-target builds in SHADOW mode (simulated)
//   paper.log          Paper Sim (simulated)
// The subfolder keeps the split files out of scripts/watch_logs.sh's merged
// view (no duplicate lines); `watch_logs.sh --only trading` etc. tails one.

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type logArea struct {
	name string
	any  []string // a line matching any of these goes to this area
}

// First match wins: simulated areas are checked before "trading" so a
// shadow build's HEDGE line never lands in the real-trading log.
var logAreas = []logArea{
	{"sbuild_shadow", []string{"[SBUILD-SHADOW]"}},
	{"lut", []string{"[LUT]", "[LUT-SIM]"}},
	{"paper", []string{"[PAPER"}},
	{"trading", []string{
		"[SBUILD-LIVE]", "[GREEKSOFT ORDER", "ORDER REFUSED", "[MONITOR]", "[RISK]", "[WINGS]", "[PARTIAL]",
		"[MODIFY]", "[BUILD", "BUILD submitted", "BUILD outcome", "BUILD verification", "BUILD submission",
		"HEDGE", "Square-off", "SQF", "SquareOff", "[MANUAL", "PersistVerifiedFills", "Persisting SQF",
		"DeployStraddle", "_TRIGGER", "MINUTE-CHECK", "[BOOT] resuming", "[BOOT] monitor", "[IRIS",
		"FREEZE QTY", "trade_uid=", "trade=",
	}},
}

type splitLogWriter struct {
	base  io.Writer
	mu    sync.Mutex
	files map[string]*os.File
	dir   string
}

func (w *splitLogWriter) Write(p []byte) (int, error) {
	n, err := w.base.Write(p)
	line := string(bytes.TrimRight(p, "\n"))
	for _, a := range logAreas {
		hit := false
		for _, k := range a.any {
			if strings.Contains(line, k) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		w.mu.Lock()
		f := w.files[a.name]
		if f == nil {
			var ferr error
			f, ferr = os.OpenFile(filepath.Join(w.dir, a.name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if ferr == nil {
				w.files[a.name] = f
			}
		}
		if f != nil {
			_, _ = f.Write(p)
		}
		w.mu.Unlock()
		break
	}
	return n, err
}

// installSplitLogs routes log output through the splitter when a log
// directory is known (LOG_DIR from start_platform.sh, else ./logs).
func installSplitLogs() {
	dir := strings.TrimSpace(os.Getenv("LOG_DIR"))
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			if st, err := os.Stat(filepath.Join(wd, "logs")); err == nil && st.IsDir() {
				dir = filepath.Join(wd, "logs")
			}
		}
	}
	if dir == "" {
		return
	}
	dir = filepath.Join(dir, "exec")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[LOGS] per-area logs disabled: %v", err)
		return
	}
	log.SetOutput(&splitLogWriter{base: os.Stderr, files: map[string]*os.File{}, dir: dir})
	log.Printf("[LOGS] per-area copies in %s (trading.log, lut.log, sbuild_shadow.log, paper.log)", dir)
}
