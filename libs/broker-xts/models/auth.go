package models

type LoginRequest struct {
	AppKey    string `json:"appKey"`
	SecretKey string `json:"secretKey"`
	Source    string `json:"source"`
}

type LoginResponse struct {
	Type   string `json:"type"`
	Result struct {
		Token string `json:"token"`
	} `json:"result"`
}
