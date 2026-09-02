package trading

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type BestQuote struct {
	BestBid float64
	BestAsk float64
}

// BestQuoteProvider is implemented by broker executors that can fetch a
// current executable bid/ask quote for a normalized trading token.
type BestQuoteProvider interface {
	GetBestQuote(
		ctx context.Context,
		token int64,
		exchangeSegment string,
	) (BestQuote, error)
}

func AggressiveLimitPrice(
	side string,
	quote BestQuote,
	buyBuffer float64,
	sellBuffer float64,
) (float64, error) {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY":
		if quote.BestAsk <= 0 {
			return 0, fmt.Errorf(
				"cannot price BUY: best ask unavailable",
			)
		}

		buffer := buyBuffer
		if buffer <= 0 {
			buffer = 2
		}

		price := quote.BestAsk + buffer
		return price, nil

	case "SELL":
		if quote.BestBid <= 0 {
			return 0, fmt.Errorf(
				"cannot price SELL: best bid unavailable",
			)
		}

		buffer := sellBuffer
		if buffer <= 0 {
			buffer = 2
		}

		price := math.Max(0.05, quote.BestBid-buffer)
		return price, nil

	default:
		return 0, fmt.Errorf(
			"unsupported order side %q",
			side,
		)
	}
}

func PriceIntentFromLiveQuote(
	ctx context.Context,
	provider BestQuoteProvider,
	intent *OrderIntent,
	buyBuffer float64,
	sellBuffer float64,
) (BestQuote, error) {
	if provider == nil {
		return BestQuote{}, fmt.Errorf("best quote provider is nil")
	}
	if intent == nil {
		return BestQuote{}, fmt.Errorf("order intent is nil")
	}

	quote, err := provider.GetBestQuote(
		ctx,
		intent.Token,
		intent.ExchangeSegment,
	)
	if err != nil {
		return BestQuote{}, fmt.Errorf(
			"quote token=%d side=%s phase=%s: %w",
			intent.Token,
			intent.Side,
			intent.Phase,
			err,
		)
	}

	price, err := AggressiveLimitPrice(
		intent.Side,
		quote,
		buyBuffer,
		sellBuffer,
	)
	if err != nil {
		return BestQuote{}, fmt.Errorf(
			"price token=%d side=%s phase=%s: %w",
			intent.Token,
			intent.Side,
			intent.Phase,
			err,
		)
	}

	intent.OrderType = "LIMIT"
	intent.LimitPrice = &price
	intent.ExpectedPrice = price

	return quote, nil
}
