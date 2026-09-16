// Package normalize turns GreekSoft's Iris OrderResponse push frames into
// the reconciler's canonical OrderUpdate/FillEvent shape.
//
// The previous version of this file modeled an XTS-shaped wire format
// (AppOrderID, LeavesQuantity, ...) that doesn't match GreekSoft's actual
// Iris push format at all -- confirmed against GreekSoft's own websocket
// documentation and a live capture. This rewrite targets the real
// OrderResponse shape.
package normalize

import (
	"strconv"
	"strings"
	"time"

	greeksoft "trading-platform/libs/broker-greeksoft"
)

// CanonicalOrderStatus represents our internal standard states.
type CanonicalOrderStatus string

const (
	StatusSubmitted CanonicalOrderStatus = "SUBMITTED"
	StatusAcked     CanonicalOrderStatus = "ACKED"
	StatusPartial   CanonicalOrderStatus = "PARTIAL_FILL"
	StatusFilled    CanonicalOrderStatus = "FILLED"
	StatusCancelled CanonicalOrderStatus = "CANCELLED"
	StatusRejected  CanonicalOrderStatus = "REJECTED"
)

// OrderUpdate is the canonical internal event derived from one
// OrderResponse frame.
type OrderUpdate struct {
	BrokerOrderID   string // GreekSoft's gorderid -- only unique per trading day
	ExchangeOrderID string // GreekSoft's eorderid -- likewise only unique per trading day
	Status          CanonicalOrderStatus
	FilledQtyToday  int64 // cumulative filled quantity for the order today, per qty_filled_today
	PendingQty      int64
	Price           float64 // the order's price field -- NOT necessarily the actual fill price, see FillEvent doc
	ReasonText      string
	BrokerTimestamp time.Time
	Side            string // canonical "BUY"/"SELL", from MapSide
	// FillID, when non-empty, is GreekSoft's own tradeid (from a
	// TradeResponse frame) and should be used as the persisted fill's
	// identity in preference to a synthesized one -- it's the broker's
	// actual unique trade identifier, not our approximation.
	FillID string
	Raw    GreeksoftOrderResponse
}

// FillEvent is derived when FilledQtyToday increases versus the
// previously persisted filled_qty for the order.
type FillEvent struct {
	BrokerOrderID string
	FillQtyDelta  int64
	// FillPrice is approximated from the OrderResponse's own `price`
	// field, since GreekSoft's push frame does not carry a distinct
	// per-fill execution price separate from the order's price. This is
	// accurate for a LIMIT order that fills at its own limit price, but
	// is only an approximation for a MARKET order that could fill away
	// from any price field on the frame. Flagged rather than presented as
	// exact -- confirm against getTradeDetail if precise fill prices ever
	// matter more than this approximation allows.
	FillPrice       float64
	BrokerTimestamp time.Time
}

// GreeksoftOrderResponse is the `data` payload of an Iris OrderResponse
// frame (streaming_type "OrderResponse"), confirmed against GreekSoft's
// official websocket documentation.
type GreeksoftOrderResponse struct {
	Side           string `json:"side"`
	Qty            string `json:"qty"`
	Product        string `json:"product"`
	GToken         string `json:"gtoken"`
	OrderStatus    string `json:"order_status"`
	EOrderID       string `json:"eorderid"`
	GOrderID       string `json:"gorderid"`
	LuTimeExchange string `json:"lu_time_exchange"`
	LuTime         string `json:"lu_time"` // epoch seconds, GreekSoft's own last-update time
	Symbol         string `json:"symbol"`
	RegularLot     string `json:"regular_lot"`
	Validity       string `json:"validity"`
	OrderType      string `json:"order_type"`
	Price          string `json:"price"`
	OrderState     string `json:"order_state"`
	TriggerPrice   string `json:"trigger_price"`
	DisclosedQty   string `json:"disclosed_qty"`
	Code           string `json:"code"`
	Reason         string `json:"reason"`
	PendingQty     string `json:"pending_qty"`
	QtyFilledToday string `json:"qty_filled_today"`
	CancelledBy    string `json:"cancelledBy"`
	TradeSymbol    string `json:"tradeSymbol"`
	Instrument     string `json:"instrument"`
	OptionType     string `json:"optionType"`
	StrikePrice    string `json:"strikePrice"`
}

// Parse normalizes a GreekSoft OrderResponse into the canonical OrderUpdate.
func Parse(raw *GreeksoftOrderResponse) OrderUpdate {
	return OrderUpdate{
		BrokerOrderID:   raw.GOrderID,
		ExchangeOrderID: raw.EOrderID,
		Status:          MapStatus(raw.OrderStatus),
		FilledQtyToday:  parseIntOrZero(raw.QtyFilledToday),
		PendingQty:      parseIntOrZero(raw.PendingQty),
		Price:           parseFloatOrZero(raw.Price),
		ReasonText:      firstNonEmpty(raw.Reason, raw.CancelledBy),
		BrokerTimestamp: parseEpochSecondsOrNow(raw.LuTime),
		Side:            MapSide(raw.Side),
		Raw:             *raw,
	}
}

// GreeksoftTradeResponse is the `data` payload of an Iris TradeResponse
// frame (streaming_type "TradeResponse"). Unlike OrderResponse, this frame
// is pushed only when a fill actually executes and carries the exact
// traded price/quantity for that fill (traded_price/traded_qty) plus
// GreekSoft's own trade id (tradeid) -- confirmed against a live capture
// on 2026-09-16, not against docs (the official docs review missed this
// frame type entirely).
type GreeksoftTradeResponse struct {
	Side           string `json:"side"`
	OrderStatus    string `json:"order_status"`
	EOrderID       string `json:"eorderid"`
	GOrderID       string `json:"gorderid"`
	LuTimeExchange string `json:"lu_time_exchange"`
	LuTime         string `json:"lu_time"`
	Symbol         string `json:"symbol"`
	RegularLot     string `json:"regular_lot"`
	Validity       string `json:"validity"`
	OrderType      string `json:"order_type"`
	Price          string `json:"price"`
	TriggerPrice   string `json:"trigger_price"`
	DisclosedQty   string `json:"disclosed_qty"`
	PendingQty     string `json:"pending_qty"`
	QtyFilledToday string `json:"qty_filled_today"`
	TradeSymbol    string `json:"tradeSymbol"`
	Instrument     string `json:"instrument"`
	OptionType     string `json:"optionType"`
	StrikePrice    string `json:"strikePrice"`
	TradeID        string `json:"tradeid"`
	TradedQty      string `json:"traded_qty"`
	TradedPrice    string `json:"traded_price"`
	Exchange       string `json:"exchange"`
}

// ParseTradeResponse normalizes a GreekSoft TradeResponse into the same
// canonical OrderUpdate shape Parse produces from an OrderResponse, so
// both converge on persistence.ApplyOrderUpdate's single idempotent write
// path. Price is taken from the frame's exact traded_price (falling back
// to its price field only if traded_price is absent/zero), which is more
// accurate than OrderResponse's approximation -- see FillEvent's doc
// comment on why that approximation existed in the first place.
func ParseTradeResponse(raw *GreeksoftTradeResponse) OrderUpdate {
	price := parseFloatOrZero(raw.TradedPrice)
	if price <= 0 {
		price = parseFloatOrZero(raw.Price)
	}
	return OrderUpdate{
		BrokerOrderID:   raw.GOrderID,
		ExchangeOrderID: raw.EOrderID,
		Status:          MapStatus(raw.OrderStatus),
		FilledQtyToday:  parseIntOrZero(raw.QtyFilledToday),
		PendingQty:      parseIntOrZero(raw.PendingQty),
		Price:           price,
		BrokerTimestamp: parseEpochSecondsOrNow(raw.LuTime),
		Side:            MapSide(raw.Side),
		FillID:          strings.TrimSpace(raw.TradeID),
	}
}

// ParseFromOrderBookEntry adapts a REST getOrderBookDetailWithLegV2 row
// (used by the recovery/catch-up path) into the same canonical OrderUpdate
// shape a live Iris OrderResponse frame produces, so both paths converge
// on ApplyOrderUpdate's idempotent write logic. Field meanings differ
// slightly from the websocket push (TrdQty here vs qty_filled_today
// there) but represent the same concepts.
func ParseFromOrderBookEntry(entry greeksoft.OrderBookEntry) OrderUpdate {
	exchangeOrderID := ""
	if entry.UniqueOrderID != "" && entry.UniqueOrderID != "0" {
		exchangeOrderID = entry.UniqueOrderID
	}

	return OrderUpdate{
		BrokerOrderID:   strconv.FormatInt(entry.OrdID, 10),
		ExchangeOrderID: exchangeOrderID,
		Status:          MapStatus(entry.Status),
		FilledQtyToday:  entry.TrdQty,
		PendingQty:      entry.PendingQty,
		Price:           entry.Price,
		ReasonText:      entry.Remarks,
		BrokerTimestamp: time.Unix(entry.OrdModTime, 0),
	}
}

// DeriveFill returns a FillEvent if update represents an increase in
// filled quantity versus previouslyFilledQty (the order's last-persisted
// filled_qty), or ok=false if there's no new fill to record.
func DeriveFill(update OrderUpdate, previouslyFilledQty int64) (fill FillEvent, ok bool) {
	delta := update.FilledQtyToday - previouslyFilledQty
	if delta <= 0 || update.Price <= 0 {
		return FillEvent{}, false
	}
	return FillEvent{
		BrokerOrderID:   update.BrokerOrderID,
		FillQtyDelta:    delta,
		FillPrice:       update.Price,
		BrokerTimestamp: update.BrokerTimestamp,
	}, true
}

// MapStatus translates GreekSoft's order_status strings into our
// canonical enum. Confirmed values (from live capture + official docs):
// "Pending", "Traded", "RMS Rejected", "Exchange Rejected". Others are
// mapped defensively by best-effort keyword match; anything unrecognized
// falls back to StatusSubmitted rather than being silently dropped.
func MapStatus(rawStatus string) CanonicalOrderStatus {
	s := strings.ToUpper(strings.TrimSpace(rawStatus))
	switch {
	case s == "PENDING" || s == "OPEN" || s == "NEW":
		return StatusAcked
	case s == "TRADED" || s == "FILLED" || s == "EXECUTED" || s == "COMPLETE":
		return StatusFilled
	case strings.Contains(s, "PARTIAL"):
		return StatusPartial
	case strings.Contains(s, "CANCEL"):
		return StatusCancelled
	case strings.Contains(s, "REJECT"):
		return StatusRejected
	default:
		return StatusSubmitted
	}
}

func parseIntOrZero(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseFloatOrZero(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func parseEpochSecondsOrNow(s string) time.Time {
	secs, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || secs <= 0 {
		return time.Now()
	}
	return time.Unix(secs, 0)
}

// MapSide translates GreekSoft's numeric side code ("1"=buy, "2"=sell,
// matching orders.go's PlaceOrder in libs/broker-greeksoft) into "BUY"/
// "SELL". Anything else returns "" rather than guessing.
func MapSide(rawSide string) string {
	switch strings.TrimSpace(rawSide) {
	case "1":
		return "BUY"
	case "2":
		return "SELL"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
