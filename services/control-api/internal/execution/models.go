package execution

type DeployStraddleRequest struct {
	UserID        string `json:"userId"`
	BrokerName    string `json:"brokerName"`
	AccountID     string `json:"accountId"`
	Symbol        string `json:"symbol"`
	Lots          int    `json:"lots"`
	DeltaNeutral  bool   `json:"deltaNeutral"`
	ProductType   string `json:"productType"`
	TargetExpiry  string `json:"targetExpiry"`
	Strike        int    `json:"strike"`
	CEToken       int64  `json:"ceToken"`
	PEToken       int64  `json:"peToken"`
	LotSize       int    `json:"lotSize"`
	CEStrikePrice int    `json:"ceStrikePrice"`
	PEStrikePrice int    `json:"peStrikePrice"`
}

type DeployStraddleResponse struct {
	Success   bool    `json:"success"`
	TradeUID  string  `json:"tradeUid,omitempty"`
	Status    string  `json:"status,omitempty"`
	Message   string  `json:"message,omitempty"`
	Symbol    string  `json:"symbol,omitempty"`
	Expiry    string  `json:"expiry,omitempty"`
	Strike    float64 `json:"strike,omitempty"`
	CEToken   int64   `json:"ceToken,omitempty"`
	PEToken   int64   `json:"peToken,omitempty"`
	CEQty     int     `json:"ceQuantity,omitempty"`
	PEQty     int     `json:"peQuantity,omitempty"`
	CELtp     float64 `json:"ceEntryPrice,omitempty"`
	PELtp     float64 `json:"peEntryPrice,omitempty"`
	NetDelta  float64 `json:"netDelta,omitempty"`
	Error     string  `json:"error,omitempty"`
	CreatedAt string  `json:"createdAt,omitempty"`
}