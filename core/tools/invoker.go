package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	MaxToolExecutionDuration = 30 * time.Minute
	MaxDeclaredToolTimeout   = 15 * time.Minute
)

var (
	errToolHardLimit       = errors.New("tool execution hard limit exceeded")
	errToolDeclaredTimeout = errors.New("tool declared timeout exceeded")
)

type InvocationMiddleware func(tool *Tool, next ToolHandlerFunc) ToolHandlerFunc

type Invoker struct {
	hardLimit   time.Duration
	maxDeclared time.Duration
	middlewares []InvocationMiddleware
}

type InvokerOption func(*Invoker)

func WithInvokerHardLimit(limit time.Duration) InvokerOption {
	return func(i *Invoker) { i.hardLimit = limit }
}

func WithInvokerMaxDeclaredTimeout(limit time.Duration) InvokerOption {
	return func(i *Invoker) { i.maxDeclared = limit }
}

func WithInvocationMiddleware(middleware ...InvocationMiddleware) InvokerOption {
	return func(i *Invoker) { i.middlewares = append(i.middlewares, middleware...) }
}

func NewInvoker(options ...InvokerOption) *Invoker {
	invoker := &Invoker{hardLimit: MaxToolExecutionDuration, maxDeclared: MaxDeclaredToolTimeout}
	for _, option := range options {
		option(invoker)
	}
	return invoker
}

func (i *Invoker) Invoke(ctx context.Context, tool *Tool, request *Request) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tool == nil {
		return nil, errors.New("tool is nil")
	}
	if tool.Handler == nil {
		return nil, fmt.Errorf("tool %s has no handler configured", tool.Name)
	}
	if request == nil {
		request = &Request{}
	}

	declared, result := i.resolveDeclaredTimeout(tool, request)
	if result != nil {
		return result, nil
	}

	callCtx, cancel, effective, _ := i.executionContext(ctx, declared)
	defer cancel()
	if result := invocationContextResult(tool.Name, callCtx, effective, 0, nil); result != nil {
		return result, nil
	}
	start := time.Now()
	handler := tool.Handler
	for index := len(i.middlewares) - 1; index >= 0; index-- {
		if i.middlewares[index] != nil {
			handler = i.middlewares[index](tool, handler)
		}
	}

	result, err := handler(callCtx, request)
	elapsed := time.Since(start)
	if contextResult := invocationContextResult(tool.Name, callCtx, effective, elapsed, result); contextResult != nil {
		return contextResult, nil
	}
	if err != nil {
		return result, err
	}
	if result != nil {
		result.ElapsedMs = elapsed.Milliseconds()
		normalizeResultStatus(result)
	}
	return result, nil
}

func invocationContextResult(toolName string, ctx context.Context, effective, elapsed time.Duration, original *Result) *Result {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errToolDeclaredTimeout):
		return mergeTimeoutResult(newTimeoutResult(toolName, TimeoutKindDeclared, effective, elapsed), original)
	case errors.Is(cause, errToolHardLimit):
		return mergeTimeoutResult(newTimeoutResult(toolName, TimeoutKindHardLimit, effective, elapsed), original)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return mergeTimeoutResult(newTimeoutResult(toolName, TimeoutKindParentDeadline, effective, elapsed), original)
	case errors.Is(ctx.Err(), context.Canceled):
		return newCancelledResult(toolName, elapsed)
	default:
		return nil
	}
}

func mergeTimeoutResult(timeout, original *Result) *Result {
	if timeout == nil || original == nil {
		return timeout
	}
	timeout.ExitCode = original.ExitCode
	if strings.TrimSpace(original.FYI) != "" {
		timeout.FYI = original.FYI
	}
	return timeout
}

// ContextTimeoutKind reports which invocation deadline expired.
func ContextTimeoutKind(ctx context.Context) TimeoutKind {
	if ctx == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ""
	}
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errToolDeclaredTimeout):
		return TimeoutKindDeclared
	case errors.Is(cause, errToolHardLimit):
		return TimeoutKindHardLimit
	default:
		return TimeoutKindParentDeadline
	}
}

func (i *Invoker) resolveDeclaredTimeout(tool *Tool, request *Request) (time.Duration, *Result) {
	policy, ok := tool.TimeoutPolicy()
	if !ok {
		return 0, nil
	}
	timeout := policy.Default
	if policy.Argument != "" && request != nil && request.Arguments != nil {
		if raw, exists := request.Arguments[policy.Argument]; exists {
			value, ok := raw.(string)
			if !ok {
				return 0, newInvalidTimeoutResult(policy.Argument, "must be a duration string")
			}
			var err error
			timeout, err = parseToolDuration(value)
			if err != nil {
				return 0, newInvalidTimeoutResult(policy.Argument, err.Error())
			}
		}
	}
	if timeout <= 0 {
		return 0, newInvalidTimeoutResult(policy.Argument, "must be greater than zero")
	}
	maxDeclared := i.maxDeclared
	if maxDeclared <= 0 {
		maxDeclared = MaxDeclaredToolTimeout
	}
	if timeout > maxDeclared {
		return 0, newInvalidTimeoutResult(policy.Argument, fmt.Sprintf("must not exceed %s", maxDeclared))
	}
	return timeout, nil
}

func (i *Invoker) executionContext(parent context.Context, declared time.Duration) (context.Context, context.CancelFunc, time.Duration, TimeoutKind) {
	hardLimit := i.hardLimit
	if hardLimit <= 0 {
		hardLimit = MaxToolExecutionDuration
	}
	timeout := hardLimit
	kind := TimeoutKindHardLimit
	cause := errToolHardLimit
	if declared > 0 && declared < timeout {
		timeout = declared
		kind = TimeoutKindDeclared
		cause = errToolDeclaredTimeout
	}
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= timeout {
			if remaining < 0 {
				remaining = 0
			}
			timeout = remaining
			kind = TimeoutKindParentDeadline
			cause = context.DeadlineExceeded
		}
	}
	ctx, cancel := context.WithTimeoutCause(parent, timeout, cause)
	return ctx, cancel, timeout, kind
}

func parseToolDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("must not be empty")
	}
	if _, err := strconv.Atoi(value); err == nil {
		value += "s"
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %w", err)
	}
	return duration, nil
}

func newInvalidTimeoutResult(argument, cause string) *Result {
	name := strings.TrimSpace(argument)
	if name == "" {
		name = "timeout"
	}
	result := NewToolResultActionableError(
		fmt.Sprintf("invalid %s: %s", name, cause),
		fmt.Sprintf("use a duration greater than zero and no more than %s; move longer work to a background task", MaxDeclaredToolTimeout),
	)
	result.ErrorCode = "invalid_argument"
	return result
}

func newTimeoutResult(toolName string, kind TimeoutKind, timeout, elapsed time.Duration) *Result {
	result := NewToolResultActionableError(
		fmt.Sprintf("tool %s timed out after %s (%s)", toolName, timeout.Round(time.Millisecond), kind),
		"the operation may have partially completed; verify current state before retrying, and use a background task for long-running commands",
	)
	result.Status = ResultStatusTimedOut
	result.ErrorCode = "tool_timeout"
	result.TimedOut = true
	result.TimeoutKind = kind
	result.TimeoutMs = timeout.Milliseconds()
	result.ElapsedMs = elapsed.Milliseconds()
	result.Retryable = false
	return result
}

func newCancelledResult(toolName string, elapsed time.Duration) *Result {
	result := NewToolResultActionableError(
		fmt.Sprintf("tool %s was cancelled before completion", toolName),
		"the operation may have partially completed; verify current state before retrying",
	)
	result.Status = ResultStatusCancelled
	result.ErrorCode = "tool_cancelled"
	result.Cancelled = true
	result.ElapsedMs = elapsed.Milliseconds()
	result.Retryable = false
	return result
}

func normalizeResultStatus(result *Result) {
	switch {
	case result.TimedOut || result.Status == ResultStatusTimedOut:
		result.Status = ResultStatusTimedOut
		result.TimedOut = true
		result.IsError = true
	case result.Cancelled || result.Status == ResultStatusCancelled:
		result.Status = ResultStatusCancelled
		result.Cancelled = true
		result.IsError = true
	case result.IsError || result.Status == ResultStatusError:
		result.Status = ResultStatusError
		result.IsError = true
	case result.Status == "":
		result.Status = ResultStatusSuccess
	}
}
