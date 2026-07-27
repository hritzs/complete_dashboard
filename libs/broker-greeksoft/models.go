package greeksoft

type sessionTokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	BrokerID int    `json:"brokerId,omitempty"`
	ValidFor string `json:"validFor"`
}

type sessionTokenResponse struct {
	ID           int    `json:"id"`
	SessionToken string `json:"sessionToken"`
	Message      string `json:"message"`
}

type greekEnvelope struct {
	Request greekRequestPayload `json:"request"`
}

type greekRequestPayload struct {
	Data           interface{} `json:"data"`
	SvcName        string      `json:"svcName,omitempty"`
	SvcGroup       string      `json:"svcGroup,omitempty"`
	SvcVersion     string      `json:"svcVersion,omitempty"`
	Gscid          string      `json:"gscid,omitempty"`
	ResponseFormat string      `json:"response_format,omitempty"`
	RequestType    string      `json:"request_type,omitempty"`
	StreamingType  string      `json:"streaming_type,omitempty"`
}

type jloginRequestData struct {
	PanDob         string `json:"pan_dob"`
	DeviceID       string `json:"deviceId"`
	Gscid          string `json:"gscid"`
	DeviceDetails  string `json:"deviceDetails"`
	DeviceType     string `json:"deviceType"`
	Password       string `json:"pass"`
	TransPass      string `json:"transPass"`
	UserType       string `json:"userType"`
	BrokerID       string `json:"brokerid"`
	PassType       string `json:"passType"`
	VersionNo      string `json:"version_no"`
	EncryptionType string `json:"encryptionType"`
}

type jloginResponse struct {
	Response jloginInnerResponse `json:"response"`
}

type jloginInnerResponse struct {
	ErrorCode  int                `json:"ErrorCode"`
	AppID      string             `json:"appID"`
	InfoID     string             `json:"infoID"`
	MsgID      string             `json:"msgID"`
	ServerTime int64              `json:"serverTime"`
	SessionID  string             `json:"sessionId"`
	SvcGroup   string             `json:"svcGroup"`
	SvcName    string             `json:"svcName"`
	SvcVersion string             `json:"svcVersion"`
	Data       jloginResponseData `json:"data"`
}

type jloginResponseData struct {
	ErrorCode           int    `json:"ErrorCode"`
	Message             string `json:"message"`
	ClientCode          int `json:"ClientCode"`
	Gscid               string `json:"gscid"`
	IrisIP              string `json:"Iris_IP"`
	IrisPort            int    `json:"Iris_Port"`
	ApolloIP            string `json:"Apollo_IP"`
	ApolloPort          int    `json:"Apollo_Port"`
	ArachneIP           string `json:"Arachne_IP"`
	ArachnePort         int    `json:"Arachne_Port"`
	BroadcastSenderPort int    `json:"BroadcastSender_Port"`
	OrderSenderPort     int    `json:"OrderSender_Port"`
}

type flagValuesResponse struct {
	Response flagValuesInnerResponse `json:"response"`
}

type flagValuesInnerResponse struct {
	ErrorCode  int            `json:"ErrorCode"`
	AppID      string         `json:"appID"`
	InfoID     string         `json:"infoID"`
	MsgID      string         `json:"msgID"`
	ServerTime int64          `json:"serverTime"`
	SessionID  string         `json:"sessionId"`
	SvcGroup   string         `json:"svcGroup"`
	SvcName    string         `json:"svcName"`
	SvcVersion string         `json:"svcVersion"`
	Data       flagValuesData `json:"data"`
}

type flagValuesData struct {
	IrisIP              string `json:"Iris_IP"`
	IrisPort            int    `json:"Iris_Port"`
	ApolloIP            string `json:"Apollo_IP"`
	ApolloPort          int    `json:"Apollo_Port"`
	ArachneIP           string `json:"Arachne_IP"`
	ArachnePort         int    `json:"Arachne_Port"`
	BroadcastSenderPort int    `json:"BroadcastSender_Port"`
	OrderSenderPort     int    `json:"OrderSender_Port"`
}

type newOrderRequestData struct {
	TriggerPrice  string `json:"trigger_price"`
	GToken        string `json:"gtoken"`
	Side          string `json:"side"`
	GCID          string `json:"gcid"`
	Validity      string `json:"validity"`
	Price         string `json:"price"`
	Exchange      string `json:"exchange"`
	DisclosedQty  string `json:"disclosed_qty"`
	TradeSymbol   string `json:"tradeSymbol"`
	Lot           string `json:"lot"`
	OrderType     string `json:"order_type"`
	Product       string `json:"product"`
	Qty           string `json:"qty"`
	COrderID      string `json:"corderid"`
	AMO           string `json:"amo"`
	IProCli       string `json:"iprocli"`
	GTDExpiry     int    `json:"gtdExpiry"`
	IsPostClosed  string `json:"is_post_closed"`
	IsPreOpen     string `json:"is_preopen_order"`
	IsSqOffOrder  string `json:"isSqOffOrder"`
	Offline       string `json:"offline"`
	IsRestAPI     string `json:"is_restapi"`
	StrategyName  string `json:"strategyName"`
	AccountNumber string `json:"AccountNumber,omitempty"`
	AlgoID        string `json:"algoId,omitempty"`
}

type newOrderResponse struct {
	Response newOrderInnerResponse `json:"response"`
	Message  string                `json:"message,omitempty"`
}

type newOrderInnerResponse struct {
	ErrorCode  int                    `json:"ErrorCode"`
	AppID      string                 `json:"appID"`
	InfoID     string                 `json:"infoID"`
	MsgID      string                 `json:"msgID"`
	ServerTime string                 `json:"serverTime"`
	SessionID  string                 `json:"sessionId"`
	SvcGroup   string                 `json:"svcGroup"`
	SvcName    string                 `json:"svcName"`
	SvcVersion string                 `json:"svcVersion"`
	Data       map[string]interface{} `json:"data"`
}
