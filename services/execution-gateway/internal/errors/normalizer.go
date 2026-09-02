package errors

import (
	"strings"
)

// NormalizedError represents a standard internal error format
type NormalizedError struct {
	Code        string
	Description string
	IsRetryable bool
	Original    error
}

// NormalizeError takes a raw broker error and maps it to the standard internal format
func NormalizeError(err error) NormalizedError {
	if err == nil {
		return NormalizedError{}
	}

	errMsg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errMsg, "timeout"), strings.Contains(errMsg, "deadline"):
		return NormalizedError{
			Code:        "ERR_TIMEOUT",
			Description: "Broker request timed out",
			IsRetryable: true,
			Original:    err,
		}
	case strings.Contains(errMsg, "unauthorized"), strings.Contains(errMsg, "token expired"):
		return NormalizedError{
			Code:        "ERR_AUTH",
			Description: "Broker session expired or unauthorized",
			IsRetryable: false, // Requires session-manager intervention
			Original:    err,
		}
	case strings.Contains(errMsg, "margin"), strings.Contains(errMsg, "insufficient funds"):
		return NormalizedError{
			Code:        "ERR_MARGIN",
			Description: "Insufficient margin to place order",
			IsRetryable: false,
			Original:    err,
		}
	case strings.Contains(errMsg, "rate limit"), strings.Contains(errMsg, "too many requests"):
		return NormalizedError{
			Code:        "ERR_RATE_LIMIT",
			Description: "Broker API rate limit exceeded",
			IsRetryable: true,
			Original:    err,
		}
	default:
		return NormalizedError{
			Code:        "ERR_UNKNOWN",
			Description: "Unknown broker error",
			IsRetryable: false,
			Original:    err,
		}
	}
}
