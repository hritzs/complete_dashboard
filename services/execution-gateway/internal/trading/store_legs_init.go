package trading

import (
	"context"
	"database/sql"
	"fmt"
)

// InitStraddleLegs creates OPEN legs for CE and PE of a straddle trade.
// It is called once immediately after the trade record is created.
// Prices are left NULL and will be populated by fill reconciliation.
func (s *PostgresBackedStore) InitStraddleLegs(
	tradeUID string,
	ceContractID, peContractID int64,
	ceQty, peQty int64,
) error {
	ctx := context.Background()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for init straddle legs: %w", err)
	}
	defer tx.Rollback()

	tradeID, err := s.loadTradeIDForLegs(ctx, tx, tradeUID)
	if err != nil {
		return err
	}

	if err := ensureTradeLeg(ctx, tx, tradeID, ceContractID, ceQty); err != nil {
		return fmt.Errorf("ensure CE leg: %w", err)
	}

	if err := ensureTradeLeg(ctx, tx, tradeID, peContractID, peQty); err != nil {
		return fmt.Errorf("ensure PE leg: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit init straddle legs: %w", err)
	}

	return nil
}

func (s *PostgresBackedStore) loadTradeIDForLegs(
	ctx context.Context,
	tx *sql.Tx,
	tradeUID string,
) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM trades WHERE trade_uid = $1
	`, tradeUID).Scan(&id)

	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("trade not found: %s", tradeUID)
	}
	if err != nil {
		return 0, fmt.Errorf("load trade id: %w", err)
	}
	return id, nil
}

// ResolveContractIDByToken returns the contracts.id for a given broker token.
// Used to initialize trade legs before orders are placed.
func (s *PostgresBackedStore) ResolveContractIDByToken(token int64, exchange string) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id
		FROM contracts
		WHERE broker_token = $1
		  AND exchange = $2
	`, token, exchange).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("resolve contract for token %d: %w", token, err)
	}
	if !id.Valid || id.Int64 <= 0 {
		return 0, fmt.Errorf("contract not found for token %d", token)
	}
	return id.Int64, nil
}
