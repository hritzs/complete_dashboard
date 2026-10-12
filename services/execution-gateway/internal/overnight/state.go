package overnight

// What the module remembers across restarts and days: the position, its
// day-by-day PnL marks, the jobs and every order it sent. One JSON file,
// rewritten atomically after every change.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	ModePaper = "PAPER" // nothing is sent to the broker; fills at the live bid / ask
	ModeLive  = "LIVE"
)

type Leg struct {
	Token    int64   `json:"token"`
	GToken   int64   `json:"gtoken"`
	Strike   float64 `json:"strike"`
	Opt      string  `json:"opt"`
	Qty      int64   `json:"qty"`      // held now (long)
	Avg      float64 `json:"avg"`      // average buy price of what is held
	Realized float64 `json:"realized"` // booked on what was sold
	Bought   int64   `json:"bought"`
	Sold     int64   `json:"sold"`
}

// DayMark is the position at the end of one trading day (rewritten every
// minute while that day runs). A day's PnL is its Cum minus the previous
// MARK's Cum -- the previous trading day the position existed on, so a
// holiday or weekend in between needs no special handling.
type DayMark struct {
	Day        string             `json:"day"` // 2006-01-02
	Minute     string             `json:"minute"`
	Cum        float64            `json:"cum"` // realized + unrealized since entry
	Realized   float64            `json:"realized"`
	Unrealized float64            `json:"unrealized"`
	Qty        int64              `json:"qty"` // CE + PE held
	Spot       float64            `json:"spot"`
	Fut        float64            `json:"fut"`
	Closes     map[string]float64 `json:"closes"` // "23500CE" -> close
}

type Position struct {
	ID       string     `json:"id"`
	Mode     string     `json:"mode"`
	Expiry   string     `json:"expiry"`
	Delta    float64    `json:"delta"`
	LotSize  int64      `json:"lot_size"`
	Legs     []*Leg     `json:"legs"`
	OpenedAt string     `json:"opened_at"`
	ClosedAt string     `json:"closed_at,omitempty"`
	Rolls    int        `json:"rolls"`
	Days     []*DayMark `json:"days"`
}

func (p *Position) userTag() string { return "OVN-" + p.ID }

func (p *Position) leg(token int64) *Leg {
	for _, l := range p.Legs {
		if l.Token == token {
			return l
		}
	}
	return nil
}

// held is the open leg of one option type (the strike a roll replaces).
func (p *Position) held(opt string) *Leg {
	for _, l := range p.Legs {
		if l.Opt == opt && l.Qty > 0 {
			return l
		}
	}
	return nil
}

func (p *Position) qty() (q int64) {
	for _, l := range p.Legs {
		q += l.Qty
	}
	return q
}

func (p *Position) realized() (r float64) {
	for _, l := range p.Legs {
		r += l.Realized
	}
	return r
}

// fill books one execution. BUY raises the average; SELL books
// (price - average) on the quantity sold.
func (p *Position) fill(in Instrument, side string, qty int64, price float64) {
	l := p.leg(in.Token)
	if l == nil {
		l = &Leg{Token: in.Token, GToken: in.GToken, Strike: in.Strike, Opt: in.Opt}
		p.Legs = append(p.Legs, l)
	}
	if side == "BUY" {
		l.Avg = (l.Avg*float64(l.Qty) + price*float64(qty)) / float64(l.Qty+qty)
		l.Qty += qty
		l.Bought += qty
		return
	}
	if qty > l.Qty {
		qty = l.Qty
	}
	l.Realized += (price - l.Avg) * float64(qty)
	l.Qty -= qty
	l.Sold += qty
}

// OrderRec is one order the module sent, kept for the order book section.
type OrderRec struct {
	Time     string  `json:"time"`
	JobID    string  `json:"job_id"`
	Tag      string  `json:"tag"`      // unique per order -> broker field "tag"
	UserTag  string  `json:"user_tag"` // one per position -> broker field "userTag"
	OrderID  string  `json:"order_id"`
	Token    int64   `json:"token"`
	Strike   float64 `json:"strike"`
	Opt      string  `json:"opt"`
	Side     string  `json:"side"`
	Qty      int64   `json:"qty"`
	Filled   int64   `json:"filled"`
	Price    float64 `json:"price"` // current limit
	AvgFill  float64 `json:"avg_fill"`
	Modifies int     `json:"modifies"`
	Status   string  `json:"status"`
	Reason   string  `json:"reason,omitempty"`
	Mode     string  `json:"mode"`

	value float64 // traded value booked so far
}

type PlanLeg struct {
	Token  int64   `json:"token"`
	Strike float64 `json:"strike"`
	Opt    string  `json:"opt"`
	Side   string  `json:"side"`
	Target int64   `json:"target"`
	Done   int64   `json:"done"`
}

type Job struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`   // ENTRY | EXIT | PARTIAL | ROLL
	Status    string     `json:"status"` // SCHEDULED | RUNNING | DONE | FAILED | STOPPED
	Mode      string     `json:"mode"`
	At        string     `json:"at,omitempty"` // HH:MM today; empty = now
	Delta     float64    `json:"delta,omitempty"`
	Qty       int64      `json:"qty,omitempty"` // per leg
	Minutes   int        `json:"minutes,omitempty"`
	Minute    string     `json:"minute,omitempty"` // the minute end its numbers came from
	Plan      []*PlanLeg `json:"plan,omitempty"`
	Chunk     int        `json:"chunk"`
	CreatedAt string     `json:"created_at"`
	StartedAt string     `json:"started_at,omitempty"`
	EndedAt   string     `json:"ended_at,omitempty"`
	Note      string     `json:"note,omitempty"`
	Log       []string   `json:"log,omitempty"`
}

type State struct {
	Mode     string      `json:"mode"`
	Delta    float64     `json:"delta"` // the delta the live scenarios follow
	Seq      int         `json:"seq"`
	Position *Position   `json:"position,omitempty"`
	History  []*Position `json:"history,omitempty"`
	Jobs     []*Job      `json:"jobs,omitempty"`
	Orders   []*OrderRec `json:"orders,omitempty"`
}

func dataDir() string {
	if p := strings.TrimSpace(os.Getenv("OVERNIGHT_DATA_DIR")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".trading-platform", "overnight")
}

func loadState(dir string) *State {
	st := &State{Mode: ModePaper, Delta: 0.25}
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Mode != ModeLive {
		st.Mode = ModePaper
	}
	if st.Delta <= 0 || st.Delta >= 1 {
		st.Delta = 0.25
	}
	// A job cut off by a restart is not resumed on its own: its orders may
	// have changed at the broker meanwhile.
	for _, j := range st.Jobs {
		if j.Status == "RUNNING" || j.Status == "SCHEDULED" {
			j.Status = "STOPPED"
			j.Note = "stopped by a restart -- check the position against the broker before running it again"
		}
	}
	return st
}

func (st *State) save(dir string) error {
	if len(st.Jobs) > 40 {
		st.Jobs = st.Jobs[len(st.Jobs)-40:]
	}
	if len(st.Orders) > 2000 {
		st.Orders = st.Orders[len(st.Orders)-2000:]
	}
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state.json"))
}
