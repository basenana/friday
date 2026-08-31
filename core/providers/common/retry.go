package common

import (
	"context"
	"time"
)

// WaitBackoff sleeps for the provided duration unless the context is canceled first.
func WaitBackoff(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RetryBackoffDelay returns the backoff delay to wait before retry attempt
// (1-based), growing linearly: 10s, 20s, 30s...
func RetryBackoffDelay(attempt int) time.Duration {
	return time.Second * time.Duration(10*attempt)
}

// Truncate shortens s to at most max bytes for logging, appending an ellipsis
// when truncation occurred. It complements FormatToolUseArgumentsError, which
// embeds the raw arguments in the error message.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
