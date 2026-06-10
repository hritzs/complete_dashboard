package broker

// Config represents the common configuration needed to instantiate a broker client.
type Config struct {
	BrokerName string
	BaseURL    string
	AppKey     string
	SecretKey  string
	Source     string
}
