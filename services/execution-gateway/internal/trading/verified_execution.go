package trading

import (
	"context"
	"fmt"
	"log"
	"strings"
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

// submitOrderIntent submits a single order intent to the broker without
// ever treating the submission response as a fill. It persists the local
// intent first, submits, and records the broker's acknowledgement (broker
// order ID + status) via MarkOrderSubmitted. It never calls
// MarkOrderExecution and never infers a fill price or filled quantity from
// the response — that can only come from a verified broker fill.
//
// Returns the broker order ID on success. An empty broker order ID from the
// executor is treated as a failure (fail closed): callers must not add an
// empty ID to a submitted-set used for verification, since that could
// coincide with how some broker responses represent "not yet acknowledged".
func (s *Service) submitOrderIntent(
	ctx context.Context,
	executor Executor,
	tradeUID string,
	intent OrderIntent,
) (brokerOrderID string, status string, err error) {
	s.Store.AppendIntent(tradeUID, intent)

	res, err := executor.ExecuteOrderIntent(ctx, intent)
	if err != nil {
		return "", "", fmt.Errorf("submit intent %s: %w", intent.IntentID, err)
	}
	if res == nil {
		return "", "", fmt.Errorf("submit intent %s: nil execution result", intent.IntentID)
	}

	if updater, ok := s.Store.(interface {
		MarkOrderSubmitted(intentID string, brokerOrderID string, status string, rawResponse string)
	}); ok {
		updater.MarkOrderSubmitted(intent.IntentID, res.BrokerOrderID, res.Status, res.RawResponse)
	}

	if strings.TrimSpace(res.BrokerOrderID) == "" {
		return "", res.Status, fmt.Errorf(
			"submit intent %s: broker returned empty broker_order_id status=%s",
			intent.IntentID,
			res.Status,
		)
	}

	return res.BrokerOrderID, res.Status, nil
}

// verifyAndPersistTradeFills polls the broker order book once for all
// broker order IDs submitted for a trade, matches verified fills against
// the trade's CE/PE tokens and requested quantities, durably persists
// whatever verified fills are found, and returns the resulting summary.
//
// It reuses verifySubmittedFills/waitForVerifiedFills unchanged. Callers
// must not treat the trade as active/complete except via
// summary.Complete() on the returned summary, and should still treat a
// persistence error as a reason not to proceed, since an unpersisted
// verified fill is not a durable source of truth.
func (s *Service) verifyAndPersistTradeFills(
	ctx context.Context,
	provider VerifiedFillsProvider,
	brokerName string,
	accountID string,
	submitted map[string]struct{},
	ceToken int64,
	peToken int64,
	requestedCE int64,
	requestedPE int64,
	maxAttempts int,
	delay time.Duration,
) (VerifiedExecutionSummary, error) {
	summary := waitForVerifiedFills(
		ctx,
		provider,
		submitted,
		ceToken,
		peToken,
		requestedCE,
		requestedPE,
		maxAttempts,
		delay,
	)

	if summary.VerificationError != nil {
		log.Printf(
			"verifyAndPersistTradeFills: verification error broker=%s account=%s err=%v",
			brokerName,
			accountID,
			summary.VerificationError,
		)
	}

	if len(summary.Fills) == 0 {
		return summary, nil
	}

	persister, ok := s.Store.(VerifiedFillPersistence)
	if !ok {
		return summary, fmt.Errorf("store does not implement VerifiedFillPersistence")
	}

	report, err := persister.PersistVerifiedFills(ctx, brokerName, accountID, summary.Fills)
	if err != nil {
		return summary, fmt.Errorf("persist verified fills: %w", err)
	}

	log.Printf(
		"verifyAndPersistTradeFills: persisted broker=%s account=%s processed=%d persisted=%d skipped=%d unmatched=%v errors=%v",
		brokerName,
		accountID,
		report.Processed,
		report.Persisted,
		report.Skipped,
		report.Unmatched,
		report.Errors,
	)

	return summary, nil
}
