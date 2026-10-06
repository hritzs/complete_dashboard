package greeksoft

// Read-only market-data pulls for checking what GreekSoft actually returns
// (candle granularity, depth/bid-ask). Nothing here touches orders.

import (
	"context"
	"fmt"
	"strconv"

	gs "trading-platform/libs/broker-greeksoft"
)

// RawOHLC returns GreekSoft's get_ohlc response as-is.
func (e *Executor) RawOHLC(ctx context.Context, token int64, interval int, date string, days int) (map[string]interface{}, int64, error) {
	if e == nil || e.Client == nil || e.Client.Session == nil {
		return nil, 0, fmt.Errorf("greeksoft client/session not available")
	}
	gtoken := resolveGreeksoftGToken("", token)
	resp, err := e.Client.GetOHLC(ctx, gtoken, interval, date, days)
	return resp, gtoken, err
}

// RawMBP returns GreekSoft's getToken_OnlyMbpData (mode FULL) response as-is.
func (e *Executor) RawMBP(ctx context.Context, token int64, exchange string) (map[string]interface{}, int64, error) {
	if e == nil || e.Client == nil || e.Client.Session == nil {
		return nil, 0, fmt.Errorf("greeksoft client/session not available")
	}
	if exchange == "" {
		exchange = "NSE"
	}
	gtoken := resolveGreeksoftGToken("", token)
	resp, err := e.Client.GetTokenOnlyMbpData(ctx, []gs.ExchangeToken{{Exchange: exchange, Token: strconv.FormatInt(gtoken, 10)}})
	return resp, gtoken, err
}
