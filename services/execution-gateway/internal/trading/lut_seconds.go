package trading

// Second-by-second future, for the Paper Sim's intra-minute hedge check
// (the live monitor hedges the complete delta inside a minute once points
// out pass 1.2 x allowed; the simulation replays the same check on these
// seconds). One file per day in LUT_DATA_DIR: <day>_sec.csv, "HH:MM:SS,future"
// (synthetic future of the current expiry, first tick of each second),
// 09:15:00 - 15:40:00. Written in small batches; never on the order path.

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func lutSecFile(day string) string { return filepath.Join(lutDataDir(), day+"_sec.csv") }

// simSec is one second's future.
type simSec struct {
	T string  // HH:MM:SS
	F float64 // future
}

// noteSecond records the future once per second. Caller holds e.mu.
func (e *lutEngine) noteSecond(now time.Time, day string, fut float64) {
	hhmm := now.Hour()*100 + now.Minute()
	if fut <= 0 || hhmm < 915 || hhmm > lutRecordUntil {
		return
	}
	sec := now.Unix()
	if sec == e.secLast {
		return
	}
	e.secLast = sec
	e.secBuf = append(e.secBuf, fmt.Sprintf("%s,%.2f\n", now.Format("15:04:05"), fut)...)
	if len(e.secBuf) >= 5*16 || now.Second() == 0 { // ~5 s batches, and at each minute
		e.flushSecondsLocked(day)
	}
}

func (e *lutEngine) flushSecondsLocked(day string) {
	if len(e.secBuf) == 0 {
		return
	}
	b := e.secBuf
	e.secBuf = nil
	if err := os.MkdirAll(lutDataDir(), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(lutSecFile(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[LUT] ⚠ seconds file: %v", err)
		return
	}
	_, _ = f.Write(b)
	_ = f.Close()
}

// lutLoadSeconds returns a day's seconds grouped by the minute they fall in
// (HHMM); second :00 belongs to the minute it opens, so it is left out --
// that instant is the minute-end check itself.
func lutLoadSeconds(day string) map[int][]simSec {
	f, err := os.Open(lutSecFile(day))
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[int][]simSec{}
	sc := bufio.NewScanner(f)
	last := ""
	for sc.Scan() {
		t, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), ",")
		if !ok || len(t) != 8 || t <= last {
			continue // malformed / out of order / repeated after a restart
		}
		px, err := strconv.ParseFloat(v, 64)
		if err != nil || px <= 0 || strings.HasSuffix(t, ":00") {
			continue
		}
		last = t
		out[lutHHMM(t[:5])] = append(out[lutHHMM(t[:5])], simSec{T: t, F: px})
	}
	return out
}
