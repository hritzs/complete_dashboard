package trading

// Second-by-second future, for the Paper Sim's intra-minute hedge check
// (the live monitor hedges the complete delta inside a minute once points
// out pass 1.2 x allowed; the simulation replays the same check on these
// seconds). One file per day in LUT_DATA_DIR: <day>_sec.csv, "HH:MM:SS,future"
// (synthetic future of the current expiry, first tick of each second),
// 09:15:00 - 15:40:00. One small append per second; never on the order path.

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func lutSecFile(day string) string { return filepath.Join(lutDataDir(), day+"_sec.csv") }

// simSec is one second's future.
type simSec struct {
	T string  // HH:MM:SS
	F float64 // future
}

// lutTicks: the future on every engine tick (100 ms) in memory, per
// minute, for the simulation driving a live trade (sim_drive.go). Second
// :00 is left out, as in the file: that instant is the minute-end check.
var lutTicks = struct {
	mu   sync.Mutex
	day  string
	m    map[int][]simSec
	done map[int]bool // minutes already compacted
}{}

// lutTicksFile keeps, per day, the ticks of every finished minute whose
// intra-minute hedge fired (up to the hedge's tick): "HHMM,HH:MM:SS.mmm,fut".
// Those are the only minutes whose 100 ms ticks decide differently from the
// seconds file; without it a restart lost them and the simulation re-ran
// the hedge on the seconds file, at a different second and future than the
// live trade had acted on (2026-10-16 10:19:12.489 -> 10:19:19).
func lutTicksFile(day string) string { return filepath.Join(lutDataDir(), day+"_ticks.csv") }

func lutLoadTicksFile(day string) map[int][]simSec {
	out := map[int][]simSec{}
	f, err := os.Open(lutTicksFile(day))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(strings.TrimSpace(sc.Text()), ",")
		if len(p) != 3 {
			continue
		}
		hm, err1 := strconv.Atoi(p[0])
		fut, err2 := strconv.ParseFloat(p[2], 64)
		if err1 != nil || err2 != nil || fut <= 0 {
			continue
		}
		out[hm] = append(out[hm], simSec{T: p[1], F: fut})
	}
	return out
}

func lutNoteTick(day string, now time.Time, fut float64) {
	if fut <= 0 || now.Second() == 0 {
		return
	}
	hm := now.Hour()*100 + now.Minute()
	lutTicks.mu.Lock()
	if lutTicks.day != day || lutTicks.m == nil {
		// New day or a restart: finished intra-hedge minutes come back from
		// the file, already compacted.
		lutTicks.day, lutTicks.m, lutTicks.done = day, lutLoadTicksFile(day), map[int]bool{}
		for k := range lutTicks.m {
			lutTicks.done[k] = true
		}
	}
	lutTicks.m[hm] = append(lutTicks.m[hm], simSec{T: now.Format("15:04:05.000"), F: fut})
	lutTicks.mu.Unlock()
}

// lutTicksFrom copies the day's ticks of minutes >= from. A day that isn't
// the one in memory (another day, or before the first tick after a restart)
// is read from its ticks file without touching the in-memory state.
func lutTicksFrom(day string, from int) map[int][]simSec {
	lutTicks.mu.Lock()
	defer lutTicks.mu.Unlock()
	src := lutTicks.m
	if lutTicks.day != day {
		src = lutLoadTicksFile(day)
	}
	out := map[int][]simSec{}
	for hm, v := range src {
		if hm >= from {
			out[hm] = append([]simSec(nil), v...)
		}
	}
	return out
}

// lutTicksCompact trims finished minutes (< before) so a re-run stays cheap
// and decides the same: a minute whose intra-minute hedge fired keeps every
// tick up to it (cut = that tick's time), any other one keeps one tick per
// second (a subset of ticks with no breach has no breach either).
func lutTicksCompact(day string, before int, cut map[int]string) {
	var persist []byte
	defer func() {
		if len(persist) == 0 {
			return
		}
		if err := os.MkdirAll(lutDataDir(), 0o755); err != nil {
			return
		}
		f, err := os.OpenFile(lutTicksFile(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			log.Printf("[LUT] ⚠ ticks file: %v", err)
			return
		}
		_, _ = f.Write(persist)
		_ = f.Close()
	}()
	lutTicks.mu.Lock()
	defer lutTicks.mu.Unlock()
	if lutTicks.day != day {
		return
	}
	for hm, v := range lutTicks.m {
		if hm >= before || len(v) == 0 || lutTicks.done[hm] {
			continue
		}
		lutTicks.done[hm] = true
		var keep []simSec
		if c, ok := cut[hm]; ok {
			for _, s := range v {
				if s.T <= c {
					keep = append(keep, s)
					persist = append(persist, fmt.Sprintf("%d,%s,%.2f\n", hm, s.T, s.F)...)
				}
			}
		} else {
			last := ""
			for _, s := range v {
				if sec := s.T[:8]; sec != last {
					last = sec
					keep = append(keep, s)
				}
			}
		}
		lutTicks.m[hm] = keep
	}
}

// noteSecond records the future once per second. Caller holds e.mu.
func (e *lutEngine) noteSecond(now time.Time, day string, fut float64) {
	hhmm := now.Hour()*100 + now.Minute()
	if fut <= 0 || hhmm < 915 || hhmm > lutRecordUntil {
		return
	}
	lutNoteTick(day, now, fut)
	sec := now.Unix()
	if sec == e.secLast {
		return
	}
	e.secLast = sec
	e.secBuf = append(e.secBuf, fmt.Sprintf("%s,%.2f\n", now.Format("15:04:05"), fut)...)
	e.flushSecondsLocked(day) // every second: the simulation-driven trade acts on it (sim_drive.go)
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
