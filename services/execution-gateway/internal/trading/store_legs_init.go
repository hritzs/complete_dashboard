package trading

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RecomputeTradeLegs re-derives every trade_legs row of tradeUID from its
// persisted fills (one count per order, see perOrderFillsSQL). Run at
// startup before a trade is resumed, so legs written under the old
// raw-sum logic (which double-counted fills) are corrected before the
// runtime reads them.
func (s *PostgresBackedStore) RecomputeTradeLegs(ctx context.Context, tradeUID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recompute legs: %w", err)
	}
	defer tx.Rollback()

	tradeID, err := s.loadTradeIDForLegs(ctx, tx, strings.TrimSpace(tradeUID))
	if err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `SELECT contract_id FROM trade_legs WHERE trade_id = $1`, tradeID)
	if err != nil {
		return fmt.Errorf("list legs trade_id=%d: %w", tradeID, err)
	}
	var contracts []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan leg contract: %w", err)
		}
		contracts = append(contracts, id)
	}
	rows.Close()

	for _, contractID := range contracts {
		if err := recomputeTradeLegFromPersistedFills(ctx, tx, tradeID, contractID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// OpenLeg is one currently-OPEN trade_legs row: a real, broker-confirmed
// position on a specific token, independent of whichever two tokens the
// trade's own StoredTrade.CEToken/PEToken happen to point at. Qty is
// SIGNED per trade_legs.current_quantity's own convention (negative =
// net short, positive = net long, see recomputeTradeLegFromPersistedFills).
type OpenLeg struct {
	Token      int64
	Exchange   string
	Qty        int64
	OptionType string  // "CE" or "PE", from contracts.option_type
	EntryPrice float64 // trade_legs.avg_entry_price
	// WingQty is the part of Qty that came from WING-phase orders (signed,
	// normally >= 0). Qty-WingQty is the real (straddle/hedge) position;
	// only that part counts for delta/PnL/exits.
	WingQty int64
}

// wingQtyCTE nets this trade's WING-phase fills per contract ($1 =
// trade_uid), with the same per-order dedupe as perOrderFillsSQL (a
// gateway cumulative row and reconciler deltas for one order never
// double count: the larger wins).
const wingQtyCTE = `
	WITH w_raw AS (
		SELECT o.id, o.contract_id, o.side, o.created_at,
		       COALESCE(SUM(f.fill_quantity) FILTER (WHERE NOT gw.is_gw), 0) AS r_qty,
		       COALESCE(MAX(f.fill_quantity) FILTER (WHERE gw.is_gw), 0) AS g_qty
		FROM orders o
		JOIN fills f ON f.order_id = o.id
		CROSS JOIN LATERAL (
			SELECT COALESCE(o.broker_name, '') <> ''
			   AND UPPER(f.fill_id) LIKE UPPER(o.broker_name) || ':%' AS is_gw
		) gw
		WHERE o.trade_uid = $1 AND o.phase = 'WING'
		GROUP BY o.id, o.contract_id, o.side, o.created_at
	), w AS (
		SELECT contract_id,
		       SUM(CASE WHEN UPPER(side) = 'BUY' THEN GREATEST(r_qty, g_qty) ELSE -GREATEST(r_qty, g_qty) END)::bigint AS qty,
		       MAX(created_at) FILTER (WHERE UPPER(side) = 'BUY') AS last_buy_at
		FROM w_raw
		GROUP BY contract_id
	)
`

// LoadWingHoldings returns every wing contract of this trade with a
// non-zero net quantity, read from the DB (survives restarts).
func (s *PostgresBackedStore) LoadWingHoldings(tradeUID string) ([]wingHolding, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, wingQtyCTE+`
		SELECT c.broker_token, c.exchange, COALESCE(c.option_type, ''),
		       COALESCE(c.strike_price, 0)::float8, w.qty,
		       COALESCE(w.last_buy_at, 'epoch'::timestamptz)
		FROM w
		JOIN contracts c ON c.id = w.contract_id
		WHERE w.qty <> 0
	`, strings.TrimSpace(tradeUID))
	if err != nil {
		return nil, fmt.Errorf("load wing holdings trade_uid=%s: %w", tradeUID, err)
	}
	defer rows.Close()

	var out []wingHolding
	for rows.Next() {
		var h wingHolding
		if err := rows.Scan(&h.Token, &h.Exchange, &h.OptionType, &h.Strike, &h.Qty, &h.LastBuyAt); err != nil {
			return nil, fmt.Errorf("scan wing holding trade_uid=%s: %w", tradeUID, err)
		}
		h.OptionType = strings.ToUpper(strings.TrimSpace(h.OptionType))
		out = append(out, h)
	}
	return out, rows.Err()
}

// LoadOpenLegs returns every currently-OPEN leg recorded for this trade in
// trade_legs -- the durable, per-token source of truth that every real
// order (build, hedge, square-off, regardless of which token it traded)
// already updates via persistVerifiedFill/ensureTradeLeg/
// recomputeTradeLegFromPersistedFills. This is what makes it possible to
// find and close a leg that lives on a DIFFERENT token than the trade's
// own CEToken/PEToken -- e.g. a hedge placed at the live ATM strike once
// spot has moved off the trade's original strike (confirmed live
// 2026-09-25: a hedge on a neighboring strike was left open after
// SquareOff, because SquareOff only ever closed tr.CEQty/tr.PEQty on
// tr.CEToken/tr.PEToken and had no way to know this leg existed).
func (s *PostgresBackedStore) LoadOpenLegs(tradeUID string) ([]OpenLeg, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, wingQtyCTE+`
		SELECT c.broker_token, c.exchange, tl.current_quantity,
		       COALESCE(c.option_type, ''), COALESCE(tl.avg_entry_price, 0),
		       COALESCE(w.qty, 0)
		FROM trade_legs tl
		JOIN trades t ON t.id = tl.trade_id
		JOIN contracts c ON c.id = tl.contract_id
		LEFT JOIN w ON w.contract_id = tl.contract_id
		WHERE t.trade_uid = $1
		  AND tl.status = 'OPEN'
		  AND tl.current_quantity <> 0
	`, strings.TrimSpace(tradeUID))
	if err != nil {
		return nil, fmt.Errorf("load open legs trade_uid=%s: %w", tradeUID, err)
	}
	defer rows.Close()

	var legs []OpenLeg
	for rows.Next() {
		var leg OpenLeg
		if err := rows.Scan(&leg.Token, &leg.Exchange, &leg.Qty, &leg.OptionType, &leg.EntryPrice, &leg.WingQty); err != nil {
			return nil, fmt.Errorf("scan open leg trade_uid=%s: %w", tradeUID, err)
		}
		legs = append(legs, leg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open legs trade_uid=%s: %w", tradeUID, err)
	}

	return legs, nil
}

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
