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
