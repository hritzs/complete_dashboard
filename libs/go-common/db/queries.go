package db

import (
	"context"
	"fmt"
	"regexp"
	broker "trading-platform/libs/broker-greeksoft"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Queries is a struct that holds the database pool.
type Queries struct {
	*pgxpool.Pool
	log *logrus.Entry
}

func NewQueries(pool *pgxpool.Pool, logger *logrus.Logger) *Queries {
	return &Queries{
		Pool: pool,
		log:  logger.WithField("component", "db-queries"),
	}
}

// TradeUIDRegex is exported so other packages can use it to extract trade UIDs.
var TradeUIDRegex = regexp.MustCompile(`((?:ny|sx|bn|fn|mc)\d{12}[a-z])`)

// UpsertOrderFromBroker inserts or updates an order based on the broker's data.
func (q *Queries) UpsertOrderFromBroker(ctx context.Context, order broker.BrokerOrder) error {
	// 1. Extract trade_uid from the OrderUniqueIdentifier
	var tradeUID string
	matches := TradeUIDRegex.FindStringSubmatch(order.OrderUniqueIdentifier)
	if len(matches) > 1 {
		tradeUID = matches[1]
	} else {
		q.log.Warnf("Could not extract trade_uid from OrderUniqueIdentifier: %s", order.OrderUniqueIdentifier)
	}

	// 2. Find internal trade_id and contract_id
	var internalTradeID, internalContractID int
	if tradeUID != "" {
		err := q.QueryRow(ctx, "SELECT id FROM trades WHERE trade_uid = $1", tradeUID).Scan(&internalTradeID)
		if err != nil {
			q.log.Warnf("Could not find internal trade_id for trade_uid %s: %v", tradeUID, err)
		}
	}

	err := q.QueryRow(ctx, "SELECT id FROM contracts WHERE broker_token = $1", order.ExchangeInstrumentID).Scan(&internalContractID)
	if err != nil {
		q.log.Warnf("Could not find internal contract_id for broker_token %d: %v", order.ExchangeInstrumentID, err)
		return fmt.Errorf("contract not found for token %d", order.ExchangeInstrumentID)
	}

	// 3. Upsert the order
	sql := `
		INSERT INTO orders (trade_id, contract_id, broker_order_id, side, quantity, order_type, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'LIMIT', $6, NOW())
		ON CONFLICT (broker_order_id) DO UPDATE SET
			status = EXCLUDED.status,
			updated_at = NOW();
	`
	var tradeIDArg interface{} = internalTradeID
	if internalTradeID == 0 {
		tradeIDArg = nil
	}

	_, err = q.Exec(ctx, sql, tradeIDArg, internalContractID, order.AppOrderID, order.OrderSide, order.OrderQuantity, order.OrderStatus)
	if err != nil {
		q.log.Errorf("Failed to upsert order %s: %v", order.AppOrderID, err)
		return err
	}
	return nil
}

// GetTradeByUID fetches a single trade from the database by its unique ID.
func (q *Queries) GetTradeByUID(ctx context.Context, tradeUID string) (*db.Trade, error) {
	var t db.Trade
	sql := `SELECT id, trade_uid, strategy_id, symbol, status, config, realized_pnl, created_at, closed_at
			FROM trades WHERE trade_uid = $1`
	err := q.QueryRow(ctx, sql, &t.ID, &t.TradeUID, &t.StrategyID, &t.Symbol, &t.Status, &t.Config,
		&t.RealizedPNL, &t.CreatedAt, &t.ClosedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get trade by UID %s: %w", tradeUID, err)
	}
	return &t, nil
}

// GetFillsForTrade fetches all fills associated with a given internal trade ID.
func (q *Queries) GetFillsForTrade(ctx context.Context, tradeID int64) ([]db.Fill, error) {
	var fills []db.Fill
	sql := `
		SELECT f.id, f.order_id, f.trade_id, f.fill_id, f.fill_quantity, f.fill_price,
		       o.side, c.broker_token
		FROM fills f
		JOIN orders o ON f.order_id = o.id
		JOIN contracts c ON o.contract_id = c.id
		WHERE f.trade_id = $1
		ORDER BY f.fill_timestamp ASC
	`
	rows, err := q.Query(ctx, sql, tradeID)
	if err != nil {
		return nil, fmt.Errorf("failed to query fills for trade %d: %w", tradeID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var fill db.Fill
		if err := rows.Scan(&fill.ID, &fill.OrderID, &fill.TradeID, &fill.FillID, &fill.FillQuantity,
			&fill.FillPrice, &fill.Side, &fill.BrokerToken); err != nil {
			return nil, fmt.Errorf("failed to scan fill row for trade %d: %w", tradeID, err)
		}
		fills = append(fills, fill)
	}
	return fills, nil
}