package trading

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TradeSummary is one trade with what actually executed and its realized PnL.
type TradeSummary struct {
	TradeUID    string     `json:"trade_uid"`
	Symbol      string     `json:"symbol"`
	Expiry      string     `json:"expiry"`
	Strike      float64    `json:"strike"`
	Lots        int        `json:"lots"`
	LotSize     int        `json:"lot_size"`
	BrokerName  string     `json:"broker_name"`
	AccountID   string     `json:"account_id"`
	ProductType string     `json:"product_type"`
	Status      string     `json:"status"`
	CloseReason string     `json:"close_reason,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`

	// RealizedPnL is gross of brokerage/charges, from broker-confirmed order
	// fills (average-cost, matched quantity).
	RealizedPnL float64 `json:"realized_pnl"`

	CE         *LegPnL          `json:"ce,omitempty"`
	PE         *LegPnL          `json:"pe,omitempty"`
	OpenCEQty  int64            `json:"open_ce_qty"`
	OpenPEQty  int64            `json:"open_pe_qty"`
	Executions []OrderExecution `json:"executions"`
}

const executionsSelect = `
	SELECT o.trade_uid, o.id, o.created_at, o.intent_id, COALESCE(o.phase, ''),
	       o.side, o.quantity, o.filled_qty,
	       COALESCE(NULLIF(o.avg_fill_price, 0), NULLIF(o.average_price, 0),
	                (SELECT f.fill_price FROM fills f WHERE f.order_id = o.id ORDER BY f.id DESC LIMIT 1), 0),
	       o.status, COALESCE(o.broker_order_id, ''),
	       COALESCE(c.option_type, ''), COALESCE(c.strike_price, 0)
	FROM orders o
	LEFT JOIN contracts c ON c.id = o.contract_id
`

func scanExecutions(rows *sql.Rows) (map[string][]OrderExecution, error) {
	out := map[string][]OrderExecution{}
	for rows.Next() {
		var (
			uid string
			e   OrderExecution
			px  float64
		)
		if err := rows.Scan(&uid, &e.OrderID, &e.Time, &e.intentID, &e.phase, &e.Side, &e.Quantity,
			&e.FilledQty, &px, &e.Status, &e.BrokerOrderID, &e.Leg, &e.Strike); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		e.AvgPrice = px
		e.Kind = classifyExecutionKind(e.phase, e.intentID)
		out[uid] = append(out[uid], e)
	}
	return out, rows.Err()
}

// TradeRealizedPnL returns a trade's realized PnL from its orders.
func (s *PostgresBackedStore) TradeRealizedPnL(ctx context.Context, tradeUID string) (float64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("postgres store is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, executionsSelect+` WHERE o.trade_uid = $1 ORDER BY o.created_at, o.id`, tradeUID)
	if err != nil {
		return 0, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()
	byTrade, err := scanExecutions(rows)
	if err != nil {
		return 0, err
	}
	return totalRealizedPnL(computeLegPnL(byTrade[tradeUID])), nil
}

// TradeSummaries returns every trade created in [from, to) with its
// executions and realized PnL, newest first.
func (s *PostgresBackedStore) TradeSummaries(ctx context.Context, from, to time.Time) ([]TradeSummary, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store is unavailable")
	}

	orderRows, err := s.db.QueryContext(ctx, executionsSelect+`
		WHERE o.trade_uid IN (SELECT trade_uid FROM trades WHERE created_at >= $1 AND created_at < $2)
		ORDER BY o.created_at, o.id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query executions: %w", err)
	}
	execs, err := scanExecutions(orderRows)
	orderRows.Close()
	if err != nil {
		return nil, err
	}

	tradeRows, err := s.db.QueryContext(ctx, `
		SELECT trade_uid, COALESCE(user_id,''), COALESCE(broker_name,''), COALESCE(account_id,''),
		       symbol, status, created_at, closed_at, COALESCE(config, '{}'::jsonb)
		FROM trades
		WHERE created_at >= $1 AND created_at < $2
		ORDER BY created_at DESC`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query trades: %w", err)
	}
	defer tradeRows.Close()

	out := []TradeSummary{}
	for tradeRows.Next() {
		var (
			uid, user, broker, account, symbol, status string
			createdAt                                  time.Time
			closedAt                                   sql.NullTime
			raw                                        []byte
		)
		if err := tradeRows.Scan(&uid, &user, &broker, &account, &symbol, &status, &createdAt, &closedAt, &raw); err != nil {
			return nil, fmt.Errorf("scan trade: %w", err)
		}

		var stored StoredTrade
		_ = json.Unmarshal(raw, &stored)

		list := execs[uid]
		legs := computeLegPnL(list)
		sum := TradeSummary{
			TradeUID: uid, Symbol: symbol, Expiry: stored.Expiry, Strike: stored.Strike,
			Lots: stored.Lots, LotSize: stored.LotSize,
			BrokerName: broker, AccountID: account, ProductType: stored.ProductType,
			Status: status, CloseReason: closeReasonForStatus(status), CreatedAt: createdAt,
			RealizedPnL: totalRealizedPnL(legs), CE: legs["CE"], PE: legs["PE"],
			Executions: list,
		}
		if sum.Executions == nil {
			sum.Executions = []OrderExecution{}
		}
		if sum.CE != nil {
			sum.OpenCEQty = sum.CE.NetShortQty
		}
		if sum.PE != nil {
			sum.OpenPEQty = sum.PE.NetShortQty
		}

		// closed_at: the column is only populated for trades closed since it
		// started being written; older rows fall back to the stored JSON, then
		// to the time of the last exit order.
		if closeReasonForStatus(status) != "" {
			switch {
			case closedAt.Valid:
				t := closedAt.Time
				sum.ClosedAt = &t
			case stored.ClosedAt.Year() > 2000:
				t := stored.ClosedAt
				sum.ClosedAt = &t
			default:
				for i := len(list) - 1; i >= 0; i-- {
					if list[i].Kind == "EXIT" && list[i].FilledQty > 0 {
						t := list[i].Time
						sum.ClosedAt = &t
						break
					}
				}
			}
		}
		out = append(out, sum)
	}
	return out, tradeRows.Err()
}

// openShortQuantities is sold minus bought (filled quantity) per option leg,
// never negative. Unlike the PnL it does not need a price: any filled order
// changes the position.
func openShortQuantities(execs []OrderExecution) (ce, pe int64) {
	net := map[string]int64{}
	for _, e := range execs {
		if e.FilledQty <= 0 {
			continue
		}
		switch strings.ToUpper(e.Side) {
		case "SELL":
			net[e.Leg] += e.FilledQty
		case "BUY":
			net[e.Leg] -= e.FilledQty
		}
	}
	return maxInt64(net["CE"], 0), maxInt64(net["PE"], 0)
}

// TradeOpenQuantities returns the short CE and PE quantity a trade really has
// open, from its orders' filled quantities.
func (s *PostgresBackedStore) TradeOpenQuantities(ctx context.Context, tradeUID string) (int64, int64, error) {
	if s == nil || s.db == nil {
		return 0, 0, fmt.Errorf("postgres store is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, executionsSelect+` WHERE o.trade_uid = $1 ORDER BY o.created_at, o.id`, tradeUID)
	if err != nil {
		return 0, 0, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()
	byTrade, err := scanExecutions(rows)
	if err != nil {
		return 0, 0, err
	}
	ce, pe := openShortQuantities(byTrade[tradeUID])
	return ce, pe, nil
}
