package trading

// Special trading sessions: a date whose market is open outside the normal
// 09:15-15:40 day -- NSE's Diwali Muhurat session (2026: Sunday 8 Nov, NSE
// circular FAOP71777; timings notified closer to the date). Set on the
// Muhurat tab; nothing changes on any date that is not listed.
//
// What a listed date changes:
//   - time to expiry (greeks, LUT): calendar days to expiry as always, plus
//     only the session's REMAINING TRADING MINUTES as a share of a normal
//     day (lutRawDTE; the feed decoder reads the same file);
//   - the Muhurat tab's LUT check runs on minutes since the open (the
//     first minute after the open is the 09:16 table) and its build exits
//     muhuratExitBefore ahead of the close.
//
// The file is shared with the C++ feed decoder (chain greeks):
// $SPECIAL_SESSIONS_FILE or ~/.trading-platform/special_sessions.json.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SpecialSession is one listed date.
type SpecialSession struct {
	Date    string `json:"date"`               // YYYY-MM-DD (IST)
	Name    string `json:"name"`               // e.g. "Diwali Muhurat"
	PreOpen string `json:"pre_open,omitempty"` // HH:MM, display only
	Open    string `json:"open"`               // HH:MM continuous trading starts
	Close   string `json:"close"`              // HH:MM continuous trading ends
}

// muhuratExitBefore: the timed exit of a special-session build, ahead of
// the close (as 15:37 is ahead of the normal 15:40).
const muhuratExitBefore = 3 * time.Minute

func specialSessionsFile() string {
	if p := strings.TrimSpace(os.Getenv("SPECIAL_SESSIONS_FILE")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".trading-platform", "special_sessions.json")
}

var specialSessions struct {
	sync.Mutex
	path    string
	mod     time.Time
	checked time.Time // last stat of the file (at most every specialSessionsRecheck)
	byDay   map[string]SpecialSession
}

const specialSessionsRecheck = 2 * time.Second

// loadSpecialSessions returns the listed sessions by date, re-read when the
// file changes. A missing or unreadable file means none.
func loadSpecialSessions() map[string]SpecialSession {
	p := specialSessionsFile()
	specialSessions.Lock()
	defer specialSessions.Unlock()
	if specialSessions.path == p && time.Since(specialSessions.checked) < specialSessionsRecheck {
		return specialSessions.byDay
	}
	specialSessions.checked = time.Now()
	st, err := os.Stat(p)
	if err != nil {
		specialSessions.path, specialSessions.byDay = p, nil
		return nil
	}
	if specialSessions.path == p && st.ModTime().Equal(specialSessions.mod) && specialSessions.byDay != nil {
		return specialSessions.byDay
	}
	m := map[string]SpecialSession{}
	if b, rerr := os.ReadFile(p); rerr == nil {
		var list []SpecialSession
		if json.Unmarshal(b, &list) == nil {
			for _, s := range list {
				if s.validate() == nil {
					m[s.Date] = s
				}
			}
		}
	}
	specialSessions.path, specialSessions.mod, specialSessions.byDay = p, st.ModTime(), m
	return m
}

func (s SpecialSession) validate() error {
	if _, err := time.Parse("2006-01-02", s.Date); err != nil {
		return fmt.Errorf("date %q: want YYYY-MM-DD", s.Date)
	}
	o, err1 := time.Parse("15:04", s.Open)
	c, err2 := time.Parse("15:04", s.Close)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("open %q / close %q: want HH:MM", s.Open, s.Close)
	}
	if !c.After(o.Add(5 * time.Minute)) {
		return fmt.Errorf("close %s must be more than 5 minutes after open %s", s.Close, s.Open)
	}
	if s.PreOpen != "" {
		p, err := time.Parse("15:04", s.PreOpen)
		if err != nil || p.After(o) {
			return fmt.Errorf("pre-open %q: want HH:MM no later than the open", s.PreOpen)
		}
	}
	return nil
}

// at returns the session's open and close on its date, in t's location.
func (s SpecialSession) at(loc *time.Location) (open, close time.Time) {
	d, _ := time.ParseInLocation("2006-01-02", s.Date, loc)
	o, _ := time.Parse("15:04", s.Open)
	c, _ := time.Parse("15:04", s.Close)
	open = time.Date(d.Year(), d.Month(), d.Day(), o.Hour(), o.Minute(), 0, 0, loc)
	close = time.Date(d.Year(), d.Month(), d.Day(), c.Hour(), c.Minute(), 0, 0, loc)
	return open, close
}

// specialSessionOn: the special session listed for t's date (IST), if any.
func specialSessionOn(t time.Time) (s SpecialSession, open, close time.Time, ok bool) {
	ist := t.In(lutIST())
	s, ok = loadSpecialSessions()[ist.Format("2006-01-02")]
	if !ok {
		return s, open, close, false
	}
	open, close = s.at(lutIST())
	return s, open, close, true
}

// specialSessionDate: d's date (IST) is a listed special session.
func specialSessionDate(d time.Time) bool {
	_, ok := loadSpecialSessions()[d.In(lutIST()).Format("2006-01-02")]
	return ok
}

// saveSpecialSessions writes the list (sorted by date) atomically.
func saveSpecialSessions(list []SpecialSession) error {
	for _, s := range list {
		if err := s.validate(); err != nil {
			return err
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	p := specialSessionsFile()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := sbWriteAtomic(p, b); err != nil {
		return err
	}
	specialSessions.Lock()
	specialSessions.checked = time.Time{} // re-read at once
	specialSessions.Unlock()
	return nil
}

// specialSessionList: every listed session, sorted by date.
func specialSessionList() []SpecialSession {
	m := loadSpecialSessions()
	out := make([]SpecialSession, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// sessionExitCap: on a special-session date no timed exit is later than
// muhuratExitBefore ahead of the session close (every build, as 15:37 is
// ahead of 15:40 on a normal day). Any other date: t unchanged.
func sessionExitCap(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	_, _, close, ok := specialSessionOn(t)
	if !ok {
		return t
	}
	if c := close.Add(-muhuratExitBefore); t.After(c) {
		return c
	}
	return t
}

// sessionHHMM is the LUT clock of a special session: minutes since its open
// counted from 09:15 (the first minute after the open is 09:16, the 09:16
// table). ok=false outside the session or on a normal day.
func sessionHHMM(now time.Time) (hhmm int, ok bool) {
	_, open, close, on := specialSessionOn(now)
	if !on || now.Before(open) || now.After(close) {
		return 0, false
	}
	m := 9*60 + 15 + int(now.Sub(open)/time.Minute)
	return (m/60)*100 + m%60, true
}
