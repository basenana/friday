package providers

import (
	"context"
	"time"
)

// RetryEvent describes a physical model request that will be retried. Attempt
// is the 1-based number of the request about to be sent. Fallback transitions
// additionally populate the previous and endpoint-key fields.
type RetryEvent struct {
	Provider         string
	Model            string
	ModelKey         string
	PreviousModel    string
	PreviousModelKey string
	Attempt          int
	MaxAttempts      int
	Error            error
	Backoff          time.Duration
}

type retryObserverKey struct{}

// WithRetryObserver installs request-scoped retry observability without
// changing the Client or Response interfaces.
func WithRetryObserver(ctx context.Context, observer func(RetryEvent)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, retryObserverKey{}, observer)
}

// NotifyRetry reports an upcoming retry to the request-scoped observer.
func NotifyRetry(ctx context.Context, event RetryEvent) {
	observer, _ := ctx.Value(retryObserverKey{}).(func(RetryEvent))
	if observer != nil {
		observer(event)
	}
}
