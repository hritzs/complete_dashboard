package db

import (
"time"

"github.com/jackc/pgx/v5/pgtype"
)

// Trade represents a row from the 'trades' table.
type Trade struct {
ID          int64            db:"id"
TradeUID    string           db:"trade_uid"
StrategyID  pgtype.Int4      db:"strategy_id"
Symbol      string           db:"symbol"
Status      string           db:"status"
Config      []byte           db:"config" // JSONB
RealizedPNL float64          db:"realized_pnl"
CreatedAt   time.Time        db:"created_at"
ClosedAt    pgtype.Timestamp db:"closed_at"
}

// Fill represents a row from the 'fills' table, potentially joined with other tables.
type Fill struct {
ID           int64     db:"id"
OrderID      int64     db:"order_id"
TradeID      int64     db:"trade_id"
FillID       string    db:"fill_id"
FillQuantity int64     db:"fill_quantity"
FillPrice    float64   db:"fill_price"
// Joined from 'orders' and 'contracts' tables
Side         string db:"side"
BrokerToken  int64  db:"broker_token"
}
