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

// MinuteCloses implements trading.MinuteCloseProvider: the day's 1-minute
// candle CLOSES of one instrument from GreekSoft's get_ohlc, keyed by the
// bar's unix timestamp. A completed candle is stamped at its END (hh:mm:00);
// the bar still forming is stamped with its latest trade time (verified
// 2026-10-09 11:06). date is YYYYMMDD. Read-only, current session.
func (e *Executor) MinuteCloses(ctx context.Context, token int64, date string) (map[int64]float64, error) {
	if e == nil || e.Client == nil || e.Client.Session == nil {
		return nil, fmt.Errorf("greeksoft client/session not available")
	}
	gtoken := resolveGreeksoftGToken("", token)
	resp, err := e.Client.GetOHLC(ctx, gtoken, 1, date, 1)
	if err != nil {
		return nil, err
	}
	out := map[int64]float64{}
	r, _ := resp["response"].(map[string]interface{})
	d, _ := r["data"].(map[string]interface{})
	bars, _ := d["data"].([]interface{})
	for _, b := range bars {
		m, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		ts := int64(gsNum(m["timestamp"]))
		c := gsNum(m["Close"])
		if ts <= 0 || c <= 0 {
			continue
		}
		if _, dup := out[ts]; !dup { // the completed bar comes before a forming one
			out[ts] = c
		}
	}
	return out, nil
}

func gsNum(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
