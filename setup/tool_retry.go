package setup

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/basenana/friday/core/tools"
	"github.com/google/uuid"
)

const (
	// defaultToolRetryMaxAttempts is the retry budget applied to every tool.
	defaultToolRetryMaxAttempts = 3
	// defaultToolRetryBaseBackoff is the base delay before the first retry.
	defaultToolRetryBaseBackoff = 500 * time.Millisecond
	// maxToolRetryBackoff caps the exponential backoff delay.
	maxToolRetryBackoff = 30 * time.Second
)

// defaultToolInvocationPolicy is the ToolInvocationPolicyResolver wired into
// NewAgent: a capped retry budget with exponential backoff for every tool.
func defaultToolInvocationPolicy(string) ToolInvocationPolicy {
	return ToolInvocationPolicy{
		MaxAttempts: defaultToolRetryMaxAttempts,
		Backoff:     defaultToolRetryBaseBackoff,
	}
}

// toolRetryBackoff returns the delay to wait after the given failed attempt
// (1-based): exponential growth from base (x2 per attempt), capped at
// maxToolRetryBackoff, with jitter in the upper half of the delay range to
// avoid thundering-herd retries.
func toolRetryBackoff(attempt int, base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	delay := base
	for i := 1; i < attempt && delay < maxToolRetryBackoff; i++ {
		delay *= 2
	}
	if delay > maxToolRetryBackoff {
		delay = maxToolRetryBackoff
	}
	half := delay / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

type ToolInvocationPolicy struct {
	MaxAttempts    int
	Backoff        time.Duration
	Required       *bool
	RequiredSource string
}

type ToolInvocationPolicyResolver func(toolName string) ToolInvocationPolicy

type retryableToolError interface {
	Retryable() bool
}

func wrapToolsWithInvocationRetry(toolList []*tools.Tool, resolve ToolInvocationPolicyResolver) []*tools.Tool {
	if resolve == nil || len(toolList) == 0 {
		return toolList
	}
	wrapped := make([]*tools.Tool, 0, len(toolList))
	for _, tool := range toolList {
		if tool == nil || tool.Handler == nil {
			wrapped = append(wrapped, tool)
			continue
		}
		clone := *tool
		originalHandler := tool.Handler
		clone.Handler = func(toolName string, handler tools.ToolHandlerFunc) tools.ToolHandlerFunc {
			return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if _, explicitlyManaged := toolInvocationEvidenceFromContext(ctx); explicitlyManaged {
					return handler(ctx, request)
				}
				policy := resolve(toolName)
				if policy.MaxAttempts < 1 {
					policy.MaxAttempts = 1
				}
				previousInvocationID := ""
				for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					invocationID := uuid.NewString()
					evidence := ToolInvocationEvidence{
						InvocationID: invocationID, Attempt: attempt, RetryOfInvocationID: previousInvocationID,
						Required: policy.Required, RequiredSource: policy.RequiredSource,
					}
					result, err := handler(WithToolInvocationEvidence(ctx, evidence), request)
					if err == nil && (result == nil || !result.IsError) {
						return result, nil
					}
					if contextErr := ctx.Err(); contextErr != nil {
						return result, contextErr
					}
					if attempt == policy.MaxAttempts || !toolFailureRetryable(ctx, result, err) {
						return result, err
					}
					previousInvocationID = invocationID
					if delay := toolRetryBackoff(attempt, policy.Backoff); delay > 0 {
						timer := time.NewTimer(delay)
						select {
						case <-ctx.Done():
							timer.Stop()
							return result, ctx.Err()
						case <-timer.C:
						}
					}
				}
				return nil, nil
			}
		}(tool.Name, originalHandler)
		wrapped = append(wrapped, &clone)
	}
	return wrapped
}

func toolFailureRetryable(ctx context.Context, result *tools.Result, err error) bool {
	if ctx == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if result != nil && result.IsError {
		return result.Retryable
	}
	var retryable retryableToolError
	return err != nil && errors.As(err, &retryable) && retryable.Retryable()
}
