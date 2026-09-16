// Package persistence writes reconciler-derived order/fill state to
// Postgres transactionally.
//
// This is a separate package from libs/go-common/db deliberately:
// go-common/db has three independent compile bugs (invalid struct tags, a
// reference to a broker.BrokerOrder type that doesn't exist, illegal
// self-package-qualified references) and is imported by nothing else in
// the repo. Rather than resurrect an orphaned, broken package, this store
// is modeled directly on execution-gateway's own proven transaction
// pattern (internal/trading/store_fills_postgres.go's
// ensureTradeLeg/recomputeTradeLegFromPersistedFills), ported here since
// those are unexported symbols in a different service's internal
// package and so can't be imported directly.
package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"trading-platform/services/reconciler/internal/normalize"
)

// ErrOrderNotFound is returned when no order row matches the incoming
// broker_order_id for the current trading day. broker_order_id
// (GreekSoft's gorderid) is only unique per trading day -- confirmed
// against live data, see docs/greeksoft-integration-architecture.md --
// so the lookup is deliberately scoped to CURRENT_DATE, not global.
var ErrOrderNotFound = fmt.Errorf("no order found for broker_order_id in current trading day")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ApplyResult reports what ApplyOrderUpdate actually wrote, so a caller
// (main.go) can publish an accurate event -- the persistence layer is the
// only place that knows the synthesized fill_id and the order's
// trade_uid/instrument token, so it hands them back rather than making
// the publisher guess at or re-derive them.
type ApplyResult struct {
	TradeUID string
	Fill     *PersistedFill // nil if this update didn't represent a new fill
	// ConfirmationLatency is the time between when execution-gateway
	// created the local order row (order submission) and this Iris push
	// being processed -- i.e. GreekSoft's real end-to-end order
	// confirmation latency, not a synthetic estimate.
	ConfirmationLatency time.Duration
}

type PersistedFill struct {
	FillID       string
	InstrumentID int64 // contracts.broker_token for the order's contract
	Event        normalize.FillEvent
}

// ApplyOrderUpdate persists one normalized OrderResponse: it updates the
// matching order row and appends an order_events row, and -- if the
// update represents a new fill -- inserts the fill and recomputes the
// affected trade leg, all in a single transaction. rawFrame is stored
// verbatim in order_events/fills for audit purposes.
//
// Returns ErrOrderNotFound (not a hard error) if no order row matches --
// callers should log and continue rather than treat this as fatal, since
// it can legitimately happen for orders not yet visible to this process
// or placed through another channel.
func (s *Store) ApplyOrderUpdate(ctx context.Context, update normalize.OrderUpdate, rawFrame []byte) (ApplyResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		orderID             int64
		tradeID             sql.NullInt64
		contractID          sql.NullInt64
		orderQuantity       int64
		previouslyFilledQty int64
		tradeUID            sql.NullString
		orderCreatedAt      time.Time
	)

	err = tx.QueryRowContext(ctx, `
		SELECT id, trade_id, contract_id, quantity, filled_qty, trade_uid, created_at
		FROM orders
		WHERE broker_order_id = $1
		  AND created_at::date = CURRENT_DATE
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE
	`, update.BrokerOrderID).Scan(&orderID, &tradeID, &contractID, &orderQuantity, &previouslyFilledQty, &tradeUID, &orderCreatedAt)
	if err == sql.ErrNoRows {
		return ApplyResult{}, ErrOrderNotFound
	}
	if err != nil {
		return ApplyResult{}, fmt.Errorf("lookup order broker_order_id=%s: %w", update.BrokerOrderID, err)
	}

	result := ApplyResult{
		TradeUID:            tradeUID.String,
		ConfirmationLatency: time.Since(orderCreatedAt),
	}

	exchangeOrderIDArg := interface{}(nil)
	if update.ExchangeOrderID != "" {
		exchangeOrderIDArg = update.ExchangeOrderID
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET status = $1,
		    filled_qty = $2,
		    pending_qty = $3,
		    exchange_order_id = COALESCE($4, exchange_order_id),
		    updated_at = NOW()
		WHERE id = $5
	`, string(update.Status), update.FilledQtyToday, update.PendingQty, exchangeOrderIDArg, orderID); err != nil {
		return ApplyResult{}, fmt.Errorf("update order id=%d: %w", orderID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO order_events (order_id, status, reason_text, event_timestamp, raw_broker_response)
		VALUES ($1, $2, $3, $4, $5)
	`, orderID, string(update.Status), update.ReasonText, update.BrokerTimestamp, rawFrame); err != nil {
		return ApplyResult{}, fmt.Errorf("insert order_events order_id=%d: %w", orderID, err)
	}

	if fill, ok := normalize.DeriveFill(update, previouslyFilledQty); ok && tradeID.Valid && contractID.Valid {
		fillID := update.FillID
		if fillID == "" {
			fillID = fmt.Sprintf("%s:%d:%d", update.BrokerOrderID, update.BrokerTimestamp.Unix(), update.FilledQtyToday)
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO fills (order_id, trade_id, fill_id, fill_quantity, fill_price, fill_timestamp, raw_broker_fill)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (fill_id, order_id) DO NOTHING
		`, orderID, tradeID.Int64, fillID, fill.FillQtyDelta, fill.FillPrice, fill.BrokerTimestamp, rawFrame)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("insert fill order_id=%d: %w", orderID, err)
		}

		if rows, _ := res.RowsAffected(); rows > 0 {
			if err := ensureTradeLeg(ctx, tx, tradeID.Int64, contractID.Int64, orderQuantity); err != nil {
				return ApplyResult{}, err
			}
			if err := recomputeTradeLegFromPersistedFills(ctx, tx, tradeID.Int64, contractID.Int64); err != nil {
				return ApplyResult{}, err
			}

			var brokerToken int64
			if err := tx.QueryRowContext(ctx, `SELECT broker_token FROM contracts WHERE id = $1`, contractID.Int64).Scan(&brokerToken); err != nil {
				return ApplyResult{}, fmt.Errorf("lookup contract broker_token id=%d: %w", contractID.Int64, err)
			}
			result.Fill = &PersistedFill{FillID: fillID, InstrumentID: brokerToken, Event: fill}
		}
	}

	if err := tx.Commit(); err != nil {
		return ApplyResult{}, fmt.Errorf("commit: %w", err)
	}
	return result, nil
}

// ensureTradeLeg and recomputeTradeLegFromPersistedFills below are ported
// verbatim (same SQL, same accounting) from
// services/execution-gateway/internal/trading/store_fills_postgres.go, to
// keep GreekSoft-fill accounting behaving identically regardless of which
// process (execution-gateway or the reconciler) records it.

func ensureTradeLeg(ctx context.Context, tx *sql.Tx, tradeID int64, contractID int64, targetQuantity int64) error {
	var legID int64

	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM trade_legs
		WHERE trade_id = $1
		  AND contract_id = $2
		FOR UPDATE
	`, tradeID, contractID).Scan(&legID)

	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("load trade leg trade_id=%d contract_id=%d: %w", tradeID, contractID, err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO trade_legs (
			trade_id, contract_id, target_quantity, current_quantity,
			avg_entry_price, avg_exit_price, realized_pnl, status, created_at, updated_at
		)
		VALUES ($1,$2,$3,0,NULL,NULL,0,'OPEN',NOW(),NOW())
	`, tradeID, contractID, targetQuantity)
	if err != nil {
		return fmt.Errorf("create trade leg trade_id=%d contract_id=%d: %w", tradeID, contractID, err)
	}

	return nil
}

func recomputeTradeLegFromPersistedFills(ctx context.Context, tx *sql.Tx, tradeID int64, contractID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT o.side, f.fill_quantity, f.fill_price
		FROM fills f
		JOIN orders o ON o.id = f.order_id
		WHERE f.trade_id = $1
		  AND o.contract_id = $2
		ORDER BY f.fill_timestamp, f.id
	`, tradeID, contractID)
	if err != nil {
		return fmt.Errorf("load fills for trade leg trade_id=%d contract_id=%d: %w", tradeID, contractID, err)
	}
	defer rows.Close()

	var (
		buyQty, sellQty     int64
		buyValue, sellValue float64
	)

	for rows.Next() {
		var (
			side  string
			qty   int64
			price float64
		)
		if err := rows.Scan(&side, &qty, &price); err != nil {
			return fmt.Errorf("scan leg fill: %w", err)
		}
		switch strings.ToUpper(strings.TrimSpace(side)) {
		case "BUY":
			buyQty += qty
			buyValue += float64(qty) * price
		case "SELL":
			sellQty += qty
			sellValue += float64(qty) * price
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate leg fills: %w", err)
	}

	buyAvg, sellAvg := 0.0, 0.0
	if buyQty > 0 {
		buyAvg = buyValue / float64(buyQty)
	}
	if sellQty > 0 {
		sellAvg = sellValue / float64(sellQty)
	}

	netQty := buyQty - sellQty
	matchedQty := minInt64(buyQty, sellQty)

	realizedPnL := 0.0
	if matchedQty > 0 {
		realizedPnL = (sellAvg - buyAvg) * float64(matchedQty)
	}

	var (
		entryPrice interface{}
		exitPrice  interface{}
		status     string
	)

	switch {
	case netQty < 0:
		entryPrice = sellAvg
		exitPrice = nil
		status = "OPEN"
	case netQty > 0:
		entryPrice = buyAvg
		exitPrice = nil
		status = "OPEN"
	default:
		entryPrice = nil
		if matchedQty > 0 {
			exitPrice = buyAvg
		}
		status = "CLOSED"
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE trade_legs
		SET target_quantity = $3, current_quantity = $4, avg_entry_price = $5,
		    avg_exit_price = $6, realized_pnl = $7, status = $8, updated_at = NOW()
		WHERE trade_id = $1 AND contract_id = $2
	`, tradeID, contractID, maxInt64(buyQty, sellQty), netQty, entryPrice, exitPrice, realizedPnL, status)
	if err != nil {
		return fmt.Errorf("update trade leg trade_id=%d contract_id=%d: %w", tradeID, contractID, err)
	}

	return nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
