package trading

import (
	"errors"
	"testing"
)

func TestBuildPendingFillErrorClassifiesAsPendingFill(t *testing.T) {
	err := NewBuildPendingFillError(
		"intent-1",
		"120000088",
		"OPEN",
		0,
		65,
	)

	if !IsBuildPendingFill(err) {
		t.Fatalf("expected IsBuildPendingFill to be true")
	}

	got := buildFailureStatusFromError(err)
	if got != TradeStatusPendingFill {
		t.Fatalf("expected %s, got %s", TradeStatusPendingFill, got)
	}
}

func TestWrappedBuildPendingFillErrorClassifiesAsPendingFill(t *testing.T) {
	base := NewBuildPendingFillError(
		"intent-2",
		"120000089",
		"SUBMITTED",
		0,
		65,
	)

	err := errors.Join(base)

	if !IsBuildPendingFill(err) {
		t.Fatalf("expected wrapped BuildPendingFillError to classify as pending fill")
	}

	got := buildFailureStatusFromError(err)
	if got != TradeStatusPendingFill {
		t.Fatalf("expected %s, got %s", TradeStatusPendingFill, got)
	}
}

func TestNormalErrorClassifiesAsFailed(t *testing.T) {
	err := errors.New("broker rejected order")

	got := buildFailureStatusFromError(err)
	if got != TradeStatusFailed {
		t.Fatalf("expected %s, got %s", TradeStatusFailed, got)
	}
}

func TestBuildCompleteStatuses(t *testing.T) {
	for _, status := range []string{"FILLED", "SUCCESS", " filled ", "success"} {
		if !isBuildCompleteStatus(status) {
			t.Fatalf("expected complete status for %q", status)
		}
	}

	for _, status := range []string{"OPEN", "SUBMITTED", "PARTIALLY_FILLED", "REJECTED", "CANCELLED"} {
		if isBuildCompleteStatus(status) {
			t.Fatalf("did not expect complete status for %q", status)
		}
	}
}

func TestBuildRetryableTerminalStatuses(t *testing.T) {
	for _, status := range []string{"REJECTED", "CANCELLED", "CANCELED", " rejected "} {
		if !isBuildRetryableTerminalStatus(status) {
			t.Fatalf("expected retryable terminal status for %q", status)
		}
	}

	for _, status := range []string{"OPEN", "SUBMITTED", "PARTIALLY_FILLED", "FILLED", "SUCCESS"} {
		if isBuildRetryableTerminalStatus(status) {
			t.Fatalf("did not expect retryable terminal status for %q", status)
		}
	}
}

func TestDeriveTradeStatusFromBuildCountsAllFilled(t *testing.T) {
	got := deriveTradeStatusFromBuildCounts(BuildOrderLifecycleCounts{
		Total:  2,
		Filled: 2,
	})

	if got != TradeStatusActive {
		t.Fatalf("expected %s, got %s", TradeStatusActive, got)
	}
}

func TestDeriveTradeStatusFromBuildCountsPending(t *testing.T) {
	got := deriveTradeStatusFromBuildCounts(BuildOrderLifecycleCounts{
		Total:   2,
		Pending: 2,
	})

	if got != TradeStatusPendingFill {
		t.Fatalf("expected %s, got %s", TradeStatusPendingFill, got)
	}
}

func TestDeriveTradeStatusFromBuildCountsPartialFill(t *testing.T) {
	got := deriveTradeStatusFromBuildCounts(BuildOrderLifecycleCounts{
		Total:   2,
		Filled:  1,
		Pending: 1,
	})

	if got != TradeStatusPendingFill {
		t.Fatalf("expected %s, got %s", TradeStatusPendingFill, got)
	}
}

func TestDeriveTradeStatusFromBuildCountsRejected(t *testing.T) {
	got := deriveTradeStatusFromBuildCounts(BuildOrderLifecycleCounts{
		Total:        2,
		TerminalFail: 2,
	})

	if got != TradeStatusFailed {
		t.Fatalf("expected %s, got %s", TradeStatusFailed, got)
	}
}

func TestDeriveTradeStatusFromBuildCountsUnknown(t *testing.T) {
	got := deriveTradeStatusFromBuildCounts(BuildOrderLifecycleCounts{
		Total:   2,
		Unknown: 2,
	})

	if got != TradeStatusPendingFill {
		t.Fatalf("expected %s, got %s", TradeStatusPendingFill, got)
	}
}
