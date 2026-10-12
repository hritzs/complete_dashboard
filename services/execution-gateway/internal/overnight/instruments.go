package overnight

// The monthly chain, read from GreekSoft's own contract file
// (IndexTokens.csv: gtoken, exchange, type, symbol, expiry, strike, option
// type, exchange token). A MONTHLY expiry is one that also lists a future.

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Instrument struct {
	GToken int64   `json:"gtoken"` // GreekSoft token (websocket / candles)
	Token  int64   `json:"token"`  // exchange token (orders / positions)
	Strike float64 `json:"strike"`
	Opt    string  `json:"opt"` // CE | PE | FUT
}

type Chain struct {
	Symbol   string
	Expiry   string // as in the file, e.g. 27-OCT-26
	ExpiryAt time.Time
	Fut      Instrument
	Strikes  []float64 // ascending
	CE, PE   map[float64]Instrument
	Step     float64
}

func parseExpiry(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	p := strings.Split(s, "-")
	if len(p) == 3 && len(p[1]) == 3 {
		p[1] = strings.ToUpper(p[1][:1]) + strings.ToLower(p[1][1:])
		s = strings.Join(p, "-")
	}
	t, err := time.ParseInLocation("02-Jan-06", s, ist())
	if err != nil {
		return time.Time{}, err
	}
	// Contracts stop trading at 15:30 on the expiry day.
	return time.Date(t.Year(), t.Month(), t.Day(), 15, 30, 0, 0, ist()), nil
}

func tokenFilePaths() []string {
	return []string{
		os.Getenv("INDEX_TOKENS_PATH"), os.Getenv("INDEX_TOKENS_CSV"),
		"/mnt/shared/IndexTokens.csv", "../../IndexTokens.csv", "IndexTokens.csv",
	}
}

// LoadNearestMonthly reads the nearest monthly expiry of symbol that has
// not expired yet.
func LoadNearestMonthly(symbol string, now time.Time) (*Chain, error) {
	var rows [][]string
	var lastErr error
	for _, p := range tokenFilePaths() {
		if strings.TrimSpace(p) == "" {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			lastErr = err
			continue
		}
		r := csv.NewReader(f)
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		rows, err = r.ReadAll()
		f.Close()
		if err == nil && len(rows) > 1 {
			break
		}
		lastErr = err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("GreekSoft contract file not found: %v", lastErr)
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	// Nearest live expiry that lists a future.
	var best time.Time
	bestRaw := ""
	for _, r := range rows[1:] {
		if len(r) < 8 || strings.TrimSpace(r[2]) != "FUTIDX" || strings.ToUpper(strings.TrimSpace(r[3])) != symbol {
			continue
		}
		at, err := parseExpiry(r[4])
		if err != nil || !at.After(now) {
			continue
		}
		if best.IsZero() || at.Before(best) {
			best, bestRaw = at, strings.TrimSpace(r[4])
		}
	}
	if best.IsZero() {
		return nil, fmt.Errorf("no live monthly %s expiry in the GreekSoft contract file", symbol)
	}
	c := &Chain{Symbol: symbol, Expiry: bestRaw, ExpiryAt: best, CE: map[float64]Instrument{}, PE: map[float64]Instrument{}}
	for _, r := range rows[1:] {
		if len(r) < 8 || strings.ToUpper(strings.TrimSpace(r[3])) != symbol || strings.TrimSpace(r[4]) != bestRaw {
			continue
		}
		g, _ := strconv.ParseInt(strings.TrimSpace(r[0]), 10, 64)
		t, _ := strconv.ParseInt(strings.TrimSpace(r[7]), 10, 64)
		if g <= 0 || t <= 0 {
			continue
		}
		switch strings.TrimSpace(r[2]) {
		case "FUTIDX":
			c.Fut = Instrument{GToken: g, Token: t, Opt: "FUT"}
		case "OPTIDX":
			k, _ := strconv.ParseFloat(strings.TrimSpace(r[5]), 64)
			opt := strings.ToUpper(strings.TrimSpace(r[6]))
			if k <= 0 {
				continue
			}
			in := Instrument{GToken: g, Token: t, Strike: k, Opt: opt}
			if opt == "CE" {
				c.CE[k] = in
			} else if opt == "PE" {
				c.PE[k] = in
			}
		}
	}
	for k := range c.CE {
		if _, ok := c.PE[k]; ok {
			c.Strikes = append(c.Strikes, k)
		}
	}
	sort.Float64s(c.Strikes)
	if len(c.Strikes) < 3 || c.Fut.Token == 0 {
		return nil, fmt.Errorf("%s %s: incomplete chain in the GreekSoft contract file (%d strikes)", symbol, bestRaw, len(c.Strikes))
	}
	// Strike step: the smallest gap (the listing is uniform near the money).
	c.Step = c.Strikes[1] - c.Strikes[0]
	for i := 2; i < len(c.Strikes); i++ {
		if d := c.Strikes[i] - c.Strikes[i-1]; d > 0 && d < c.Step {
			c.Step = d
		}
	}
	return c, nil
}

// idx is the position of the listed strike nearest to k.
func (c *Chain) idx(k float64) int {
	i := sort.SearchFloat64s(c.Strikes, k)
	if i >= len(c.Strikes) {
		return len(c.Strikes) - 1
	}
	if i > 0 && k-c.Strikes[i-1] <= c.Strikes[i]-k {
		return i - 1
	}
	return i
}

func (c *Chain) leg(k float64, opt string) (Instrument, bool) {
	if opt == "CE" {
		in, ok := c.CE[k]
		return in, ok
	}
	in, ok := c.PE[k]
	return in, ok
}

func (c *Chain) byToken(tok int64) (Instrument, bool) {
	if c.Fut.Token == tok {
		return c.Fut, true
	}
	for _, m := range []map[float64]Instrument{c.CE, c.PE} {
		for _, in := range m {
			if in.Token == tok {
				return in, true
			}
		}
	}
	return Instrument{}, false
}
