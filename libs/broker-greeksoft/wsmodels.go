package greeksoft

import (
	"encoding/json"
	"strings"
)

// IrisLoginRequest is the websocket authentication request sent after
// connecting to the interactive Iris websocket endpoint.
type IrisLoginRequest struct {
	Request IrisLoginRequestEnvelope `json:"request"`
}

type IrisLoginRequestEnvelope struct {
	Data           IrisLoginData `json:"data"`
	ResponseFormat string        `json:"response_format"`
	RequestType    string        `json:"request_type"`
	StreamingType  string        `json:"streaming_type"`
}

type IrisLoginData struct {
	GSCID      string `json:"gscid"`
	GCID       string `json:"gcid"`
	SessionID  string `json:"sessionId"`
	DeviceType string `json:"device_type"`
}

// IrisHeartbeatRequest is the frame GreekSoft's websocket docs require be
// sent every heartbeat_Intervals seconds (from getFlagValues) to keep an
// Iris (or Apollo) connection alive; a connection that stops heartbeating
// is expected to be dropped by the broker.
type IrisHeartbeatRequest struct {
	Request IrisHeartbeatRequestEnvelope `json:"request"`
}

type IrisHeartbeatRequestEnvelope struct {
	Data           IrisHeartbeatData `json:"data"`
	ResponseFormat string            `json:"response_format"`
	RequestType    string            `json:"request_type"`
	StreamingType  string            `json:"streaming_type"`
}

type IrisHeartbeatData struct {
	GCID      string `json:"gcid"`
	SessionID string `json:"sessionId"`
}

// Known streaming_type values, confirmed against GreekSoft's official
// websocket docs and/or wsclient_test.go's fixture. classifyIrisFrame
// itself does no filtering on these -- they exist so callers (and the
// reconciler's frame dispatcher) don't hardcode magic strings.
const (
	StreamingTypeLogin         = "login"
	StreamingTypeLoginResponse = "LoginResponse"
	StreamingTypeLicense       = "LicenseResponse"
	StreamingTypeHeartBeat     = "HeartBeat"
	StreamingTypeOrderResponse = "OrderResponse"
	// StreamingTypeTradeResponse is a distinct push frame GreekSoft sends
	// when a fill actually executes (order_status "Executed", carrying
	// tradeid/traded_qty/traded_price) -- confirmed live on 2026-09-16.
	// It is NOT just a re-send of OrderResponse: earlier docs review
	// missed it, so the reconciler was silently dropping every fill this
	// frame represents. See docs/greeksoft-integration-architecture.md.
	StreamingTypeTradeResponse = "TradeResponse"
	// The following Apollo (market-data) frame types were confirmed live
	// on 2026-09-15 via cmd/apolloprobe against account 147, not just
	// inferred from docs.
	StreamingTypeMarketPicture = "marketPicture"
	StreamingTypeOpenInterest  = "OpenInterest"
	StreamingTypeMarketStatus  = "MarketStatus"
	StreamingTypeIndex         = "index" // unsolicited broadcast, arrives without an explicit subscription
)

// IrisEnvelope captures the common metadata used by GreekSoft websocket
// responses while retaining the broker-specific data as raw JSON.
type IrisEnvelope struct {
	Response IrisResponse `json:"response"`
}

type IrisResponse struct {
	AppID         string          `json:"appID"`
	InfoID        string          `json:"infoID"`
	MsgID         string          `json:"msgID"`
	ServerTime    json.RawMessage `json:"serverTime"`
	StreamingType string          `json:"streaming_type"`
	SvcName       string          `json:"svcName"`
	ErrorCode     json.RawMessage `json:"ErrorCode"`
	Data          json.RawMessage `json:"data"`
}

// IrisFrame is the normalized read-only event emitted by the discovery
// client. Raw always contains the original broker websocket frame.
type IrisFrame struct {
	StreamingType string
	ServiceName   string
	Raw           []byte
}

func classifyIrisFrame(raw []byte) IrisFrame {
	frame := IrisFrame{
		StreamingType: "UNKNOWN",
		Raw:           append([]byte(nil), raw...),
	}

	var envelope IrisEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return frame
	}

	frame.StreamingType = strings.TrimSpace(
		envelope.Response.StreamingType,
	)
	frame.ServiceName = strings.TrimSpace(
		envelope.Response.SvcName,
	)

	if frame.StreamingType == "" {
		frame.StreamingType = "UNKNOWN"
	}

	return frame
}
