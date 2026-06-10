package broker
package greeksoft

// Credentials holds the authentication details for the broker.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
// JLoginRequest wraps the outer request envelope for Greeksoft APIs
type JLoginRequest struct {
	Request JLoginRequestData `json:"request"`
}

// SessionTokenResponse matches the JSON from the /sessiontoken endpoint.
type SessionTokenResponse struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Result      struct {
		SessionToken     string `json:"sessionToken"`
		GCID             string `json:"gcid"`
		IsInvestorClient bool   `json:"isInvestorClient"`
	} `json:"result"`
type JLoginRequestData struct {
	Data     JLoginData `json:"data"`
	SvcName  string     `json:"svcName"`
	SvcGroup string     `json:"svcGroup"`
}

// OrderBookResponse matches the structure of the order book API call.
type OrderBookResponse struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Result      struct {
		OrderList []BrokerOrder `json:"orderList"`
	} `json:"result"`
type JLoginData struct {
	PanDob         string `json:"pan_dob"`
	DeviceID       string `json:"deviceId"`
	Gscid          string `json:"gscid"`
	DeviceDetails  string `json:"deviceDetails"`
	DeviceType     string `json:"deviceType"`
	Pass           string `json:"pass"` // MD5 hashed password
	TransPass      string `json:"transPass"`
	UserType       string `json:"userType"`
	BrokerID       string `json:"brokerid"` // Note: usually "1" for jloginNew
	PassType       string `json:"passType"`
	VersionNo      string `json:"version_no"`
	EncryptionType string `json:"encryptionType"`
}

// BrokerOrder represents a single order as returned by the broker's API.
// Field names are mapped from the Python code's usage.
type BrokerOrder struct {
	AppOrderID              string  `json:"AppOrderID"`
	OrderUniqueIdentifier   string  `json:"OrderUniqueIdentifier"`
	OrderStatus             string  `json:"OrderStatus"`
	OrderSide               string  `json:"OrderSide"`
	ExchangeInstrumentID    int64   `json:"ExchangeInstrumentID"`
	TradingSymbol           string  `json:"TradingSymbol"`
	OrderQuantity           int64   `json:"OrderQuantity"`
	CumulativeQuantity      int64   `json:"CumulativeQuantity"`
	OrderAverageTradedPrice float64 `json:"OrderAverageTradedPrice"`
	OrderRejectionReason    string  `json:"OrderRejectionReason"`
// JLoginResponse represents the incoming response from the broker
type JLoginResponse struct {
	Response struct {
		Data      JLoginResponseData `json:"data"`
		SessionID string             `json:"sessionId"`
	} `json:"response"`
}

// WsLoginRequest is the payload for authenticating a WebSocket connection.
type WsLoginRequest struct {
	T          string `json:"t"`
	UID        string `json:"uid"`
	ActID      string `json:"actid"`
	Source     string `json:"source"`
	Susertoken string `json:"susertoken"`
type JLoginResponseData struct {
	ErrorCode  int    `json:"ErrorCode"`
	ClientCode string `json:"ClientCode"` // This maps to the GCID
	IrisIP     string `json:"Iris_IP"`
	IrisPort   int    `json:"Iris_Port"`
	ApolloIP   string `json:"Apollo_IP"`
	ApolloPort int    `json:"Apollo_Port"`
}

type SessionDetails struct {
	GCID               string `json:"gcid"`
	WebsocketSessionID string `json:"websocket_session_id"`
	IrisEndpoint       string `json:"iris_endpoint"`
	ApolloEndpoint     string `json:"apollo_endpoint"`
}