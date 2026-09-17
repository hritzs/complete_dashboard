package greeksoft

import (
	"context"
	"database/sql"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"execution-gateway/internal/trading"
	gs "trading-platform/libs/broker-greeksoft"
	"trading-platform/libs/broker-greeksoft/ingest"
	"trading-platform/libs/broker-greeksoft/normalize"
	broker "trading-platform/libs/go-broker"
)

// OMSFeed maintains a real-time, in-memory view of GreekSoft order/fill
// state fed by the Iris push feed (order_status pushes and TradeResponse
// fills), replacing REST-order-book polling as the OMS's own
// fill-verification source. It implements trading.VerifiedFillsProvider,
// so it is a drop-in alternative to Executor.GetVerifiedFills
// (which polls GetOrderBook over REST).
//
// This is the OMS side of an explicit OMS/PMS split: OMSFeed is
// advisory/real-time-only and never persists anything -- it exists only
// to let a single execution call (e.g. PartialSquareOff's chunk loop)
// learn about a fill in microseconds instead of waiting out a REST-poll
// cycle. services/reconciler remains the sole durable, transactional
// (ACID) writer of canonical order/fill state to Postgres from the same
// Iris stream (the PMS side) -- that does not change here. If this
// process restarts, OMSFeed simply starts with an empty view, exactly
// like a fresh REST poll would only see the broker's current state; nothing
// durable is lost because nothing durable was ever kept here.
type OMSFeed struct {
	mu     sync.RWMutex
	orders map[string]omsOrderState // key: broker order ID (gorderid)

	// updates fires (best-effort, non-blocking) whenever any order's state
	// changes, so a caller can react to a push immediately instead of
	// waiting out a fixed poll delay. Not yet consumed anywhere -- exposed
	// for a future event-driven wait loop; the current integration keeps
	// waitForVerifiedFills's existing loop shape unchanged and simply
	// points it at this fast in-memory provider instead of REST, per the
	// approved migration plan's "smallest blast radius" phasing.
	updates chan string
}

type omsOrderState struct {
	Token        int64
	Side         string
	Status       string
	FilledQty    int64
	AveragePrice float64
	BrokerTime   time.Time
}

func NewOMSFeed() *OMSFeed {
	return &OMSFeed{
		orders:  make(map[string]omsOrderState),
		updates: make(chan string, 256),
	}
}

// Start begins consuming GreekSoft's Iris push feed in the background.
// Safe to call once per process; ctx cancellation stops it. Uses the same
// shared-session login as everything else in this platform (LoginShared
// via the caller), so it doesn't collide with the reconciler's or
// greeksoft-feed-bridge's own GreekSoft sessions.
func (f *OMSFeed) Start(ctx context.Context, client *gs.Client, db *sql.DB, accCfg *broker.AccountConfig) {
	go func() {
		err := client.ReadLoopWithReconnect(ctx, db, accCfg, func(frame gs.IrisFrame) {
			kind, orderPayload, tradePayload, dispatchErr := ingest.Dispatch(frame)
			if dispatchErr != nil {
				return
			}
			switch kind {
			case ingest.KindOrderResponse:
				if orderPayload != nil {
					f.applyOrder(*orderPayload)
				}
			case ingest.KindTradeResponse:
				if tradePayload != nil {
					f.applyTrade(*tradePayload)
				}
			}
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("[OMSFEED] Iris read loop exited unexpectedly: %v", err)
		}
	}()
}

func (f *OMSFeed) applyOrder(raw normalize.GreeksoftOrderResponse) {
	update := normalize.Parse(&raw)
	token := resolveGreeksoftShortToken(parseIntOrZeroLocal(raw.GToken))
	f.apply(update.BrokerOrderID, token, update.Side, string(update.Status), update.FilledQtyToday, update.Price, update.BrokerTimestamp)
}

func (f *OMSFeed) applyTrade(raw normalize.GreeksoftTradeResponse) {
	update := normalize.ParseTradeResponse(&raw)
	// TradeResponse carries no gtoken field of its own -- reuse whatever
	// token this order was already tracked under (populated by an
	// OrderResponse push, which GreekSoft always sends alongside/around a
	// TradeResponse per this session's live captures). If genuinely never
	// seen before, token stays 0 and this fill won't match any CE/PE
	// token filter until an OrderResponse arrives for it too.
	f.mu.RLock()
	existing, ok := f.orders[update.BrokerOrderID]
	f.mu.RUnlock()
	token := int64(0)
	if ok {
		token = existing.Token
	}
	f.apply(update.BrokerOrderID, token, update.Side, string(update.Status), update.FilledQtyToday, update.Price, update.BrokerTimestamp)
}

// apply records the latest known state for a broker order. FilledQty from
// GreekSoft's qty_filled_today is already cumulative (matches the REST
// order book's own filled_qty/traded_qty semantics), so it's stored
// directly, never summed.
//
// AveragePrice is kept as the most recent non-zero price seen for the
// order's current (or higher) filled quantity -- since a TradeResponse's
// exact traded_price and a same-increment OrderResponse's approximate
// price can arrive in either order, a later OrderResponse push carrying a
// stale/approximate price can overwrite a more precise TradeResponse
// price for the same fill. This is an accepted, documented imprecision:
// OMSFeed is advisory/real-time-only for pacing decisions, never the
// durable source of truth (that's the reconciler's Postgres writes).
func (f *OMSFeed) apply(brokerOrderID string, token int64, side, status string, filledQty int64, price float64, brokerTime time.Time) {
	if strings.TrimSpace(brokerOrderID) == "" {
		return
	}

	f.mu.Lock()
	existing := f.orders[brokerOrderID]

	next := existing
	next.Status = status
	if side != "" {
		next.Side = side
	}
	if token > 0 {
		next.Token = token
	}
	if filledQty >= existing.FilledQty {
		next.FilledQty = filledQty
		if price > 0 {
			next.AveragePrice = price
		}
	}
	if !brokerTime.IsZero() {
		next.BrokerTime = brokerTime
	}
	f.orders[brokerOrderID] = next
	f.mu.Unlock()

	select {
	case f.updates <- brokerOrderID:
	default:
	}
}

// GetVerifiedFills implements trading.VerifiedFillsProvider by reading
// this in-memory, Iris-fed map instead of calling GreekSoft's order-book
// REST endpoint. Only orders with a positive filled quantity and average
// price are returned, matching the REST-based provider's own filter
// (see greeksoftMapToVerifiedFill).
func (f *OMSFeed) GetVerifiedFills(ctx context.Context) ([]trading.BrokerFill, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	fills := make([]trading.BrokerFill, 0, len(f.orders))
	for brokerOrderID, st := range f.orders {
		if st.FilledQty <= 0 || st.AveragePrice <= 0 {
			continue
		}
		fills = append(fills, trading.BrokerFill{
			BrokerOrderID: brokerOrderID,
			Token:         st.Token,
			BrokerToken:   st.Token,
			Side:          st.Side,
			FilledQty:     st.FilledQty,
			AveragePrice:  st.AveragePrice,
			Status:        st.Status,
			BrokerTime:    st.BrokerTime,
			Verified:      true,
			Source:        "GREEKSOFT_IRIS",
		})
	}
	return fills, nil
}

// Updates returns a channel that receives a broker order ID every time
// that order's state changes via an Iris push. Best-effort: an update is
// dropped (never blocks the Iris read loop) if the channel is full.
func (f *OMSFeed) Updates() <-chan string {
	return f.updates
}

var _ trading.VerifiedFillsProvider = (*OMSFeed)(nil)

func parseIntOrZeroLocal(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
