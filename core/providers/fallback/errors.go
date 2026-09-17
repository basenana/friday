package fallback

import (
	"context"
	"errors"
)

// shouldFallbackOnError returns true for any model error except context cancellation.
func shouldFallbackOnError(err error) bool {
	if err == nil {
		return false
	}

	// Cancellation belongs to the caller and should stop the whole request.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	return true
}
