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

// BuildExecutionOutcome represents the complete local view of one BUILD
// attempt. SubmittedCount is the number of non-empty broker order IDs
// obtained. SubmissionErrors records unresolved submission outcomes.
//
// Summary contains only broker-order-book-verified quantities and prices.
// It must never contain quantities inferred from the requested order size
// or prices inferred from limit or expected prices.
type BuildExecutionOutcome struct {
	Summary          VerifiedExecutionSummary
	SubmittedCount   int
	SubmissionErrors int
	FirstError       error
}

func (o BuildExecutionOutcome) HasSubmittedOrders() bool {
	return o.SubmittedCount > 0
}

func (o BuildExecutionOutcome) HasVerifiedExposure() bool {
	return o.Summary.VerifiedCE > 0 ||
		o.Summary.VerifiedPE > 0
}

func (o BuildExecutionOutcome) FullyVerified() bool {
	return o.FirstError == nil &&
		o.SubmissionErrors == 0 &&
		o.Summary.VerificationError == nil &&
		o.Summary.Complete()
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
	// Unconfirmed lists submitted broker order IDs that got no terminal
	// confirmation in time: their outcome is UNKNOWN (may well have
	// filled), so a caller must never re-send their quantity.
	Unconfirmed []string
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

// submittedOrderMeta is what waitForVerifiedFillsLive needs per submitted
// broker order to wait on it individually: which leg/token it belongs to
// and the quantity that makes it terminal (a real IRIS FilledQty reaching
// this counts as done, same threshold orderTerminal already uses).
type submittedOrderMeta struct {
	Token    int64
	Leg      string
	Quantity int64
}

// waitForVerifiedFillsLive verifies each submitted order via the live,
// event-driven OrderEventRegistry (fed in real time by the IRIS push
// pipeline, see order_event_registry.go) instead of polling the
// reconciler's DB a fixed number of times over a fixed total budget.
//
// This closes a real double-square-off race confirmed live 2026-09-23: a
// 77-lot position's square-off polled for a shared 4.8s budget across an
// entire chunk of orders; under the load of that many orders, the
// reconciler's Iris-push-to-Postgres write pipeline hadn't caught up by
// the time the budget ran out, so SquareOff's outer retry loop saw
// "still not fully verified" and submitted a SECOND full round of BUY
// orders for the same "remaining" quantity -- while the FIRST round's
// orders were still genuinely alive and went on to fill anyway. The real
// broker position ended up bought back twice (confirmed via
// GetAllContract/fills: order count and filled quantity both ~2x the
// entry side), and had to be corrected manually in the broker's own
// terminal. Waiting on each order's own live confirmation, with no fixed
// shared budget, means the loop only ever concludes "still remaining"
// once every submitted order has genuinely reached a terminal outcome --
// never while something already sent might still be resolving.
//
// Falls back to the polling-based waitForVerifiedFills if the live
// registry isn't available (e.g. in tests, or before it's wired up) --
// never silently skips verification.
func waitForVerifiedFillsLive(
	ctx context.Context,
	events *OrderEventRegistry,
	provider VerifiedFillsProvider,
	tradeUID string,
	submitted map[string]submittedOrderMeta,
	ceToken int64,
	peToken int64,
	requestedCE int64,
	requestedPE int64,
	perOrderTimeout time.Duration,
) VerifiedExecutionSummary {
	if events == nil || !events.Healthy() {
		plain := make(map[string]struct{}, len(submitted))
		for id := range submitted {
			plain[id] = struct{}{}
		}
		return waitForVerifiedFills(ctx, provider, plain, ceToken, peToken, requestedCE, requestedPE, 6, 800*time.Millisecond)
	}

	out := VerifiedExecutionSummary{RequestedCE: requestedCE, RequestedPE: requestedPE}

	type result struct {
		brokerOrderID string
		filled        int64
		status        string
		err           error
	}
	results := make(chan result, len(submitted))
	for brokerOrderID, meta := range submitted {
		go func(brokerOrderID string, meta submittedOrderMeta) {
			u, err := events.WaitTerminal(ctx, tradeUID, brokerOrderID, meta.Quantity, perOrderTimeout)
			results <- result{brokerOrderID: brokerOrderID, filled: u.FilledQty, status: u.Status, err: err}
		}(brokerOrderID, meta)
	}

	for range submitted {
		r := <-results
		meta := submitted[r.brokerOrderID]
		if r.err != nil {
			// No live terminal event within the per-order timeout -- do
			// NOT assume either outcome. Leave it unverified (counted as
			// still-remaining below) rather than guessing it filled or
			// guessing it's safely dead; the caller's own circuit breaker
			// and bounded attempt count are what stop this from looping
			// forever on a genuinely stuck order.
			log.Printf("[SQF-LIVE] no terminal confirmation for order=%s trade=%s within %s: %v", r.brokerOrderID, tradeUID, perOrderTimeout, r.err)
			out.Unconfirmed = append(out.Unconfirmed, r.brokerOrderID)
			continue
		}
		if r.filled <= 0 {
			continue
		}
		fill := BrokerFill{
			BrokerOrderID: r.brokerOrderID,
			Token:         meta.Token,
			FilledQty:     r.filled,
			Status:        r.status,
			Verified:      true,
			Source:        "IRIS_LIVE_EVENT",
		}
		out.Fills = append(out.Fills, fill)
		switch meta.Token {
		case ceToken:
			out.VerifiedCE += r.filled
		case peToken:
			out.VerifiedPE += r.filled
		}
	}

	return out.Clamp()
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

	// Never send an order whose intent_id is recorded under ANOTHER trade:
	// its fills would be matched to the wrong trade (or none), and that
	// trade's exit would then re-send the "unconfirmed" quantity.
	if owners, ok := s.Store.(interface {
		IntentOwner(ctx context.Context, intentID string) (string, bool, error)
	}); ok {
		owner, found, oerr := owners.IntentOwner(ctx, intent.IntentID)
		if oerr == nil && found && owner != "" && owner != tradeUID {
			log.Printf("🚫 ORDER REFUSED, NOT SENT: intent_id=%s is already recorded for trade=%s, not trade=%s (side=%s token=%d qty=%d)",
				intent.IntentID, owner, tradeUID, intent.Side, intent.Token, intent.Quantity)
			return "", "", fmt.Errorf("submit intent %s: intent_id already belongs to trade %s", intent.IntentID, owner)
		}
	}

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
