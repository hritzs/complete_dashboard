-- Latency samples: a stage-discriminated table so any future latency
-- source (feed-decoder tick-to-publish, order-to-REST-ack, etc.) can
-- share this table without a schema change. v1 only writes
-- stage='iris_confirmation' (see services/reconciler/internal/persistence/latency.go).
CREATE TABLE IF NOT EXISTS latency_samples (
    id              BIGSERIAL PRIMARY KEY,
    stage           TEXT NOT NULL,
    order_id        BIGINT REFERENCES orders(id),
    trade_uid       TEXT,
    broker_order_id TEXT NOT NULL,
    latency_us      BIGINT NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_latency_samples_stage_recorded_at
    ON latency_samples(stage, recorded_at DESC);
