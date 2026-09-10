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
