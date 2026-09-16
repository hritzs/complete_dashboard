-- Reconciles libs/db/schema/schema.sql with the columns the live database
-- and execution-gateway's Go code (store_postgres.go / store_fills_postgres.go /
-- store_legs_init.go) already actually use. These columns were previously
-- added directly against the running database via undocumented manual
-- ALTER TABLEs, with no corresponding migration file — this migration exists
-- to make that drift idempotent-safe to (re)apply and to document it.
--
-- Deliberately NOT done here: adding UNIQUE(broker_order_id) to orders.
-- GreekSoft's broker_order_id ("gorderid") is only unique within a single
-- trading day in its UAT/test environment (confirmed: the live `orders`
-- table already has 19 broker_order_id values reused across 2-3 unrelated
-- trades on different dates) -- exchange/broker order numbers universally
-- reset per trading day, so a plain UNIQUE constraint here would be wrong,
-- not just currently-violated. Order/fill correlation must be scoped by
-- (broker_order_id, trading day), handled at the application layer.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS order_uid TEXT,
    ADD COLUMN IF NOT EXISTS trade_uid TEXT,
    ADD COLUMN IF NOT EXISTS broker_name TEXT,
    ADD COLUMN IF NOT EXISTS account_id TEXT,
    ADD COLUMN IF NOT EXISTS filled_qty BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS pending_qty BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS avg_fill_price NUMERIC(15, 4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS average_price NUMERIC(12, 4),
    ADD COLUMN IF NOT EXISTS phase TEXT NOT NULL DEFAULT 'PRIMARY',
    ADD COLUMN IF NOT EXISTS hedge_group_id TEXT,
    ADD COLUMN IF NOT EXISTS parent_trade_uid TEXT;

CREATE INDEX IF NOT EXISTS idx_orders_broker_order_id ON orders(broker_order_id);
CREATE INDEX IF NOT EXISTS idx_orders_account_id ON orders(account_id);
CREATE INDEX IF NOT EXISTS idx_orders_trade_uid ON orders(trade_uid);
CREATE INDEX IF NOT EXISTS idx_orders_trade_created ON orders(trade_uid, created_at);
CREATE INDEX IF NOT EXISTS idx_orders_hedge_group ON orders(hedge_group_id);
CREATE INDEX IF NOT EXISTS idx_orders_parent_phase ON orders(parent_trade_uid, phase, status);

ALTER TABLE trades
    ADD COLUMN IF NOT EXISTS user_id TEXT,
    ADD COLUMN IF NOT EXISTS broker_name TEXT,
    ADD COLUMN IF NOT EXISTS account_id TEXT;

CREATE INDEX IF NOT EXISTS idx_trades_account_id ON trades(account_id);
CREATE INDEX IF NOT EXISTS idx_trades_broker_account ON trades(broker_name, account_id);

-- trade_legs already has no duplicate (trade_id, contract_id) rows in the
-- live data (verified before adding this) and execution-gateway's
-- ensureTradeLeg() already assumes this uniqueness at the application
-- layer (SELECT ... FOR UPDATE then insert-if-missing) -- making it a real
-- constraint closes a race condition that pattern alone can't under
-- concurrent writers (e.g. reconciler running alongside execution-gateway).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'trade_legs_trade_contract_key'
    ) THEN
        ALTER TABLE trade_legs
            ADD CONSTRAINT trade_legs_trade_contract_key UNIQUE (trade_id, contract_id);
    END IF;
END $$;
