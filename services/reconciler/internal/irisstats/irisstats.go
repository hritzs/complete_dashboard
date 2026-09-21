// Package irisstats keeps live counters for the Iris order-push websocket
// so the UI can show real evidence -- not an assumption -- that order and
// fill confirmations arrive over the websocket.
package irisstats

import (
	"sync"
	"time"
)

// LiveWindow is how recently a frame must have arrived for the feed to be
// reported as live. GreekSoft sends a heartbeat every ~10s, so three missed
// heartbeats means the connection is not delivering anything.
const LiveWindow = 30 * time.Second

type Kind int

const (
	KindOther Kind = iota
	KindHeartbeat
	KindOrder
	KindTrade
)

type Stats struct {
	mu sync.Mutex

	host      string
	startedAt time.Time

	lastFrame, lastHeartbeat, lastOrder, lastTrade time.Time
	frames, heartbeats, orders, trades             int64
	applied, unmatched                             int64
}

func New(host string, now time.Time) *Stats {
	return &Stats{host: host, startedAt: now}
}

// Frame records one frame received from the websocket.
func (s *Stats) Frame(kind Kind, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames++
	s.lastFrame = now
	switch kind {
	case KindHeartbeat:
		s.heartbeats++
		s.lastHeartbeat = now
	case KindOrder:
		s.orders++
		s.lastOrder = now
	case KindTrade:
		s.trades++
		s.lastTrade = now
	}
}

// Applied records a push that was matched to a local order and persisted.
func (s *Stats) Applied() {
	s.mu.Lock()
	s.applied++
	s.mu.Unlock()
}

// Unmatched records a push that never matched a local order row.
func (s *Stats) Unmatched() {
	s.mu.Lock()
	s.unmatched++
	s.mu.Unlock()
}

type Snapshot struct {
	Host             string     `json:"host"`
	Connected        bool       `json:"connected"`
	StartedAt        time.Time  `json:"started_at"`
	LastFrameAgeSec  *float64   `json:"last_frame_age_sec"`
	LastHeartbeatAt  *time.Time `json:"last_heartbeat_at"`
	LastOrderPushAt  *time.Time `json:"last_order_push_at"`
	LastTradePushAt  *time.Time `json:"last_trade_push_at"`
	Frames           int64      `json:"frames"`
	Heartbeats       int64      `json:"heartbeats"`
	OrderPushes      int64      `json:"order_pushes"`
	TradePushes      int64      `json:"trade_pushes"`
	Applied          int64      `json:"applied"`
	UnmatchedGaveUp  int64      `json:"unmatched_gave_up"`
	LiveWindowSecond float64    `json:"live_window_sec"`
}

func opt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Stats) Snapshot(now time.Time) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{
		Host:             s.host,
		StartedAt:        s.startedAt,
		LastHeartbeatAt:  opt(s.lastHeartbeat),
		LastOrderPushAt:  opt(s.lastOrder),
		LastTradePushAt:  opt(s.lastTrade),
		Frames:           s.frames,
		Heartbeats:       s.heartbeats,
		OrderPushes:      s.orders,
		TradePushes:      s.trades,
		Applied:          s.applied,
		UnmatchedGaveUp:  s.unmatched,
		LiveWindowSecond: LiveWindow.Seconds(),
	}
	if !s.lastFrame.IsZero() {
		age := now.Sub(s.lastFrame).Seconds()
		snap.LastFrameAgeSec = &age
		snap.Connected = now.Sub(s.lastFrame) <= LiveWindow
	}
	return snap
}
