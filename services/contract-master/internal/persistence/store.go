package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Contract struct {
	BrokerToken    int64
	Exchange       string
	Symbol         string
	InstrumentType string
	ExpiryDate     time.Time
	StrikePrice    float64
	OptionType     string
	LotSize        int
	TickSize       float64
	RawDetails     []byte
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) UpsertContracts(ctx context.Context, contracts []Contract) error {
	if len(contracts) == 0 {
		return nil
	}

	query := `
    INSERT INTO contracts (
        broker_token,
        exchange,
        symbol,
        instrument_type,
        expiry_date,
        strike_price,
        option_type,
        lot_size,
        tick_size,
        raw_details,
        updated_at
    )
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
    ON CONFLICT (broker_token, exchange)
    DO UPDATE SET
        symbol = EXCLUDED.symbol,
        instrument_type = EXCLUDED.instrument_type,
        expiry_date = EXCLUDED.expiry_date,
        strike_price = EXCLUDED.strike_price,
        option_type = EXCLUDED.option_type,
        lot_size = EXCLUDED.lot_size,
        tick_size = EXCLUDED.tick_size,
        raw_details = EXCLUDED.raw_details,
        updated_at = NOW()
    `

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range contracts {
		_, err := stmt.ExecContext(
			ctx,
			c.BrokerToken,
			strings.ToUpper(strings.TrimSpace(c.Exchange)),
			strings.ToUpper(strings.TrimSpace(c.Symbol)),
			strings.ToUpper(strings.TrimSpace(c.InstrumentType)),
			c.ExpiryDate,
			c.StrikePrice,
			strings.ToUpper(strings.TrimSpace(c.OptionType)),
			c.LotSize,
			c.TickSize,
			c.RawDetails,
		)
		if err != nil {
			return fmt.Errorf("upsert contract %s %s %s failed: %w",
				c.Exchange,
				c.Symbol,
				c.ExpiryDate.Format("2006-01-02"),
				err,
			)
		}
	}

	return tx.Commit()
}

func (s *Store) GetLotSize(ctx context.Context, symbol string, expiry string) (int, error) {
	exp, err := parseExpiry(expiry)
	if err != nil {
		return 0, err
	}

	query := `
    SELECT lot_size
    FROM contracts
    WHERE UPPER(symbol) = UPPER($1)
      AND expiry_date::date = $2::date
    LIMIT 1
    `

	var lotSize int
	err = s.db.QueryRowContext(ctx, query, strings.TrimSpace(symbol), exp.Format("2006-01-02")).Scan(&lotSize)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("lot size not found for %s %s", strings.ToUpper(strings.TrimSpace(symbol)), exp.Format("2006-01-02"))
		}
		return 0, err
	}

	return lotSize, nil
}

func parseExpiry(v string) (time.Time, error) {
	layouts := []string{
		"2006-01-02",
		"2006-01-02T15:04:05",
		time.RFC3339,
		"02-JAN-06",
		"02-JAN-2006",
		"02-Jan-06",
		"02-Jan-2006",
		"2-Jan-2006",
		"2-JAN-2006",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
			return t, nil
		}
		if t, err := time.Parse(layout, strings.ToUpper(strings.TrimSpace(v))); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid expiry: %s", v)
}