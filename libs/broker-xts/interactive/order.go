package interactive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	xts "trading-platform/libs/broker-xts"
	"trading-platform/libs/broker-xts/auth"
	broker "trading-platform/libs/go-broker"
)

func PlaceOrder(c *xts.Client, intent broker.OrderIntent, orderUID string, limitPrice float64) error {
	if err := auth.EnsureLogin(c); err != nil {
		return err
	}

	payload := map[string]interface{}{
		"exchangeSegment":       intent.ExchangeSegment,
		"exchangeInstrumentID":  intent.InstrumentToken,
		"productType":           intent.ProductType,
		"orderType":             intent.OrderType,
		"orderSide":             intent.Side,
		"timeInForce":           "DAY",
		"disclosedQuantity":     0,
		"orderQuantity":         intent.Quantity,
		"limitPrice":            limitPrice,
		"stopPrice":             0.0,
		"orderUniqueIdentifier": orderUID,
		"clientID":              intent.ClientID,
	}
	pb, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", c.BaseURL+"/interactive/orders", bytes.NewBuffer(pb))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	c.Mu.Lock()
	req.Header.Set("authorization", c.Token)
	c.Mu.Unlock()

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if resp.StatusCode != 200 {
		return fmt.Errorf("XTS rejected order (HTTP %d): %v", resp.StatusCode, out)
	}

	return nil
}
