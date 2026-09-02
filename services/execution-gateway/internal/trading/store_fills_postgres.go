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
		orderID    int64
		tradeID    int64
		contractID int64
		orderSide  string
	)

	err = tx.QueryRowContext(ctx, `
		SELECT
			id,
			trade_id,
			contract_id,
			side
		FROM orders
		WHERE broker_order_id = $1
		  
		  
		FOR UPDATE
	`,
		fill.BrokerOrderID,
	).Scan(
		&orderID,
		&tradeID,
		&contractID,
		&orderSide,
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

	// Greeksoft currently reports aggregate order-book fills. This stable ID
	// makes repeated reconciliation idempotent for an unchanged aggregate.
	fillID := fmt.Sprintf(
		"%s:%s:%d:%.4f",
		brokerName,
		fill.BrokerOrderID,
		fill.FilledQty,
		fill.AveragePrice,
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
		ON CONFLICT (fill_id, order_id) DO NOTHING
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
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit existing fill transaction: %w", err)
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE orders
		SET
			status = 'FILLED',
			updated_at = NOW()
		WHERE id = $1
	`, orderID); err != nil {
		return false, fmt.Errorf("mark local order %d filled: %w", orderID, err)
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
