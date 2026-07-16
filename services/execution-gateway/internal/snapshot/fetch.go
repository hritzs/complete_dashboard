package snapshot

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
)

type OptionChainResponse struct {
	Success bool            `json:"success"`
	Data    OptionChainData `json:"data"`
	Error   string          `json:"error"`
}

type OptionChainData struct {
	Symbol            string           `json:"symbol"`
	SyntheticFuture   float64          `json:"synthetic_future"`
	FutureLtp         float64          `json:"future_ltp"`
	ATM               float64          `json:"atm"`
	Expiry            string           `json:"expiry"`
	AvailableExpiries []string         `json:"available_expiries,omitempty"`
	LotSize           int              `json:"lot_size"`
	Chain             []OptionChainRow `json:"chain"`
}

type OptionChainRow struct {
	Strike  float64 `json:"strike"`
	CEToken int64   `json:"ce_token"`
	PEToken int64   `json:"pe_token"`
	CELtp   float64 `json:"ce_ltp"`
	PELtp   float64 `json:"pe_ltp"`
	CEDelta float64 `json:"ce_delta"`
	PEDelta float64 `json:"pe_delta"`
	IsATM   bool    `json:"is_atm"`
}

func FetchATMData(symbol string, targetExpiry string) (OptionChainData, error) {
	url := fmt.Sprintf("http://127.0.0.1:8003/api/option-chain/%s", symbol)
	if strings.TrimSpace(targetExpiry) != "" {
		url = fmt.Sprintf("%s?expiry=%s", url, targetExpiry)
	}
	resp, err := http.Get(url)
	if err != nil {
		return OptionChainData{}, fmt.Errorf("failed to connect to snapshot service: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return OptionChainData{}, fmt.Errorf("snapshot service returned HTTP %d", resp.StatusCode)
	}
	var result OptionChainResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return OptionChainData{}, fmt.Errorf("failed to parse JSON: %v", err)
	}
	if !result.Success {
		if result.Error != "" {
			return OptionChainData{}, fmt.Errorf("%s", result.Error)
		}
		return OptionChainData{}, fmt.Errorf("snapshot service did not return success")
	}
	return result.Data, nil
}

func FindATMRow(chain OptionChainData) (*OptionChainRow, error) {
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if row.IsATM || row.Strike == chain.ATM {
			return row, nil
		}
	}
	return nil, fmt.Errorf("ATM row not found")
}

func FindRowByStrike(chain OptionChainData, strike int) (*OptionChainRow, error) {
	for i := range chain.Chain {
		row := &chain.Chain[i]
		if int(math.Round(row.Strike)) == strike {
			return row, nil
		}
	}
	return nil, fmt.Errorf("strike %d not found in chain", strike)
}
