package chain_fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	
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
	HTTP    *http.Client
}

func NewClient(contractMasterURL string) *Client {
	return &Client{
		BaseURL: contractMasterURL,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) get(endpoint string, params map[string]string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.BaseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
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

// Response from contract-master
type TokenResponse struct {
	Success    bool    `json:"success"`
	Symbol     string  `json:"symbol"`
	Expiry     string  `json:"expiry"`
	OptionType string  `json:"option_type"`
	Strike     float64 `json:"strike"`
	Token      int64   `json:"token"`
	Error      string  `json:"error"`
}

type LotSizeResponse struct {
	Success bool   `json:"success"`
	Symbol  string `json:"symbol"`
	Expiry  string `json:"expiry"`
	LotSize int    `json:"lot_size"`
	Error   string `json:"error"`
}

func (c *Client) FetchExpiryDates(symbol string) ([]string, error) {
	// For now, return a placeholder - expiry dates should come from a separate service or config
	// This can be enhanced later to fetch from a dedicated expiry service
	return []string{}, fmt.Errorf("expiry dates not implemented - use static config or separate service")
}

func (c *Client) FetchOptionToken(symbol, expiry, optType string, strike float64) (int64, error) {
	params := map[string]string{
		"symbol":      symbol,
		"expiry":      expiry,
		"option_type": optType,
		"strike":      fmt.Sprintf("%.0f", strike),
	}

	body, err := c.get("/api/token", params)
	if err != nil {
		return 0, err
	}

	var res TokenResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, fmt.Errorf("JSON parse error: %v | Body: %s", err, string(body))
	}

	if !res.Success {
		return 0, fmt.Errorf("contract-master error: %s | Body: %s", res.Error, string(body))
	}

	return res.Token, nil
}

func (c *Client) FetchLotSize(symbol, expiry string) (int, error) {
	params := map[string]string{
		"symbol": symbol,
		"expiry": expiry,
	}

	body, err := c.get("/api/lot-size", params)
	if err != nil {
		return 0, err
	}

	var res LotSizeResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, fmt.Errorf("JSON parse error: %v | Body: %s", err, string(body))
	}

	if !res.Success {
		return 0, fmt.Errorf("contract-master error: %s | Body: %s", res.Error, string(body))
	}

	return res.LotSize, nil
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
		if ce, err := c.FetchOptionToken(symbol, expiry, "CE", s); err == nil {
			row.CEToken = int(ce)
		} else {
			slog.Warn("Failed to fetch CE token", "symbol", symbol, "strike", s, "error", err)
		}

		// Fetch PE
		if pe, err := c.FetchOptionToken(symbol, expiry, "PE", s); err == nil {
			row.PEToken = int(pe)
		} else {
			slog.Warn("Failed to fetch PE token", "symbol", symbol, "strike", s, "error", err)
		}

		chain.Strikes = append(chain.Strikes, row)
	}

	return chain, nil
}
