package trading

import (
	"context"
	"time"
)

// BrokerFill is a normalized, verified broker execution record.
// It is derived only from broker order-book data — never fabricated.
type BrokerFill struct {
	BrokerOrderID string
	Token         int64 // normalized (short) token
	BrokerToken   int64 // raw broker (global) token
	Side          string
	FilledQty     int64
	AveragePrice  float64
	Status        string

	StrategyKey   string
	BrokerTag     string
	BrokerUserTag string
	OptionType    string
	Strike        float64
	Symbol        string
	TradeSymbol   string
	BrokerTime    time.Time

	Verified bool
	Source   string
}

type LegReconciliation struct {
	Token       int64   `json:"token"`
	BuyQty      int64   `json:"buy_qty"`
	BuyAvg      float64 `json:"buy_avg"`
	SellQty     int64   `json:"sell_qty"`
	SellAvg     float64 `json:"sell_avg"`
	NetQty      int64   `json:"net_qty"`
	OpenQty     int64   `json:"open_qty"`
	EntryAvg    float64 `json:"entry_avg"`
	RealizedPnL float64 `json:"realized_pnl"`
}

type TradeReconciliation struct {
	TradeUID    string            `json:"trade_uid"`
	CE          LegReconciliation `json:"ce"`
	PE          LegReconciliation `json:"pe"`
	RealizedPnL float64           `json:"realized_pnl"`
	Verified    bool              `json:"verified"`
	Source      string            `json:"source"`
}

// AggregateLegFromFills computes a LegReconciliation for one token from
// a set of verified broker fills. It performs no order placement.
func AggregateLegFromFills(fills []BrokerFill, token int64) LegReconciliation {
	var out LegReconciliation
	out.Token = token

	var buyValue, sellValue float64

	for _, f := range fills {
		if f.Token != token || !f.Verified || f.FilledQty <= 0 {
			continue
		}

		switch f.Side {
		case "BUY":
			out.BuyQty += f.FilledQty
			buyValue += float64(f.FilledQty) * f.AveragePrice
		case "SELL":
			out.SellQty += f.FilledQty
			sellValue += float64(f.FilledQty) * f.AveragePrice
		}
	}

	if out.BuyQty > 0 {
		out.BuyAvg = buyValue / float64(out.BuyQty)
	}
	if out.SellQty > 0 {
		out.SellAvg = sellValue / float64(out.SellQty)
	}

	out.NetQty = out.BuyQty - out.SellQty

	matched := out.BuyQty
	if out.SellQty < matched {
		matched = out.SellQty
	}

	if out.NetQty < 0 {
		// Net short: entry average is the sell average, open qty is |NetQty|.
		out.EntryAvg = out.SellAvg
		out.OpenQty = -out.NetQty
		if matched > 0 {
			out.RealizedPnL = (out.SellAvg - out.BuyAvg) * float64(matched)
		}
	} else if out.NetQty > 0 {
		// Net long: entry average is the buy average, open qty is NetQty.
		out.EntryAvg = out.BuyAvg
		out.OpenQty = out.NetQty
		if matched > 0 {
			out.RealizedPnL = (out.SellAvg - out.BuyAvg) * float64(matched)
		}
	} else {
		// Flat: fully matched, everything is realized.
		out.OpenQty = 0
		if matched > 0 {
			out.RealizedPnL = (out.SellAvg - out.BuyAvg) * float64(matched)
		}
	}

	return out
}

// VerifiedFillsProvider is implemented by any broker executor that can
// report verified, broker-confirmed order-book fills. Defining this
// interface here (in trading) instead of importing a concrete broker
// package avoids an import cycle: broker packages import trading for
// BrokerFill, so trading must not import broker packages back.
type VerifiedFillsProvider interface {
	GetVerifiedFills(ctx context.Context) ([]BrokerFill, error)
}
