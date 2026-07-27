package trading

import (
	"errors"
	"fmt"
	"strings"
)

const (
	TradeStatusPendingFill = "PENDING_FILL"
	TradeStatusFailed      = "FAILED"
)

type BuildPendingFillError struct {
	IntentID      string
	BrokerOrderID string
	Status        string
	FilledQty     int64
	PendingQty    int64
}

func NewBuildPendingFillError(intentID string, brokerOrderID string, status string, filledQty int64, pendingQty int64) *BuildPendingFillError {
	return &BuildPendingFillError{
		IntentID:      intentID,
		BrokerOrderID: strings.TrimSpace(brokerOrderID),
		Status:        strings.ToUpper(strings.TrimSpace(status)),
		FilledQty:     filledQty,
		PendingQty:    pendingQty,
	}
}

func (e *BuildPendingFillError) Error() string {
	if e == nil {
		return "build order pending fill"
	}
	return fmt.Sprintf(
		"build order not fully filled: intent_id=%s broker_order_id=%s status=%s filled_qty=%d pending_qty=%d",
		e.IntentID,
		e.BrokerOrderID,
		e.Status,
		e.FilledQty,
		e.PendingQty,
	)
}

func IsBuildPendingFill(err error) bool {
	if err == nil {
		return false
	}
	var pending *BuildPendingFillError
	return errors.As(err, &pending)
}

func buildFailureStatusFromError(err error) string {
	if IsBuildPendingFill(err) {
		return TradeStatusPendingFill
	}
	return TradeStatusFailed
}

func isBuildCompleteStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FILLED", "SUCCESS":
		return true
	default:
		return false
	}
}

func isBuildRetryableTerminalStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "REJECTED", "CANCELLED", "CANCELED":
		return true
	default:
		return false
	}
}
