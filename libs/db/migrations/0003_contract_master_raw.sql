CREATE TABLE IF NOT EXISTS contract_master_raw (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_contract_master_raw_created_at
ON contract_master_raw(created_at DESC);
