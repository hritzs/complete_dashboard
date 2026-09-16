package greeksoft

import (
	"encoding/json"
	"strings"
)

// ApolloLoginRequest/Envelope/Data mirror IrisLoginRequest -- GreekSoft's
// Apollo (market-data) websocket uses the identical login handshake shape
// as Iris, confirmed against the official websocket docs.
type ApolloLoginRequest struct {
	Request ApolloLoginRequestEnvelope `json:"request"`
}

type ApolloLoginRequestEnvelope struct {
	Data           ApolloLoginData `json:"data"`
	ResponseFormat string          `json:"response_format"`
	RequestType    string          `json:"request_type"`
	StreamingType  string          `json:"streaming_type"`
}

type ApolloLoginData struct {
	GSCID      string `json:"gscid"`
	GCID       string `json:"gcid"`
	SessionID  string `json:"sessionId"`
	DeviceType string `json:"device_type"`
}

// ApolloEnvelope captures the common metadata used by Apollo websocket
// responses while retaining the broadcast-specific data as raw JSON,
// mirroring IrisEnvelope.
type ApolloEnvelope struct {
	Response ApolloResponse `json:"response"`
}

type ApolloResponse struct {
	SvcName       string          `json:"svcName"`
	ServerTime    json.RawMessage `json:"serverTime"`
	BCastTime     json.RawMessage `json:"BCastTime"` // broadcast frames (marketPicture) use this instead of serverTime
	StreamingType string          `json:"streaming_type"`
	Data          json.RawMessage `json:"data"`
}

// ApolloFrame is the normalized read-only event emitted by
// ApolloMarketDataClient.ReadLoop.
type ApolloFrame struct {
	StreamingType string
	ServiceName   string
	Raw           []byte
}

func classifyApolloFrame(raw []byte) ApolloFrame {
	frame := ApolloFrame{
		StreamingType: "UNKNOWN",
		Raw:           append([]byte(nil), raw...),
	}

	var envelope ApolloEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return frame
	}

	frame.StreamingType = strings.TrimSpace(envelope.Response.StreamingType)
	frame.ServiceName = strings.TrimSpace(envelope.Response.SvcName)

	if frame.StreamingType == "" {
		frame.StreamingType = "UNKNOWN"
	}

	return frame
}

// ApolloSubscribeSymbol identifies one instrument token to subscribe/
// unsubscribe for marketPicture updates.
type ApolloSubscribeSymbol struct {
	Symbol string `json:"symbol"`
}

type ApolloSubscribeRequest struct {
	Request ApolloSubscribeRequestEnvelope `json:"request"`
}

type ApolloSubscribeRequestEnvelope struct {
	Data           ApolloSubscribeData `json:"data"`
	ResponseFormat string              `json:"response_format"`
	Gscid          string              `json:"gscid"`
	Gcid           string              `json:"gcid"`
	RequestType    string              `json:"request_type"` // "subscribe" or "unsubscribe"
	StreamingType  string              `json:"streaming_type"`
}

type ApolloSubscribeData struct {
	Symbols []ApolloSubscribeSymbol `json:"symbols"`
}

// ApolloMarketPicture is a single symbol's broadcast tick for
// streaming_type "marketPicture". Field names and set confirmed 2026-09-15
// against a live capture (RELIANCE, token 101002885) via cmd/apolloprobe,
// not just the docs example -- the docs example omits several fields
// (exchange_token, segment, IndicativeImbalanceQty, LimitImbalanceQty,
// CasRefPrice, IndicativeClosePrice) that are present on the wire.
type ApolloMarketPicture struct {
	Symbol                 string            `json:"symbol"`
	ExchangeToken          string            `json:"exchange_token"`
	Segment                string            `json:"segment"`
	LTP                    json.Number       `json:"ltp"`
	ATP                    json.Number       `json:"atp"`
	Open                   json.Number       `json:"open"`
	High                   json.Number       `json:"high"`
	Low                    json.Number       `json:"low"`
	High52W                json.Number       `json:"h52w"`
	Low52W                 json.Number       `json:"l52w"`
	Close                  json.Number       `json:"close"`
	Change                 json.Number       `json:"change"`
	PercentChg             json.Number       `json:"p_change"`
	Bid                    json.Number       `json:"bid"`
	Ask                    json.Number       `json:"ask"`
	BidQty                 json.Number       `json:"bidqty"`
	AskQty                 json.Number       `json:"askqty"`
	TotalBuyQty            json.Number       `json:"tot_buyQty"`
	TotalSellQty           json.Number       `json:"tot_sellQty"`
	TotalBidQty            json.Number       `json:"tbq"`
	TotalAskQty            json.Number       `json:"taq"`
	TotalVolume            json.Number       `json:"tot_vol"`
	LastTradedQty          json.Number       `json:"ltq"`
	OI                     json.Number       `json:"oi"`
	IndicativeImbalanceQty json.Number       `json:"IndicativeImbalanceQty"`
	LimitImbalanceQty      json.Number       `json:"LimitImbalanceQty"`
	CasRefPrice            json.Number       `json:"CasRefPrice"`
	IndicativeClosePrice   json.Number       `json:"IndicativeClosePrice"`
	AssetType              string            `json:"asset_type"`
	Exchange               string            `json:"exch"`
	LastTradeTime          string            `json:"ltt"`
	LastUpdateTime         string            `json:"lut"`
	Name                   string            `json:"name"`
	Level2                 []ApolloLevel2Row `json:"level2"`
}

type ApolloLevel2Row struct {
	Bid ApolloLevel2Side `json:"bid"`
	Ask ApolloLevel2Side `json:"ask"`
}

type ApolloLevel2Side struct {
	Price json.Number `json:"price"`
	No    json.Number `json:"no"`
	Qty   json.Number `json:"qty"`
}

// ApolloOpenInterest is streaming_type "OpenInterest" -- confirmed live
// 2026-09-15 via cmd/apolloprobe, arrives per subscribed token alongside
// marketPicture.
type ApolloOpenInterest struct {
	MarketID  string      `json:"market_id"`
	GToken    string      `json:"gtoken"`
	CurrentOI json.Number `json:"currentOI"`
}

// ApolloMarketStatusFrame is streaming_type "MarketStatus" as pushed over
// the websocket (distinct from the REST getMarketStatus response, which
// uses integer fields for the same concepts) -- confirmed live 2026-09-15.
type ApolloMarketStatusFrame struct {
	MarketID string `json:"market_id"`
	Status   string `json:"status"`
	Session  string `json:"session"`
}

// ApolloIndexTick is streaming_type "index" -- an unsolicited broadcast
// (arrived without any explicit subscription in testing) carrying index
// (e.g. Nifty 50) level ticks. Confirmed live 2026-09-15; by far the
// highest-frequency frame type observed.
type ApolloIndexTick struct {
	Symbol          string      `json:"symbol"`
	Name            string      `json:"name"`
	IndexCode       string      `json:"indexCode"`
	Exchange        string      `json:"exch"`
	LTP             json.Number `json:"ltp"`
	Change          json.Number `json:"change"`
	PercentChg      json.Number `json:"p_change"`
	Open            json.Number `json:"open"`
	High            json.Number `json:"high"`
	Low             json.Number `json:"low"`
	Close           json.Number `json:"close"`
	High52W         json.Number `json:"h52w"`
	Low52W          json.Number `json:"l52w"`
	IndicativeClose json.Number `json:"indicativeclose"`
}
