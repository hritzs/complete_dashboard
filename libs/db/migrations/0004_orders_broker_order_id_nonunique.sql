-- Greeksoft test/orderbook environments can reuse broker order ids across sessions/accounts.
-- Do not enforce global uniqueness on broker_order_id.
-- intent_id remains the primary idempotency key for platform orders.

ALTER TABLE orders
DROP CONSTRAINT IF EXISTS orders_broker_order_id_key;

CREATE INDEX IF NOT EXISTS idx_orders_broker_order_id
ON orders(broker_order_id);
