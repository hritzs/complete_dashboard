-- 0001_initial_schema.up.sql

-- Enable UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Trades table to group orders for a single strategy execution
CREATE TABLE trades (
    id BIGSERIAL PRIMARY KEY,
    uuid UUID NOT NULL DEFAULT uuid_generate_v4(),
    strategy_id VARCHAR(255) NOT NULL,
    symbol VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL, -- e.g., ACTIVE, PAUSED, COMPLETED
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trades_strategy_id ON trades(strategy_id);
CREATE INDEX idx_trades_status ON trades(status);
CREATE UNIQUE INDEX idx_trades_uuid ON trades(uuid);

-- Orders table to track every order intent and its lifecycle
CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY,
    intent_id BIGINT NOT NULL,
    trade_id BIGINT NOT NULL REFERENCES trades(id),
    broker_order_id VARCHAR(255),
    exchange_order_id VARCHAR(255),

    instrument_id INT NOT NULL,
    side VARCHAR(10) NOT NULL,
    order_type VARCHAR(20) NOT NULL,
    product_type VARCHAR(10) NOT NULL,

    quantity INT NOT NULL,
    limit_price DECIMAL(15, 5),
    trigger_price DECIMAL(15, 5),

    status VARCHAR(50) NOT NULL,
    filled_quantity INT NOT NULL DEFAULT 0,
    pending_quantity INT NOT NULL,
    average_price DECIMAL(15, 5),

    reason_code VARCHAR(50),
    reason_text TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    broker_timestamp TIMESTAMPTZ,

    -- Raw broker data for debugging
    raw_broker_response JSONB
);

CREATE UNIQUE INDEX idx_orders_intent_id ON orders(intent_id);
CREATE INDEX idx_orders_trade_id ON orders(trade_id);
CREATE INDEX idx_orders_broker_order_id ON orders(broker_order_id);
CREATE INDEX idx_orders_status ON orders(status);

-- Fills table to record every partial or full fill for an order
CREATE TABLE fills (
    id BIGSERIAL PRIMARY KEY,
    fill_id BIGINT NOT NULL,
    order_id BIGINT NOT NULL REFERENCES orders(id),
    trade_id BIGINT NOT NULL REFERENCES trades(id),

    instrument_id INT NOT NULL,
    side VARCHAR(10) NOT NULL,
    fill_quantity INT NOT NULL,
    fill_price DECIMAL(15, 5) NOT NULL,

    fill_time TIMESTAMPTZ NOT NULL,

    -- Raw broker data for debugging
    raw_broker_fill_response JSONB
);

CREATE UNIQUE INDEX idx_fills_fill_id ON fills(fill_id);
CREATE INDEX idx_fills_order_id ON fills(order_id);
CREATE INDEX idx_fills_trade_id ON fills(trade_id);

-- Create a trigger to update the 'updated_at' column on row update
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_trades_updated_at BEFORE UPDATE ON trades FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();
CREATE TRIGGER update_orders_updated_at BEFORE UPDATE ON orders FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();