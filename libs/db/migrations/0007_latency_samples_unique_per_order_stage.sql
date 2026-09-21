-- One latency sample per (order, stage), keeping the smallest value. The
-- reconciler's RecordLatency upserts against this index (and also creates it
-- idempotently on start, so applying this file by hand is optional).
CREATE UNIQUE INDEX IF NOT EXISTS idx_latency_samples_order_stage
    ON latency_samples (order_id, stage)
    WHERE order_id IS NOT NULL;
