package trading

// Platform state in Postgres. Everything the gateway keeps outside the
// trades/orders tables -- Straddle Build rules and runs (phase, PMS fills,
// audit events, rejections, stop flags), the LUT build config, LUT day
// state and every minute evaluation, Paper Sim days, the portfolio MTM
// rule's config/state -- is written to the database as well as the local
// file:
//
//	platform_state  -- the latest version of each state document, keyed by
//	                   its path under ~/.trading-platform; upserted on every
//	                   save (sbWriteAtomic) and the portfolio's live view
//	                   every second.
//	platform_events -- append-only history: every LUT minute, every
//	                   portfolio status change, a portfolio snapshot each
//	                   minute.
//
// The files stay the fast local copy. A missing file is restored from the
// database on read (stateRead / stateReadLines), so a lost or cleared
// ~/.trading-platform comes back from the DB.
//
// Writes are queued (latest version per key, coalesced) and flushed by one
// background writer, so a save under a lock never waits on the database.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const stateSchema = `
CREATE TABLE IF NOT EXISTS platform_state (
	key        text PRIMARY KEY,
	kind       text NOT NULL,
	day        text,
	data       jsonb NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS platform_events (
	id   bigserial PRIMARY KEY,
	at   timestamptz NOT NULL DEFAULT now(),
	kind text NOT NULL,
	key  text,
	day  text,
	data jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_platform_events_kind_day ON platform_events (kind, day, at);
CREATE INDEX IF NOT EXISTS idx_platform_events_key ON platform_events (key, id);
CREATE INDEX IF NOT EXISTS idx_platform_state_kind_day ON platform_state (kind, day);
`

type stateEventRow struct {
	at             time.Time
	kind, key, day string
	data           []byte
}

var stateW struct {
	mu      sync.Mutex
	db      *sql.DB
	pending map[string][]byte // key -> latest document
	kick    chan struct{}
	events  chan stateEventRow
	errAt   time.Time
}

var stateDayRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// StartStateDB creates the tables and starts the background writer. Call
// before the engines start so their state can be restored from the DB.
func (s *Service) StartStateDB() {
	pg, ok := s.Store.(*PostgresBackedStore)
	if !ok || pg.db == nil {
		log.Printf("[STATE-DB] no postgres store: state stays in files only")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pg.db.ExecContext(ctx, stateSchema); err != nil {
		log.Printf("[STATE-DB] ⚠ cannot create tables: %v -- state stays in files only", err)
		return
	}
	stateW.mu.Lock()
	stateW.db = pg.db
	stateW.pending = map[string][]byte{}
	stateW.kick = make(chan struct{}, 1)
	stateW.events = make(chan stateEventRow, 8192)
	stateW.mu.Unlock()
	go stateWriter()
	go stateBackfill(pg.db)
	log.Printf("[STATE-DB] platform state mirrored to postgres (platform_state / platform_events)")
}

// stateBackfill loads every state file already on disk that the database
// does not have yet: JSON documents into platform_state, .jsonl histories
// (LUT minutes and chain recordings) into platform_events. The raw feed
// recordings (feedrec/, market-data CSVs) are not platform state.
func stateBackfill(db *sql.DB) {
	docs, lines := 0, 0
	_ = filepath.WalkDir(stateRoot(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "feedrec" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		key := stateKey(path)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		switch {
		case strings.HasSuffix(name, ".json"):
			var n int
			if db.QueryRowContext(ctx, `SELECT count(*) FROM platform_state WHERE key = $1`, key).Scan(&n) != nil || n > 0 {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			info, _ := d.Info()
			at := time.Now()
			if info != nil {
				at = info.ModTime()
			}
			if _, e := db.ExecContext(ctx, `INSERT INTO platform_state (key, kind, day, data, updated_at) VALUES ($1, $2, NULLIF($3, ''), $4::jsonb, $5) ON CONFLICT (key) DO NOTHING`,
				key, stateKind(key), stateDayRe.FindString(key), string(stateJSON(b)), at); e == nil {
				docs++
			}
		case strings.HasSuffix(name, ".jsonl"):
			var n int
			if db.QueryRowContext(ctx, `SELECT count(*) FROM platform_events WHERE key = $1`, key).Scan(&n) != nil || n > 0 {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			info, _ := d.Info()
			at := time.Now()
			if info != nil {
				at = info.ModTime()
			}
			tx, terr := db.BeginTx(ctx, nil)
			if terr != nil {
				return nil
			}
			kind, day, ok := lutJSONLKind(path), stateDayRe.FindString(name), true
			for _, line := range strings.Split(string(b), "\n") {
				if line = strings.TrimSpace(line); line == "" {
					continue
				}
				if _, e := tx.ExecContext(ctx, `INSERT INTO platform_events (at, kind, key, day, data) VALUES ($1, $2, $3, NULLIF($4, ''), $5::jsonb)`,
					at, kind, key, day, string(stateJSON([]byte(line)))); e != nil {
					ok = false
					break
				}
				lines++
			}
			if ok {
				_ = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		return nil
	})
	if docs+lines > 0 {
		log.Printf("[STATE-DB] backfilled %d state document(s) and %d history line(s) from %s", docs, lines, stateRoot())
	}
}

func stateRoot() string { return filepath.Dir(lutDataDir()) }

// stateKey: the path under ~/.trading-platform (absolute path if outside).
func stateKey(path string) string {
	if rel, err := filepath.Rel(stateRoot(), path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func stateKind(key string) string {
	k := strings.TrimSuffix(strings.TrimSuffix(key, ".json"), ".jsonl")
	if i := strings.Index(k, "/"); i > 0 {
		return k[:i]
	}
	return k
}

// stateJSON makes b valid jsonb (non-JSON content is wrapped).
func stateJSON(b []byte) []byte {
	if json.Valid(b) {
		return b
	}
	w, _ := json.Marshal(map[string]string{"raw": string(b)})
	return w
}

// stateMirror queues the latest version of a saved document.
func stateMirror(path string, b []byte) {
	stateMirrorKey(stateKey(path), b)
}

func stateMirrorKey(key string, b []byte) {
	stateW.mu.Lock()
	defer stateW.mu.Unlock()
	if stateW.db == nil {
		return
	}
	stateW.pending[key] = append([]byte(nil), b...)
	select {
	case stateW.kick <- struct{}{}:
	default:
	}
}

// stateEvent appends one history row (never blocks; dropped if the queue
// is full, which is logged).
func stateEvent(kind, key, day string, data []byte) {
	stateW.mu.Lock()
	ch := stateW.events
	stateW.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- stateEventRow{at: time.Now(), kind: kind, key: key, day: day, data: append([]byte(nil), data...)}:
	default:
		log.Printf("[STATE-DB] ⚠ event queue full -- %s %s not saved to the DB (still in its file)", kind, key)
	}
}

func stateWriter() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stateW.kick:
		case <-t.C:
		case ev := <-stateW.events:
			stateInsertEvents(ev)
			continue
		}
		stateFlush()
	}
}

func stateFlush() {
	stateW.mu.Lock()
	db, batch := stateW.db, stateW.pending
	if len(batch) == 0 {
		stateW.mu.Unlock()
		return
	}
	stateW.pending = map[string][]byte{}
	stateW.mu.Unlock()
	for key, b := range batch {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		day := stateDayRe.FindString(key)
		_, err := db.ExecContext(ctx, `
			INSERT INTO platform_state (key, kind, day, data, updated_at) VALUES ($1, $2, NULLIF($3, ''), $4::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET data = EXCLUDED.data, kind = EXCLUDED.kind, day = EXCLUDED.day, updated_at = now()`,
			key, stateKind(key), day, string(stateJSON(b)))
		cancel()
		if err != nil {
			stateW.mu.Lock()
			if _, newer := stateW.pending[key]; !newer {
				stateW.pending[key] = b // retry with the next flush
			}
			logIt := time.Since(stateW.errAt) > time.Minute
			if logIt {
				stateW.errAt = time.Now()
			}
			stateW.mu.Unlock()
			if logIt {
				log.Printf("[STATE-DB] ⚠ save %s: %v -- retrying (the file copy is current)", key, err)
			}
		}
	}
}

func stateInsertEvents(first stateEventRow) {
	rows := []stateEventRow{first}
	for len(rows) < 500 {
		select {
		case ev := <-stateW.events:
			rows = append(rows, ev)
			continue
		default:
		}
		break
	}
	stateW.mu.Lock()
	db := stateW.db
	stateW.mu.Unlock()
	for _, ev := range rows {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := db.ExecContext(ctx, `INSERT INTO platform_events (at, kind, key, day, data) VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5::jsonb)`,
			ev.at, ev.kind, ev.key, ev.day, string(stateJSON(ev.data)))
		cancel()
		if err != nil {
			log.Printf("[STATE-DB] ⚠ event %s %s not saved: %v", ev.kind, ev.key, err)
		}
	}
}

// stateRead reads a state file; when the file is missing it is restored
// from the database (and written back to disk).
func stateRead(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	stateW.mu.Lock()
	db := stateW.db
	stateW.mu.Unlock()
	if db == nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var data string
	if qerr := db.QueryRowContext(ctx, `SELECT data::text FROM platform_state WHERE key = $1`, stateKey(path)).Scan(&data); qerr != nil {
		return nil, err // not in the DB either: the original "not exist"
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if werr := os.WriteFile(path, []byte(data), 0o644); werr == nil {
		log.Printf("[STATE-DB] restored %s from the database", stateKey(path))
	}
	return []byte(data), nil
}

// stateReadLines restores a missing append-only .jsonl file from its
// events (one JSON document per line, in order).
func stateReadLines(path, kind string) {
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return
	}
	stateW.mu.Lock()
	db := stateW.db
	stateW.mu.Unlock()
	if db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT data::text FROM platform_events WHERE kind = $1 AND key = $2 ORDER BY id`, kind, stateKey(path))
	if err != nil {
		return
	}
	defer rows.Close()
	var sb strings.Builder
	n := 0
	for rows.Next() {
		var line string
		if rows.Scan(&line) == nil {
			sb.WriteString(line)
			sb.WriteByte('\n')
			n++
		}
	}
	if n == 0 {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if os.WriteFile(path, []byte(sb.String()), 0o644) == nil {
		log.Printf("[STATE-DB] restored %s (%d lines) from the database", stateKey(path), n)
	}
}
