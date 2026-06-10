package broker

// AccountConfig holds the generic credentials and parameters for connecting to any broker.
type AccountConfig struct {
	Name       string `json:"name" yaml:"name"`
	BrokerType string `json:"broker_type" yaml:"broker_type"`
	APIKey     string `json:"api_key" yaml:"api_key"`
	APISecret  string `json:"api_secret" yaml:"api_secret"`
	ClientID   string `json:"client_id" yaml:"client_id"`
	Source     string `json:"source" yaml:"source"`       // Used by XTS
	BrokerID   int    `json:"broker_id" yaml:"broker_id"` // Used by Greeksoft
	PanDob     string `json:"pan_dob" yaml:"pan_dob"`     // Used by Greeksoft
}

// SessionDetails represents a standardized authentication session across all brokers.
type SessionDetails struct {
	UserID         string
	AuthToken      string
	IsLoggedIn     bool
	BrokerSpecific map[string]interface{} // Holds broker-specific metadata like Greeksoft's gcid or session_id
}

// OrderIntent represents a standardized internal request to place an order.
type OrderIntent struct {
	TradeUID        string  `json:"trade_uid"`
	IntentID        string  `json:"intent_id,omitempty"` // XTS Compatibility
	InstrumentToken int     `json:"instrument_token"`    // XTS expects int
	ExchangeSegment string  `json:"exchange_segment"`
	Side            string  `json:"side"`
	Quantity        int     `json:"quantity"`
	OrderType       string  `json:"order_type"`
	ProductType     string  `json:"product_type"`
	TimeInForce     string  `json:"time_in_force"` // Required by XTS
	ClientID        string  `json:"client_id"`     // Required by XTS for PRO accounts
	LimitPrice      float64 `json:"limit_price"`
	StopPrice       float64 `json:"stop_price"`
	DisclosedQty    int     `json:"disclosed_quantity"` // Required by XTS
}

// OrderResponse represents a standardized response from placing or modifying an order.
type OrderResponse struct {
	OrderID       string `json:"order_id"`
	BrokerOrderID string `json:"broker_order_id,omitempty"` // XTS Compatibility
	TradeUID      string `json:"trade_uid"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	Error         string `json:"error"`
	RawResponse   string `json:"raw_response,omitempty"` // XTS Compatibility
}
