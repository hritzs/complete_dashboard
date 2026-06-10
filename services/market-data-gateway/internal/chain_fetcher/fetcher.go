package chain_fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// OptionChain defines the resolved tokens for the C++ decoder
type OptionChain struct {
	Symbol      string
	Expiry      string
	CashToken   int
	ExchangeSeg int
	Strikes     []StrikeRow
}

type StrikeRow struct {
	Strike  float64
	CEToken int
	PEToken int
}

type Config struct {
	Segment     int
	CashSegment int
	CashToken   int
	Gap         float64
	SeriesOpt   string
}

// Mapped exactly from your legacy SYMBOL_CONFIG in chain_provider.py
var SymbolConfigs = map[string]Config{
	"NIFTY":      {Segment: 2, CashSegment: 1, CashToken: 26000, Gap: 50, SeriesOpt: "OPTIDX"},
	"BANKNIFTY":  {Segment: 2, CashSegment: 1, CashToken: 26001, Gap: 100, SeriesOpt: "OPTIDX"},
	"FINNIFTY":   {Segment: 2, CashSegment: 1, CashToken: 26034, Gap: 50, SeriesOpt: "OPTIDX"},
	"MIDCPNIFTY": {Segment: 2, CashSegment: 1, CashToken: 26121, Gap: 25, SeriesOpt: "OPTIDX"},
	"SENSEX":     {Segment: 12, CashSegment: 11, CashToken: 26065, Gap: 100, SeriesOpt: "IO"},
}

type Client struct {
	BaseURL string
	Token   string
	UserID  string
	HTTP    *http.Client
}

func NewClient(baseURL, token, userID string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		UserID:  userID,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) get(endpoint string, params map[string]string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.BaseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", c.Token)

	q := req.URL.Query()
	q.Add("userID", c.UserID)
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// Response schemas matching XTS
type ExpiryResponse struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Result      []string `json:"result"`
}

type OptionSymbolResponse struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Result      []struct {
		// XTS sometimes sends strings, sometimes numbers. interface{} safely catches both.
		ExchangeInstrumentID interface{} `json:"ExchangeInstrumentID"`
	} `json:"result"`
}

func (c *Client) FetchExpiryDates(segment int, series, symbol string) ([]string, error) {
	params := map[string]string{
		"exchangeSegment": strconv.Itoa(segment),
		"series":          series,
		"symbol":          symbol,
	}
	body, err := c.get("/instruments/instrument/expiryDate", params)
	if err != nil {
		return nil, err
	}

	var res ExpiryResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("JSON parse error: %v | Body: %s", err, string(body))
	}

	if len(res.Result) == 0 {
		return nil, fmt.Errorf("XTS API returned no expiries or error: %s | Body: %s", res.Description, string(body))
	}
	return res.Result, nil
}

func (c *Client) FetchOptionToken(segment int, series, symbol, expiry, optType string, strike float64) (int, error) {
	params := map[string]string{
		"exchangeSegment": strconv.Itoa(segment),
		"series":          series,
		"symbol":          symbol,
		"expiryDate":      expiry,
		"optionType":      optType,
		"strikePrice":     fmt.Sprintf("%.0f", strike),
	}
	body, err := c.get("/instruments/instrument/optionSymbol", params)
	if err != nil {
		return 0, err
	}

	var res OptionSymbolResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, fmt.Errorf("JSON parse error: %v | Body: %s", err, string(body))
	}

	if len(res.Result) == 0 {
		return 0, fmt.Errorf("XTS API returned no token or error: %s | Body: %s", res.Description, string(body))
	}

	switch v := res.Result[0].ExchangeInstrumentID.(type) {
	case float64:
		return int(v), nil
	case string:
		return strconv.Atoi(v)
	default:
		return 0, fmt.Errorf("unexpected type for ExchangeInstrumentID")
	}
}

// BuildChain fetches tokens for a range around a given ATM strike
func (c *Client) BuildChain(symbol string, expiry string, atmStrike float64, strikeRange int) (*OptionChain, error) {
	cfg, ok := SymbolConfigs[symbol]
	if !ok {
		return nil, fmt.Errorf("unsupported symbol %s", symbol)
	}

	chain := &OptionChain{
		Symbol:      symbol,
		Expiry:      expiry,
		CashToken:   cfg.CashToken,
		ExchangeSeg: cfg.Segment,
		Strikes:     make([]StrikeRow, 0),
	}

	startStrike := atmStrike - (float64(strikeRange) * cfg.Gap)
	endStrike := atmStrike + (float64(strikeRange) * cfg.Gap)

	slog.Info("Building Option Chain", "symbol", symbol, "expiry", expiry, "startStrike", startStrike, "endStrike", endStrike)

	for s := startStrike; s <= endStrike; s += cfg.Gap {
		row := StrikeRow{Strike: s}

		// Fetch CE
		if ce, err := c.FetchOptionToken(cfg.Segment, cfg.SeriesOpt, symbol, expiry, "CE", s); err == nil {
			row.CEToken = ce
		} else {
			slog.Warn("Failed to fetch CE token", "symbol", symbol, "strike", s, "error", err)
		}

		// Fetch PE
		if pe, err := c.FetchOptionToken(cfg.Segment, cfg.SeriesOpt, symbol, expiry, "PE", s); err == nil {
			row.PEToken = pe
		} else {
			slog.Warn("Failed to fetch PE token", "symbol", symbol, "strike", s, "error", err)
		}

		chain.Strikes = append(chain.Strikes, row)
	}

	return chain, nil
}
