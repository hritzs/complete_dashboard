package greeksoft

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net/http/httptrace"
	"sync"
	"time"
)

type orderTimingKey struct{}

type timingEvent struct {
	Phase     string  `json:"phase"`
	At        string  `json:"at"`
	ElapsedMS float64 `json:"elapsed_ms"`
	Failed    bool    `json:"failed,omitempty"`
	Reused    bool    `json:"reused,omitempty"`
	WasIdle   bool    `json:"was_idle,omitempty"`
	IdleMS    float64 `json:"idle_ms,omitempty"`
}

type orderHTTPTiming struct {
	mu     sync.Mutex
	start  time.Time
	tag    string
	events []timingEvent
}

func (t *orderHTTPTiming) event(phase string, failed bool) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, timingEvent{Phase: phase, At: now.UTC().Format(time.RFC3339Nano), ElapsedMS: float64(now.Sub(t.start)) / float64(time.Millisecond), Failed: failed})
}

// Trace callbacks may run concurrently (including parallel connection attempts).
// Keep every event rather than pairing overlapping attempts into misleading totals.
func traceOrderHTTP(ctx context.Context) (context.Context, *orderHTTPTiming) {
	tag, ok := ctx.Value(orderTimingKey{}).(string)
	if !ok {
		return ctx, nil
	}
	t := &orderHTTPTiming{start: time.Now(), tag: tag}
	t.event("prepare", false)
	trace := &httptrace.ClientTrace{
		GetConn:           func(string) { t.event("get_conn", false) },
		DNSStart:          func(httptrace.DNSStartInfo) { t.event("dns_start", false) },
		DNSDone:           func(i httptrace.DNSDoneInfo) { t.event("dns_done", i.Err != nil) },
		ConnectStart:      func(string, string) { t.event("connect_start", false) },
		ConnectDone:       func(_, _ string, err error) { t.event("connect_done", err != nil) },
		TLSHandshakeStart: func() { t.event("tls_start", false) },
		TLSHandshakeDone:  func(_ tls.ConnectionState, err error) { t.event("tls_done", err != nil) },
		GotConn: func(i httptrace.GotConnInfo) {
			now := time.Now()
			t.mu.Lock()
			defer t.mu.Unlock()
			t.events = append(t.events, timingEvent{Phase: "got_conn", At: now.UTC().Format(time.RFC3339Nano), ElapsedMS: float64(now.Sub(t.start)) / float64(time.Millisecond), Reused: i.Reused, WasIdle: i.WasIdle, IdleMS: float64(i.IdleTime) / float64(time.Millisecond)})
		},
		WroteHeaders:         func() { t.event("wrote_headers", false) },
		WroteRequest:         func(i httptrace.WroteRequestInfo) { t.event("wrote_request", i.Err != nil) },
		GotFirstResponseByte: func() { t.event("first_response_byte", false) },
	}
	return httptrace.WithClientTrace(ctx, trace), t
}

func (t *orderHTTPTiming) finish(stage string, status, size int) {
	if t == nil {
		return
	}
	t.event("finished", stage != "done")
	t.mu.Lock()
	record := struct {
		Tag           string        `json:"tag"`
		Stage         string        `json:"stage"`
		Status        int           `json:"http_status"`
		ResponseBytes int           `json:"response_bytes"`
		Events        []timingEvent `json:"events"`
	}{t.tag, stage, status, size, append([]timingEvent(nil), t.events...)}
	t.mu.Unlock()
	b, err := json.Marshal(record)
	if err == nil {
		log.Printf("[GREEKSOFT HTTP TIMING] %s", b)
	}
}
