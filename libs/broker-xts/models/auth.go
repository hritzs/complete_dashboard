package models

// LoginRequest represents the payload required for both Interactive and MarketData logins.
type LoginRequest struct {
	AppKey    string `json:"appKey"`
	SecretKey string `json:"secretKey"`
	Source    string `json:"source"`
}

// LoginResponse represents the API response structure for a successful login.
type LoginResponse struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Result      struct {
		Token            string `json:"token"`
		UserID           string `json:"userID"`
		IsInvestorClient bool   `json:"isInvestorClient,omitempty"`
	} `json:"result"`
}