package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
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
	return s.mem.LoadTrade(tradeUID)
}

func (s *PostgresBackedStore) AllTrades() []StoredTrade {
	memTrades := s.mem.AllTrades()
	if len(memTrades) > 0 {
		return memTrades
	}

	dbTrades := s.loadTradesFromDB()
	for _, tr := range dbTrades {
		s.mem.SaveTrade(tr)
	}

	return dbTrades
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

	raw, _ := json.Marshal(intent)

	limitPrice := sql.NullFloat64{}
	if intent.LimitPrice != nil {
		limitPrice.Valid = true
		limitPrice.Float64 = *intent.LimitPrice
	}

	orderUID := intent.OrderUID
	if orderUID == "" {
		orderUID = intent.IntentID
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO orders (
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
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW(),NOW())
		ON CONFLICT (intent_id) DO NOTHING
	`,
		intent.IntentID,
		orderUID,
		tradeUID,
		intent.BrokerName,
		intent.AccountID,
		intent.Side,
		intent.Quantity,
		intent.OrderType,
		limitPrice,
		"CREATED",
		string(raw),
	)

	if err != nil {
		log.Printf("[SQL STORE] insert order intent failed trade_uid=%s intent_id=%s err=%v", tradeUID, intent.IntentID, err)
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
