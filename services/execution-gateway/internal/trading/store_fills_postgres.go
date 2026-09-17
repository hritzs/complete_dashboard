package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

var _ VerifiedFillPersistence = (*PostgresBackedStore)(nil)

func (s *PostgresBackedStore) PersistVerifiedFills(
	ctx context.Context,
	brokerName string,
	accountID string,
	fills []BrokerFill,
) (FillPersistenceReport, error) {
	report := FillPersistenceReport{}

	log.Printf("📦 PersistVerifiedFills called: broker=%s account=%s fills=%d", brokerName, accountID, len(fills))
	if s == nil || s.db == nil {
		return report, fmt.Errorf("postgres store is unavailable")
	}

	brokerName = strings.TrimSpace(brokerName)
	accountID = strings.TrimSpace(accountID)

	if brokerName == "" || accountID == "" {
		return report, fmt.Errorf("broker_name and account_id are required")
	}

	for _, fill := range fills {
		report.Processed++

		if !fill.Verified ||
			strings.TrimSpace(fill.BrokerOrderID) == "" ||
			fill.FilledQty <= 0 ||
			fill.AveragePrice <= 0 {
			report.Skipped++
			continue
		}

		inserted, err := s.persistVerifiedFill(
			ctx,
			brokerName,
			accountID,
			fill,
		)
		if err != nil {
			if strings.Contains(err.Error(), "no local order") {
				log.Printf("📦 persistVerifiedFill result: broker_order_id=%s inserted=%t err=%v", fill.BrokerOrderID, inserted, err)
				report.Unmatched = append(report.Unmatched, fill.BrokerOrderID)
				continue
			}

			report.Errors = append(
				report.Errors,
				fmt.Sprintf("broker_order_id=%s: %v", fill.BrokerOrderID, err),
			)
			continue
		}

		if inserted {
			report.Persisted++
		} else {
			report.Skipped++
		}
	}

	log.Printf("📦 PersistVerifiedFills done: processed=%d persisted=%d skipped=%d unmatched=%v errors=%v", report.Processed, report.Persisted, report.Skipped, report.Unmatched, report.Errors)

	if len(report.Errors) > 0 {
		return report, fmt.Errorf("one or more verified fills could not be persisted")
	}

	return report, nil
}

func (s *PostgresBackedStore) persistVerifiedFill(
	ctx context.Context,
	brokerName string,
	accountID string,
	fill BrokerFill,
) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin fill transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var (
		orderID       int64
		tradeID       int64
		contractID    int64
		orderSide     string
		orderQuantity int64
	)

	// Scoped to broker_name + account_id: broker_order_id alone has no
	// uniqueness guarantee at the schema level, so an unscoped lookup could
	// nondeterministically match another account's order on collision.
	//
	// GreekSoft's broker_order_id is only unique within a trading session,
	// not globally -- confirmed 2026-09-17: order ids like 120000003 and
	// 120000013 were reused the very next day for unrelated orders (even
	// with the opposite side), and without an ORDER BY this lookup could
	// return yesterday's stale row instead of today's real one, producing
	// a false "side mismatch" and leaving today's trade stuck in PARTIAL
	// with its autonomous SL/TP/TIME monitor never started. ORDER BY
	// created_at DESC picks the most recently placed matching order,
	// which is always the correct one to reconcile against.
	err = tx.QueryRowContext(ctx, `
		SELECT
			id,
			trade_id,
			contract_id,
			side,
			quantity
		FROM orders
		WHERE broker_order_id = $1
		  AND broker_name = $2
		  AND account_id = $3
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE
	`,
		fill.BrokerOrderID,
		brokerName,
		accountID,
	).Scan(
		&orderID,
		&tradeID,
		&contractID,
		&orderSide,
		&orderQuantity,
	)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("no local order for broker order %s", fill.BrokerOrderID)
	}
	if err != nil {
		return false, fmt.Errorf("load local order %s: %w", fill.BrokerOrderID, err)
	}

	if tradeID <= 0 {
		return false, fmt.Errorf("local order %d has no trade_id", orderID)
	}
	if contractID <= 0 {
		return false, fmt.Errorf("local order %d has no contract_id", orderID)
	}

	localSide := strings.ToUpper(strings.TrimSpace(orderSide))
	brokerSide := strings.ToUpper(strings.TrimSpace(fill.Side))
	if brokerSide != "" && brokerSide != localSide {
		return false, fmt.Errorf(
			"side mismatch for local order %d: local=%s broker=%s",
			orderID,
			localSide,
			brokerSide,
		)
	}

	rawFill, err := json.Marshal(fill)
	if err != nil {
		return false, fmt.Errorf("marshal broker fill: %w", err)
	}

	// Greeksoft reports aggregate/cumulative order-book fills per broker
	// order, not incremental deltas. The fill_id is keyed on broker+order
	// only (Model A: one row holding the latest cumulative snapshot) so a
	// later, larger cumulative report updates the existing row instead of
	// inserting a second row that would double-count in
	// recomputeTradeLegFromPersistedFills.
	fillID := fmt.Sprintf(
		"%s:%s",
		brokerName,
		fill.BrokerOrderID,
	)

	// Greeksoft BrokerTime is intentionally zero until its format is validated.
	// Persist the local reconciliation time and preserve broker data in JSON.
	fillTimestamp := time.Now().UTC()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO fills (
			order_id,
			trade_id,
			fill_id,
			fill_quantity,
			fill_price,
			fill_timestamp,
			raw_broker_fill
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
		ON CONFLICT (fill_id, order_id) DO UPDATE SET
			fill_quantity = EXCLUDED.fill_quantity,
			fill_price = EXCLUDED.fill_price,
			fill_timestamp = EXCLUDED.fill_timestamp,
			raw_broker_fill = EXCLUDED.raw_broker_fill
		WHERE fills.fill_quantity IS DISTINCT FROM EXCLUDED.fill_quantity
		   OR fills.fill_price IS DISTINCT FROM EXCLUDED.fill_price
	`,
		orderID,
		tradeID,
		fillID,
		fill.FilledQty,
		fill.AveragePrice,
		fillTimestamp,
		string(rawFill),
	)
	if err != nil {
		return false, fmt.Errorf("insert fill for order %d: %w", orderID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read fill insert result: %w", err)
	}

	if rowsAffected == 0 {
		// The cumulative snapshot is unchanged since the last reconciliation
		// pass; nothing new to propagate to orders/trade_legs.
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit existing fill transaction: %w", err)
		}
		return false, nil
	}

	newFilledQty := fill.FilledQty
	newPendingQty := orderQuantity - newFilledQty
	if newPendingQty < 0 {
		newPendingQty = 0
	}
	newStatus := "PARTIALLY_FILLED"
	if newFilledQty >= orderQuantity {
		newStatus = "FILLED"
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET
			status = $2,
			filled_qty = $3,
			pending_qty = $4,
			avg_fill_price = $5,
			average_price = $5,
			updated_at = NOW()
		WHERE id = $1
	`,
		orderID,
		newStatus,
		newFilledQty,
		newPendingQty,
		fill.AveragePrice,
	); err != nil {
		return false, fmt.Errorf("update local order %d fill state: %w", orderID, err)
	}

	if err := ensureTradeLeg(
		ctx,
		tx,
		tradeID,
		contractID,
		fill.FilledQty,
	); err != nil {
		return false, err
	}

	if err := recomputeTradeLegFromPersistedFills(
		ctx,
		tx,
		tradeID,
		contractID,
	); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit verified fill: %w", err)
	}

	return true, nil
}

func ensureTradeLeg(
	ctx context.Context,
	tx *sql.Tx,
	tradeID int64,
	contractID int64,
	targetQuantity int64,
) error {
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
		return fmt.Errorf(
			"load trade leg trade_id=%d contract_id=%d: %w",
			tradeID,
			contractID,
			err,
		)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO trade_legs (
			trade_id,
			contract_id,
			target_quantity,
			current_quantity,
			avg_entry_price,
			avg_exit_price,
			realized_pnl,
			status,
			created_at,
			updated_at
		)
		VALUES ($1,$2,$3,0,NULL,NULL,0,'OPEN',NOW(),NOW())
	`,
		tradeID,
		contractID,
		targetQuantity,
	)
	if err != nil {
		return fmt.Errorf(
			"create trade leg trade_id=%d contract_id=%d: %w",
			tradeID,
			contractID,
			err,
		)
	}

	return nil
}

func recomputeTradeLegFromPersistedFills(
	ctx context.Context,
	tx *sql.Tx,
	tradeID int64,
	contractID int64,
) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			o.side,
			f.fill_quantity,
			f.fill_price
		FROM fills f
		JOIN orders o ON o.id = f.order_id
		WHERE f.trade_id = $1
		  AND o.contract_id = $2
		ORDER BY f.fill_timestamp, f.id
	`,
		tradeID,
		contractID,
	)
	if err != nil {
		return fmt.Errorf(
			"load fills for trade leg trade_id=%d contract_id=%d: %w",
			tradeID,
			contractID,
			err,
		)
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

	buyAvg := 0.0
	sellAvg := 0.0

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
			// For a closed net-short leg, the BUY average is the exit price.
			// For a closed net-long leg, this is a simplification; later
			// execution-level matching can distinguish both directions.
			exitPrice = buyAvg
		}
		status = "CLOSED"
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE trade_legs
		SET
			target_quantity = $3,
			current_quantity = $4,
			avg_entry_price = $5,
			avg_exit_price = $6,
			realized_pnl = $7,
			status = $8,
			updated_at = NOW()
		WHERE trade_id = $1
		  AND contract_id = $2
	`,
		tradeID,
		contractID,
		maxInt64(buyQty, sellQty),
		netQty,
		entryPrice,
		exitPrice,
		realizedPnL,
		status,
	)
	if err != nil {
		return fmt.Errorf(
			"update trade leg trade_id=%d contract_id=%d: %w",
			tradeID,
			contractID,
			err,
		)
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
