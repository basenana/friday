package common

import (
	"context"
	"math/rand/v2"
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

// RetryBackoffDelay returns exponential backoff for the retry number
// (1-based), with 20 percent jitter: about 500ms, then about 1s.
func RetryBackoffDelay(retry int) time.Duration {
	if retry < 1 {
		retry = 1
	}
	base := 500 * time.Millisecond * time.Duration(1<<(retry-1))
	jitter := 0.8 + rand.Float64()*0.4
	delay := time.Duration(float64(base) * jitter)
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

// RetryDelay honors Retry-After when present, otherwise uses local backoff.
func RetryDelay(err error, retry int) time.Duration {
	if delay, ok := RetryAfter(err, time.Now()); ok {
		return delay
	}
	return RetryBackoffDelay(retry)
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
