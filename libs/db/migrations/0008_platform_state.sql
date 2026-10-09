-- Platform state mirrored from the execution gateway (also created by the
-- gateway on startup: internal/trading/state_db.go).
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
