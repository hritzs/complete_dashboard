package greeksoft

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"execution-gateway/internal/trading"
)

// OMSFeed implements trading.VerifiedFillsProvider by reading the fills
// services/reconciler has already persisted to Postgres from GreekSoft's
// live Iris push feed -- i.e. it's a fast, Iris-sourced read path that
// never opens its own Iris connection.
//
// CONFIRMED LIVE INCIDENT (2026-09-17): an earlier version of this type
// opened its own separate Iris websocket subscription. GreekSoft allows
// only ONE live Iris connection per account (not just one HTTP session),
// so that connection and services/reconciler's existing one repeatedly
// disconnected each other, and this directly caused a real fill to be
// missed (reconciler's connection dropped at the exact moment an order's
// TradeResponse would have arrived). Required a manual DB correction
// afterward, confirmed against the broker's live NPRequest net position.
// This redesign avoids a second Iris connection entirely: it only ever
// reads Postgres, which reconciler already updates within milliseconds
// of an Iris push arriving -- the OMS/PMS split's OMS side reading the
// PMS side's own durable state, rather than trying to observe Iris
// independently.
//
// "Websocket as primary verifier, REST as fallback": Executor.GetVerifiedFills
// tries this (Postgres/Iris-sourced) path first; on any error here --
// including reconciler being down, since GetVerifiedFills also checks
// reconciler's own health endpoint -- it falls back to the existing
// REST order-book poll unchanged. A genuinely empty (but error-free)
// result (no fills yet) is NOT treated as a failure -- that's the normal
// "still pending" case the existing retry loop already handles by
// polling again.
type OMSFeed struct {
	db *sql.DB

	healthURL   string
	httpClient  *http.Client
	healthMu    sync.Mutex
	healthOK    bool
	healthAt    time.Time
	healthCache time.Duration
}

// NewOMSFeed builds a Postgres-backed verified-fills reader. healthURL is
// services/reconciler's health endpoint (e.g.
// "http://localhost:8021/api/health") -- used only as a cheap, cached
// liveness signal for the "websocket path" (reconciler is what's actually
// consuming Iris); if it's unreachable, GetVerifiedFills reports an error
// so the caller falls back to REST instead of silently returning "no
// fills yet" forever because nothing is writing new rows.
func NewOMSFeed(db *sql.DB, healthURL string) *OMSFeed {
	return &OMSFeed{
		db:          db,
		healthURL:   strings.TrimSpace(healthURL),
		httpClient:  &http.Client{Timeout: 2 * time.Second},
		healthCache: 3 * time.Second,
	}
}

// reconcilerHealthy checks (with a short cache, since GetVerifiedFills
// can be called every few hundred milliseconds by the existing retry
// loop) whether services/reconciler's health endpoint is reachable.
func (f *OMSFeed) reconcilerHealthy(ctx context.Context) bool {
	if f.healthURL == "" {
		return true // no health URL configured: skip the check, trust the DB path
	}

	f.healthMu.Lock()
	if time.Since(f.healthAt) < f.healthCache {
		ok := f.healthOK
		f.healthMu.Unlock()
		return ok
	}
	f.healthMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.healthURL, nil)
	ok := false
	if err == nil {
		resp, doErr := f.httpClient.Do(req)
		if doErr == nil {
			ok = resp.StatusCode == http.StatusOK
			_ = resp.Body.Close()
		}
	}

	f.healthMu.Lock()
	f.healthOK = ok
	f.healthAt = time.Now()
	f.healthMu.Unlock()

	return ok
}

// GetVerifiedFills implements trading.VerifiedFillsProvider by querying
// today's orders/fills/contracts (the same tables services/reconciler
// writes transactionally on every Iris push -- see
// services/reconciler/internal/persistence/store.go) instead of calling
// GreekSoft's order-book REST endpoint. One row per order with at least
// one fill, filled_qty summed and fill_price quantity-weighted, matching
// the REST-based provider's own cumulative-per-order semantics (see
// greeksoftMapToVerifiedFill).
func (f *OMSFeed) GetVerifiedFills(ctx context.Context) ([]trading.BrokerFill, error) {
	if f == nil || f.db == nil {
		return nil, fmt.Errorf("greeksoft OMSFeed: nil db")
	}
	if !f.reconcilerHealthy(ctx) {
		return nil, fmt.Errorf("greeksoft OMSFeed: reconciler health check failed at %s", f.healthURL)
	}

	rows, err := f.db.QueryContext(ctx, `
		SELECT
			o.broker_order_id,
			o.side,
			c.broker_token,
			o.status,
			COALESCE(SUM(fl.fill_quantity), 0) AS filled_qty,
			CASE WHEN COALESCE(SUM(fl.fill_quantity), 0) > 0
			     THEN SUM(fl.fill_quantity * fl.fill_price) / SUM(fl.fill_quantity)
			     ELSE 0 END AS avg_price,
			MAX(fl.fill_timestamp) AS last_fill_time
		FROM orders o
		JOIN contracts c ON c.id = o.contract_id
		JOIN fills fl ON fl.order_id = o.id
		WHERE o.created_at::date = CURRENT_DATE
		GROUP BY o.id, o.broker_order_id, o.side, c.broker_token, o.status
		HAVING COALESCE(SUM(fl.fill_quantity), 0) > 0
	`)
	if err != nil {
		return nil, fmt.Errorf("greeksoft OMSFeed: query verified fills: %w", err)
	}
	defer rows.Close()

	var fills []trading.BrokerFill
	for rows.Next() {
		var (
			brokerOrderID string
			side          string
			token         int64
			status        string
			filledQty     int64
			avgPrice      float64
			lastFillTime  sql.NullTime
		)
		if err := rows.Scan(&brokerOrderID, &side, &token, &status, &filledQty, &avgPrice, &lastFillTime); err != nil {
			return nil, fmt.Errorf("greeksoft OMSFeed: scan verified fill row: %w", err)
		}

		fill := trading.BrokerFill{
			BrokerOrderID: brokerOrderID,
			Token:         token,
			BrokerToken:   token,
			Side:          strings.ToUpper(strings.TrimSpace(side)),
			FilledQty:     filledQty,
			AveragePrice:  avgPrice,
			Status:        status,
			Verified:      true,
			Source:        "GREEKSOFT_RECONCILER_DB",
		}
		if lastFillTime.Valid {
			fill.BrokerTime = lastFillTime.Time
		}
		fills = append(fills, fill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("greeksoft OMSFeed: iterate verified fill rows: %w", err)
	}

	return fills, nil
}

var _ trading.VerifiedFillsProvider = (*OMSFeed)(nil)
