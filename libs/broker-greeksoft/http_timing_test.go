package greeksoft

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func timingRecords(t *testing.T, b *bytes.Buffer) []struct {
	Tag    string        `json:"tag"`
	Stage  string        `json:"stage"`
	Status int           `json:"http_status"`
	Events []timingEvent `json:"events"`
} {
	t.Helper()
	var out []struct {
		Tag    string        `json:"tag"`
		Stage  string        `json:"stage"`
		Status int           `json:"http_status"`
		Events []timingEvent `json:"events"`
	}
	for _, line := range strings.Split(b.String(), "\n") {
		_, data, ok := strings.Cut(line, "[GREEKSOFT HTTP TIMING] ")
		if !ok {
			continue
		}
		var r struct {
			Tag    string        `json:"tag"`
			Stage  string        `json:"stage"`
			Status int           `json:"http_status"`
			Events []timingEvent `json:"events"`
		}
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestOrderHTTPTimingTLSReuseAndResponseWait(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	c := NewClient("", server.URL)
	c.HTTPClient = server.Client()
	for i := 0; i < 2; i++ {
		var out map[string]bool
		ctx := context.WithValue(context.Background(), orderTimingKey{}, "TEST_ORDER")
		_, _, err := c.postJSON(ctx, server.URL, "SECRET_AUTH_TOKEN", map[string]string{"private": "SECRET_BODY"}, &out)
		if err != nil || !out["ok"] {
			t.Fatalf("response %v, %v", out, err)
		}
	}
	records := timingRecords(t, &logs)
	if len(records) != 2 {
		t.Fatalf("records: %s", logs.String())
	}
	for i, r := range records {
		if r.Tag != "TEST_ORDER" || r.Stage != "done" || r.Status != 200 {
			t.Fatalf("bad record %+v", r)
		}
		phases := map[string]timingEvent{}
		for _, e := range r.Events {
			phases[e.Phase] = e
			if _, err := time.Parse(time.RFC3339Nano, e.At); err != nil {
				t.Fatal(err)
			}
		}
		for _, phase := range []string{"prepare", "get_conn", "got_conn", "wrote_request", "first_response_byte", "finished"} {
			if _, ok := phases[phase]; !ok {
				t.Fatalf("missing %s: %+v", phase, r)
			}
		}
		if phases["first_response_byte"].ElapsedMS-phases["wrote_request"].ElapsedMS < 20 {
			t.Fatalf("response wait not captured: %+v", r)
		}
		if phases["finished"].ElapsedMS-phases["first_response_byte"].ElapsedMS < 20 {
			t.Fatalf("body wait not captured: %+v", r)
		}
		if i == 0 {
			if _, ok := phases["tls_done"]; !ok {
				t.Fatal("missing TLS")
			}
		}
		if i == 1 && !phases["got_conn"].Reused {
			t.Fatal("connection reuse not captured")
		}
	}
	if strings.Contains(logs.String(), "SECRET") {
		t.Fatal("sensitive request data logged")
	}
}

func TestOrderHTTPTimingErrorAndScope(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "invalid JSON") }))
	defer server.Close()
	c := NewClient("", server.URL)
	var out map[string]bool
	if _, _, err := c.getJSON(context.Background(), server.URL, "secret", &out); err == nil {
		t.Fatal("expected decode error")
	}
	if logs.Len() != 0 {
		t.Fatal("non-order request traced")
	}
	ctx := context.WithValue(context.Background(), orderTimingKey{}, "BAD_JSON")
	if _, _, err := c.getJSON(ctx, server.URL, "secret", &out); err == nil {
		t.Fatal("expected decode error")
	}
	cancelled, cancel := context.WithCancel(context.WithValue(context.Background(), orderTimingKey{}, "CANCELLED"))
	cancel()
	if _, _, err := c.getJSON(cancelled, server.URL, "secret", &out); err == nil {
		t.Fatal("expected cancellation")
	}
	records := timingRecords(t, &logs)
	if len(records) != 2 || records[0].Stage != "decode" || records[1].Stage != "http_round_trip" {
		t.Fatalf("bad error records %+v", records)
	}
}
