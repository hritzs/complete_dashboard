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

type PostgresBackedStore struct {
	mem *MemoryStore
	db  *sql.DB
}

func NewPostgresBackedStore(db *sql.DB) *PostgresBackedStore {
	return &PostgresBackedStore{
		mem: NewMemoryStore(),
		db:  db,
	}
}

func (s *PostgresBackedStore) SaveTrade(tr StoredTrade) {
	s.mem.SaveTrade(tr)
	s.upsertTrade(tr)
}

func (s *PostgresBackedStore) UpdateTrade(tr StoredTrade) {
	s.mem.UpdateTrade(tr)
	s.upsertTrade(tr)
}

func (s *PostgresBackedStore) LoadTrade(tradeUID string) (StoredTrade, bool) {
	if s == nil || s.db == nil {
		return StoredTrade{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var (
		dbTradeUID    string
		dbUserID      string
		dbBrokerName  string
		dbAccountID   string
		dbSymbol      string
		dbStatus      string
		rawConfig     []byte
		dbRealizedPNL float64
		dbCreatedAt   time.Time
		dbClosedAt    sql.NullTime
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT
			trade_uid,
			COALESCE(user_id, ''),
			COALESCE(broker_name, ''),
			COALESCE(account_id, ''),
			symbol,
			status,
			COALESCE(config, '{}'::jsonb),
			COALESCE(realized_pnl, 0),
			created_at,
			closed_at
		FROM trades
		WHERE trade_uid = $1
	`, strings.TrimSpace(tradeUID)).Scan(
		&dbTradeUID,
		&dbUserID,
		&dbBrokerName,
		&dbAccountID,
		&dbSymbol,
		&dbStatus,
		&rawConfig,
		&dbRealizedPNL,
		&dbCreatedAt,
		&dbClosedAt,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[SQL STORE] load trade failed trade_uid=%s err=%v", tradeUID, err)
		}
		return StoredTrade{}, false
	}

	var tr StoredTrade
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &tr); err != nil {
			log.Printf(
				"[SQL STORE] decode trade config failed trade_uid=%s err=%v",
				dbTradeUID,
				err,
			)
		}
	}

	// Relational columns are authoritative over JSON config.
	tr.TradeUID = dbTradeUID
	tr.UserID = dbUserID
	tr.BrokerName = dbBrokerName
	tr.AccountID = dbAccountID
	tr.Symbol = dbSymbol
	tr.Status = dbStatus
	tr.RealizedPnL = dbRealizedPNL
	tr.CreatedAt = dbCreatedAt

	if dbClosedAt.Valid {
		tr.ClosedAt = dbClosedAt.Time
	}

	// trade_legs is the durable execution source of truth. The legacy
	// StoredTrade fields CELtp/PELtp are used by the runtime monitor as
	// entry prices, but the JSON config can retain pre-trade snapshot or
	// limit prices. Hydrate them from broker-fill-reconciled leg state.
	legRows, err := s.db.QueryContext(ctx, `
		SELECT
			c.option_type,
			tl.current_quantity,
			tl.avg_entry_price
		FROM trade_legs tl
		JOIN trades t
			ON t.id = tl.trade_id
		JOIN contracts c
			ON c.id = tl.contract_id
		WHERE t.trade_uid = $1
		  AND tl.status = 'OPEN'
		  AND tl.current_quantity <> 0
		  AND tl.avg_entry_price IS NOT NULL
		  AND tl.avg_entry_price > 0
	`, dbTradeUID)
	if err != nil {
		log.Printf("[SQL STORE] load trade legs failed trade_uid=%s err=%v", dbTradeUID, err)
		return tr, true
	}
	defer legRows.Close()

	for legRows.Next() {
		var (
			optionType string
			quantity   int64
			entryPrice float64
		)

		if err := legRows.Scan(&optionType, &quantity, &entryPrice); err != nil {
			log.Printf("[SQL STORE] scan trade leg failed trade_uid=%s err=%v", dbTradeUID, err)
			continue
		}

		switch strings.ToUpper(strings.TrimSpace(optionType)) {
		case "CE":
			tr.CELtp = entryPrice
		case "PE":
			tr.PELtp = entryPrice
		}
	}

	if err := legRows.Err(); err != nil {
		log.Printf("[SQL STORE] iterate trade legs failed trade_uid=%s err=%v", dbTradeUID, err)
	}

	return tr, true
}

func (s *PostgresBackedStore) AllTrades() []StoredTrade {
	if s == nil || s.db == nil {
		return nil
	}

	// PostgreSQL is authoritative. Memory is reserved for transient
	// runtime/snapshot state and must never override durable trade state.
	return s.loadTradesFromDB()
}

func (s *PostgresBackedStore) AppendIntent(tradeUID string, intent OrderIntent) {
	s.mem.AppendIntent(tradeUID, intent)
	s.insertOrderIntent(tradeUID, intent)
}

func (s *PostgresBackedStore) LoadIntents(tradeUID string) []StoredIntent {
	return s.mem.LoadIntents(tradeUID)
}

func (s *PostgresBackedStore) SaveSnapshot(snapshot TradeSnapshot) {
	s.mem.SaveSnapshot(snapshot)
}

func (s *PostgresBackedStore) LoadSnapshot(tradeUID string) (TradeSnapshot, bool) {
	return s.mem.LoadSnapshot(tradeUID)
}

func (s *PostgresBackedStore) SaveRuntime(rt *RuntimeTrade) {
	s.mem.SaveRuntime(rt)
}

func (s *PostgresBackedStore) LoadRuntime(tradeUID string) (*RuntimeTrade, bool) {
	return s.mem.LoadRuntime(tradeUID)
}

func (s *PostgresBackedStore) DeleteRuntime(tradeUID string) {
	s.mem.DeleteRuntime(tradeUID)
}

func (s *PostgresBackedStore) upsertTrade(tr StoredTrade) {
	if s == nil || s.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	raw, _ := json.Marshal(tr)

	createdAt := tr.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO trades (
			trade_uid,
			user_id,
			broker_name,
			account_id,
			symbol,
			status,
			config,
			created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (trade_uid) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			broker_name = EXCLUDED.broker_name,
			account_id = EXCLUDED.account_id,
			symbol = EXCLUDED.symbol,
			status = EXCLUDED.status,
			config = EXCLUDED.config
	`,
		tr.TradeUID,
		tr.UserID,
		tr.BrokerName,
		tr.AccountID,
		tr.Symbol,
		tr.Status,
		string(raw),
		createdAt,
	)

	if err != nil {
		log.Printf("[SQL STORE] upsert trade failed trade_uid=%s err=%v", tr.TradeUID, err)
	}
}

func (s *PostgresBackedStore) insertOrderIntent(tradeUID string, intent OrderIntent) {
	if s == nil || s.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tradeUID = strings.TrimSpace(tradeUID)
	if tradeUID == "" {
		log.Printf("[SQL STORE] refusing order insert: empty trade_uid intent_id=%s", intent.IntentID)
		return
	}

	exchangeSegment := strings.TrimSpace(intent.ExchangeSegment)
	brokerName := strings.TrimSpace(intent.BrokerName)
	if exchangeSegment == "" || brokerName == "" {
		log.Printf(
			"[SQL STORE] refusing order insert: missing exchange_segment or broker_name trade_uid=%s intent_id=%s exchange_segment=%q broker_name=%q",
			tradeUID,
			intent.IntentID,
			exchangeSegment,
			brokerName,
		)
		return
	}

	var tradeID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM trades
		WHERE trade_uid = $1
	`, tradeUID).Scan(&tradeID); err != nil {
		log.Printf(
			"[SQL STORE] refusing order insert: cannot resolve trade_id trade_uid=%s intent_id=%s err=%v",
			tradeUID,
			intent.IntentID,
			err,
		)
		return
	}
	var contractID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM contracts
		WHERE broker_token = $1
		AND exchange = $2
	`,
		intent.Token,
		exchangeSegment,
	).Scan(&contractID)

	if err != nil {
		log.Printf(
			"[SQL STORE] refusing order insert: cannot resolve contract_id trade_uid=%s intent_id=%s token=%d broker=%s err=%v",
			tradeUID,
			intent.IntentID,
			intent.Token,
			exchangeSegment,
			err,
		)
		return
	}

	if !contractID.Valid || contractID.Int64 <= 0 {
		log.Printf(
			"[SQL STORE] refusing order insert: contract not found trade_uid=%s intent_id=%s token=%d broker=%s",
			tradeUID,
			intent.IntentID,
			intent.Token,
			exchangeSegment,
		)
		return
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		log.Printf(
			"[SQL STORE] refusing order insert: marshal intent failed trade_uid=%s intent_id=%s err=%v",
			tradeUID,
			intent.IntentID,
			err,
		)
		return
	}

	limitPrice := sql.NullFloat64{}
	if intent.LimitPrice != nil {
		limitPrice.Valid = true
		limitPrice.Float64 = *intent.LimitPrice
	}

	orderUID := strings.TrimSpace(intent.OrderUID)
	if orderUID == "" {
		orderUID = intent.IntentID
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO orders (
			trade_id,
			contract_id,
			intent_id,
			order_uid,
			trade_uid,
			broker_name,
			account_id,
			side,
			quantity,
			order_type,
			limit_price,
			status,
			raw_broker_request,
			created_at,
			updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NOW(),NOW())
		ON CONFLICT (intent_id) DO NOTHING
	`,
		tradeID,
		contractID.Int64, // <-- new
		intent.IntentID,
		orderUID,
		tradeUID,
		brokerName,
		intent.AccountID,
		intent.Side,
		intent.Quantity,
		intent.OrderType,
		limitPrice,
		"CREATED",
		string(raw),
	)

	if err != nil {
		log.Printf(
			"[SQL STORE] insert order intent failed trade_uid=%s trade_id=%d intent_id=%s err=%v",
			tradeUID,
			tradeID,
			intent.IntentID,
			err,
		)
	}
}

func (s *PostgresBackedStore) MarkOrderSubmitted(intentID string, brokerOrderID string, status string, rawResponse string) {
	if s == nil || s.db == nil {
		return
	}

	if status == "" {
		status = "SUBMITTED"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var orderID int64
	err := s.db.QueryRowContext(ctx, `
		UPDATE orders
		SET broker_order_id = $2,
		    status = $3,
		    updated_at = NOW()
		WHERE intent_id = $1
		RETURNING id
	`, intentID, brokerOrderID, status).Scan(&orderID)

	if err != nil {
		log.Printf("[SQL STORE] mark order submitted failed intent_id=%s broker_order_id=%s err=%v", intentID, brokerOrderID, err)
		return
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO order_events (
			order_id,
			status,
			reason_text,
			event_timestamp,
			raw_broker_response
		)
		VALUES ($1,$2,$3,NOW(),$4)
	`, orderID, status, "BROKER_RESPONSE", rawResponse)

	if err != nil {
		log.Printf("[SQL STORE] insert order event failed order_id=%d err=%v", orderID, err)
	}
}

// MarkOrderExecution stores the broker execution result and creates a
// durable verified fill and trade leg when execution details are valid.
func (s *PostgresBackedStore) MarkOrderExecution(
	intentID string,
	brokerOrderID string,
	status string,
	filledQty int64,
	pendingQty int64,
	fillPrice float64,
	rawResponse string,
) {
	if s == nil || s.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	intentID = strings.TrimSpace(intentID)
	brokerOrderID = strings.TrimSpace(brokerOrderID)
	status = strings.ToUpper(strings.TrimSpace(status))

	if status == "" {
		status = "SUBMITTED"
	}

	if pendingQty < 0 {
		pendingQty = 0
	}

	var orderID int64
	var tradeID int64
	var contractID sql.NullInt64
	var orderSide string
	var brokerName string
	var accountID string

	err := s.db.QueryRowContext(ctx, `
		SELECT
			id,
			trade_id,
			contract_id,
			side,
			COALESCE(broker_name, ''),
			COALESCE(account_id, '')
		FROM orders
		WHERE intent_id = $1
	`, intentID).Scan(
		&orderID,
		&tradeID,
		&contractID,
		&orderSide,
		&brokerName,
		&accountID,
	)

	if err == sql.ErrNoRows {
		log.Printf(
			"[SQL STORE] execution update skipped: order not found intent_id=%s broker_order_id=%s",
			intentID,
			brokerOrderID,
		)
		return
	}

	if err != nil {
		log.Printf(
			"[SQL STORE] execution lookup failed intent_id=%s broker_order_id=%s err=%v",
			intentID,
			brokerOrderID,
			err,
		)
		return
	}

	if tradeID <= 0 {
		log.Printf(
			"[SQL STORE] execution update refused: order_id=%d has invalid trade_id=%d",
			orderID,
			tradeID,
		)
		return
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE orders
		SET
			broker_order_id = $2,
			status = $3,
			filled_qty = $4,
			pending_qty = $5,
			avg_fill_price = $6,
			average_price = $6,
			updated_at = NOW()
		WHERE id = $1
	`,
		orderID,
		brokerOrderID,
		status,
		filledQty,
		pendingQty,
		fillPrice,
	)

	if err != nil {
		log.Printf(
			"[SQL STORE] execution update failed order_id=%d broker_order_id=%s err=%v",
			orderID,
			brokerOrderID,
			err,
		)
		return
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO order_events (
			order_id,
			status,
			reason_text,
			event_timestamp,
			raw_broker_response
		)
		VALUES ($1,$2,$3,NOW(),$4::jsonb)
	`,
		orderID,
		status,
		"BROKER_EXECUTION",
		rawResponse,
	)

	if err != nil {
		log.Printf(
			"[SQL STORE] execution event insert failed order_id=%d err=%v",
			orderID,
			err,
		)
	}

	if status != "FILLED" || filledQty <= 0 || fillPrice <= 0 {
		return
	}

	if !contractID.Valid || contractID.Int64 <= 0 {
		log.Printf(
			"[SQL STORE] verified fill not persisted: order_id=%d has no contract_id",
			orderID,
		)
		return
	}
	rawFill := rawResponse
	if strings.TrimSpace(rawFill) == "" {
		rawFill = "{}"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[SQL STORE] begin execution fill transaction failed order_id=%d err=%v", orderID, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	fillID := fmt.Sprintf(
		"%s:%s:%d:%.4f",
		brokerName,
		brokerOrderID,
		filledQty,
		fillPrice,
	)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO fills (
			order_id,
			trade_id,
			fill_id,
			fill_quantity,
			fill_price,
			fill_timestamp,
			raw_broker_fill
		)
		VALUES ($1,$2,$3,$4,$5,NOW(),$6::jsonb)
		ON CONFLICT (fill_id, order_id) DO NOTHING
	`,
		orderID,
		tradeID,
		fillID,
		filledQty,
		fillPrice,
		rawFill,
	)

	if err != nil {
		log.Printf("[SQL STORE] execution fill insert failed order_id=%d err=%v", orderID, err)
		return
	}

	if err := ensureTradeLeg(ctx, tx, tradeID, contractID.Int64, filledQty); err != nil {
		log.Printf("[SQL STORE] ensure trade leg failed trade_id=%d contract_id=%d err=%v",
			tradeID, contractID.Int64, err)
		return
	}

	if err := recomputeTradeLegFromPersistedFills(
		ctx,
		tx,
		tradeID,
		contractID.Int64,
	); err != nil {
		log.Printf("[SQL STORE] recompute trade leg failed trade_id=%d contract_id=%d err=%v",
			tradeID, contractID.Int64, err)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[SQL STORE] commit execution fill failed order_id=%d err=%v", orderID, err)
		return
	}
}

func (s *PostgresBackedStore) loadTradesFromDB() []StoredTrade {
	if s == nil || s.db == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			trade_uid,
			COALESCE(user_id, ''),
			COALESCE(broker_name, ''),
			COALESCE(account_id, ''),
			symbol,
			status,
			created_at
		FROM trades
		ORDER BY created_at DESC
		LIMIT 200
	`)
	if err != nil {
		log.Printf("[SQL STORE] load trades failed err=%v", err)
		return nil
	}
	defer rows.Close()

	out := []StoredTrade{}
	for rows.Next() {
		var tr StoredTrade
		if err := rows.Scan(
			&tr.TradeUID,
			&tr.UserID,
			&tr.BrokerName,
			&tr.AccountID,
			&tr.Symbol,
			&tr.Status,
			&tr.CreatedAt,
		); err != nil {
			log.Printf("[SQL STORE] scan trade failed err=%v", err)
			continue
		}
		out = append(out, tr)
	}

	return out
}

// RuntimeEligibility is the durable database-backed state required before an
// active trade may be resumed by the runtime monitor after a restart.
type RuntimeEligibility struct {
	TradeUID string
	CEQty    int64
	PEQty    int64
	CEEntry  float64
	PEEntry  float64
}

// ValidateTradeForRuntime verifies that an ACTIVE trade has exactly one
// reconciled, open CE leg and one reconciled, open PE leg. It reads only
// durable PostgreSQL state: trades, trade_legs, contracts, orders, and fills.
func (s *PostgresBackedStore) ValidateTradeForRuntime(
	ctx context.Context,
	tradeUID string,
) (RuntimeEligibility, error) {
	var result RuntimeEligibility

	if s == nil || s.db == nil {
		return result, fmt.Errorf("postgres store is unavailable")
	}

	tradeUID = strings.TrimSpace(tradeUID)
	if tradeUID == "" {
		return result, fmt.Errorf("trade_uid is required")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			c.option_type,
			tl.current_quantity,
			tl.avg_entry_price,
			COUNT(DISTINCT f.id) AS fill_count,
			COUNT(DISTINCT o.id)
				FILTER (WHERE o.status = 'FILLED') AS filled_order_count
		FROM trades t
		JOIN trade_legs tl
			ON tl.trade_id = t.id
		JOIN contracts c
			ON c.id = tl.contract_id
		LEFT JOIN orders o
			ON o.trade_id = t.id
		   AND o.contract_id = tl.contract_id
		LEFT JOIN fills f
			ON f.order_id = o.id
		WHERE t.trade_uid = $1
		  AND t.status = 'ACTIVE'
		  AND tl.status = 'OPEN'
		GROUP BY
			c.option_type,
			tl.current_quantity,
			tl.avg_entry_price
		ORDER BY c.option_type
	`, tradeUID)
	if err != nil {
		return result, fmt.Errorf("query runtime eligibility: %w", err)
	}
	defer rows.Close()

	type legState struct {
		qty          int64
		entry        float64
		fillCount    int64
		filledOrders int64
	}

	legs := map[string]legState{}

	for rows.Next() {
		var (
			optionType   string
			quantity     int64
			entryPrice   sql.NullFloat64
			fillCount    int64
			filledOrders int64
		)

		if err := rows.Scan(
			&optionType,
			&quantity,
			&entryPrice,
			&fillCount,
			&filledOrders,
		); err != nil {
			return result, fmt.Errorf("scan runtime leg: %w", err)
		}

		optionType = strings.ToUpper(strings.TrimSpace(optionType))
		if optionType != "CE" && optionType != "PE" {
			continue
		}

		if !entryPrice.Valid || entryPrice.Float64 <= 0 {
			return result, fmt.Errorf(
				"%s leg has no valid persisted entry price",
				optionType,
			)
		}

		if quantity == 0 {
			return result, fmt.Errorf(
				"%s leg has zero persisted quantity",
				optionType,
			)
		}

		if fillCount <= 0 {
			return result, fmt.Errorf(
				"%s leg has no persisted broker fill",
				optionType,
			)
		}

		if filledOrders <= 0 {
			return result, fmt.Errorf(
				"%s leg has no linked FILLED order",
				optionType,
			)
		}

		legs[optionType] = legState{
			qty:          quantity,
			entry:        entryPrice.Float64,
			fillCount:    fillCount,
			filledOrders: filledOrders,
		}
	}

	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate runtime legs: %w", err)
	}

	ce, ceOK := legs["CE"]
	pe, peOK := legs["PE"]

	if !ceOK || !peOK || len(legs) != 2 {
		return result, fmt.Errorf(
			"expected exactly one reconciled CE and PE open leg; found CE=%t PE=%t total=%d",
			ceOK,
			peOK,
			len(legs),
		)
	}

	if absInt64(ce.qty) != absInt64(pe.qty) {
		return result, fmt.Errorf(
			"CE/PE quantity mismatch: ce=%d pe=%d",
			ce.qty,
			pe.qty,
		)
	}

	if (ce.qty < 0) != (pe.qty < 0) {
		return result, fmt.Errorf(
			"CE/PE direction mismatch: ce=%d pe=%d",
			ce.qty,
			pe.qty,
		)
	}

	return RuntimeEligibility{
		TradeUID: tradeUID,
		CEQty:    ce.qty,
		PEQty:    pe.qty,
		CEEntry:  ce.entry,
		PEEntry:  pe.entry,
	}, nil
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ActivePortfolioTrade is the DB-authoritative UI representation of one
// reconciled active straddle. Entry quantities/prices come only from
// persisted legs/fills; live LTP is added later by the market-data overlay.
type ActivePortfolioTrade struct {
	TradeUID     string    `json:"trade_uid"`
	UserID       string    `json:"user_id"`
	BrokerName   string    `json:"broker_name"`
	AccountID    string    `json:"account_id"`
	Symbol       string    `json:"symbol"`
	Status       string    `json:"status"`
	Expiry       string    `json:"expiry"`
	Strike       float64   `json:"strike"`
	CEToken      int64     `json:"ce_token"`
	PEToken      int64     `json:"pe_token"`
	CEQuantity   int64     `json:"ce_quantity"`
	PEQuantity   int64     `json:"pe_quantity"`
	CEEntryPrice float64   `json:"ce_entry_price"`
	PEEntryPrice float64   `json:"pe_entry_price"`
	RealizedPnL  float64   `json:"realized_pnl"`
	CreatedAt    time.Time `json:"created_at"`
	DataSource   string    `json:"data_source"`
}

// LoadActivePortfolio returns only reconciled ACTIVE trades having exactly
// one open CE leg and one open PE leg with nonzero quantity and entry price.
func (s *PostgresBackedStore) LoadActivePortfolio(
	ctx context.Context,
) ([]ActivePortfolioTrade, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store is unavailable")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			t.trade_uid,
			COALESCE(t.user_id, ''),
			COALESCE(t.broker_name, ''),
			COALESCE(t.account_id, ''),
			t.symbol,
			t.status,
			COALESCE(t.realized_pnl, 0),
			t.created_at,

			MAX(c.expiry_date) AS expiry_date,
			MAX(c.strike_price) AS strike_price,

			MAX(c.broker_token)
				FILTER (WHERE c.option_type = 'CE') AS ce_token,
			MAX(c.broker_token)
				FILTER (WHERE c.option_type = 'PE') AS pe_token,

			MAX(tl.current_quantity)
				FILTER (WHERE c.option_type = 'CE') AS ce_quantity,
			MAX(tl.current_quantity)
				FILTER (WHERE c.option_type = 'PE') AS pe_quantity,

			MAX(tl.avg_entry_price)
				FILTER (WHERE c.option_type = 'CE') AS ce_entry_price,
			MAX(tl.avg_entry_price)
				FILTER (WHERE c.option_type = 'PE') AS pe_entry_price

		FROM trades t
		JOIN trade_legs tl
			ON tl.trade_id = t.id
		   AND tl.status = 'OPEN'
		JOIN contracts c
			ON c.id = tl.contract_id
		WHERE t.status = 'ACTIVE'
		  AND tl.current_quantity <> 0
		  AND tl.avg_entry_price IS NOT NULL
		  AND tl.avg_entry_price > 0
		GROUP BY
			t.trade_uid,
			t.user_id,
			t.broker_name,
			t.account_id,
			t.symbol,
			t.status,
			t.realized_pnl,
			t.created_at
		HAVING
			COUNT(*) FILTER (WHERE c.option_type = 'CE') = 1
			AND COUNT(*) FILTER (WHERE c.option_type = 'PE') = 1
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query active portfolio: %w", err)
	}
	defer rows.Close()

	out := make([]ActivePortfolioTrade, 0)

	for rows.Next() {
		var (
			item       ActivePortfolioTrade
			expiryDate time.Time
		)

		if err := rows.Scan(
			&item.TradeUID,
			&item.UserID,
			&item.BrokerName,
			&item.AccountID,
			&item.Symbol,
			&item.Status,
			&item.RealizedPnL,
			&item.CreatedAt,
			&expiryDate,
			&item.Strike,
			&item.CEToken,
			&item.PEToken,
			&item.CEQuantity,
			&item.PEQuantity,
			&item.CEEntryPrice,
			&item.PEEntryPrice,
		); err != nil {
			return nil, fmt.Errorf("scan active portfolio: %w", err)
		}

		item.Expiry = expiryDate.Format("02-JAN-06")
		item.DataSource = "postgres:trades+trade_legs+fills"

		out = append(out, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active portfolio: %w", err)
	}

	return out, nil
}
