package trading

import (
	"context"
	"fmt"
	"log"
	"time"
)

type VerifiedOrderAttempt struct {
	Intent        OrderIntent
	BrokerOrderID string
	RequestedQty  int64
	FilledQty     int64
	Status        string
}

type VerifiedExecutionSummary struct {
	Attempts          []VerifiedOrderAttempt
	Fills             []BrokerFill
	RequestedCE       int64
	RequestedPE       int64
	VerifiedCE        int64
	VerifiedPE        int64
	UnfilledCE        int64
	UnfilledPE        int64
	VerificationError error
}

func (s VerifiedExecutionSummary) Complete() bool {
	return s.UnfilledCE <= 0 && s.UnfilledPE <= 0
}

func (s VerifiedExecutionSummary) Clamp() VerifiedExecutionSummary {
	if s.VerifiedCE > s.RequestedCE {
		s.VerifiedCE = s.RequestedCE
	}
	if s.VerifiedPE > s.RequestedPE {
		s.VerifiedPE = s.RequestedPE
	}
	s.UnfilledCE = s.RequestedCE - s.VerifiedCE
	s.UnfilledPE = s.RequestedPE - s.VerifiedPE
	return s
}

func verifySubmittedFills(
	ctx context.Context,
	provider VerifiedFillsProvider,
	submitted map[string]struct{},
	ceToken int64,
	peToken int64,
	requestedCE int64,
	requestedPE int64,
) (VerifiedExecutionSummary, error) {
	if provider == nil {
		return VerifiedExecutionSummary{}, fmt.Errorf("nil verified fills provider")
	}

	fills, err := provider.GetVerifiedFills(ctx)
	if err != nil {
		return VerifiedExecutionSummary{}, err
	}

	out := VerifiedExecutionSummary{
		RequestedCE: requestedCE,
		RequestedPE: requestedPE,
	}

	for _, fill := range fills {
		if !fill.Verified || fill.FilledQty <= 0 {
			continue
		}
		if _, ok := submitted[fill.BrokerOrderID]; !ok {
			continue
		}
		if fill.Token != ceToken && fill.Token != peToken {
			continue
		}

		out.Fills = append(out.Fills, fill)

		switch fill.Token {
		case ceToken:
			out.VerifiedCE += fill.FilledQty
		case peToken:
			out.VerifiedPE += fill.FilledQty
		}
	}

	return out.Clamp(), nil
}

func waitForVerifiedFills(
	ctx context.Context,
	provider VerifiedFillsProvider,
	submitted map[string]struct{},
	ceToken int64,
	peToken int64,
	requestedCE int64,
	requestedPE int64,
	maxAttempts int,
	delay time.Duration,
) VerifiedExecutionSummary {
	var last VerifiedExecutionSummary

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				last.VerificationError = ctx.Err()
				return last
			case <-time.After(delay):
			}
		}

		result, err := verifySubmittedFills(
			ctx,
			provider,
			submitted,
			ceToken,
			peToken,
			requestedCE,
			requestedPE,
		)
		if err != nil {
			last.VerificationError = err
			log.Printf("verified-fill attempt %d failed: %v", attempt+1, err)
			continue
		}

		last = result
		if result.Complete() {
			return result
		}
	}

	return last
}
