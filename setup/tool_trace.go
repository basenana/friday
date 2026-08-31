package setup

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/tools"
	"github.com/google/uuid"
)

type ToolTraceKind string

const (
	ToolTraceStart ToolTraceKind = "start"
	ToolTraceEnd   ToolTraceKind = "end"
	ToolTraceError ToolTraceKind = "error"
)

type ToolTraceEvent struct {
	Kind                ToolTraceKind
	InvocationID        string
	Attempt             int
	RetryOfInvocationID string
	Required            *bool
	RequiredSource      string
	Name                string
	Arguments           map[string]interface{}
	Error               string
	Result              string        // Truncated tool output (end/error events only)
	Duration            time.Duration // Execution duration (end/error events only)
	IsSystemError       bool          // true when handler returned Go error (not result.IsError)
	ExitCode            *int
	Cancelled           bool
	OccurredAt          time.Time
}

const maxToolTraceResultLen = 10000

type toolInvocationContextKey struct{}
type toolInvocationEvidenceContextKey struct{}

type ToolInvocationEvidence struct {
	InvocationID        string
	Attempt             int
	RetryOfInvocationID string
	Required            *bool
	RequiredSource      string
}

func WithToolInvocationEvidence(ctx context.Context, evidence ToolInvocationEvidence) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolInvocationEvidenceContextKey{}, evidence)
}

func InvocationEvidenceFromContext(ctx context.Context) ToolInvocationEvidence {
	evidence, ok := toolInvocationEvidenceFromContext(ctx)
	if !ok {
		return ToolInvocationEvidence{Attempt: 1}
	}
	return evidence
}

func toolInvocationEvidenceFromContext(ctx context.Context) (ToolInvocationEvidence, bool) {
	if ctx == nil {
		return ToolInvocationEvidence{}, false
	}
	evidence, ok := ctx.Value(toolInvocationEvidenceContextKey{}).(ToolInvocationEvidence)
	return evidence, ok
}

func validateToolInvocationEvidence(evidence ToolInvocationEvidence) error {
	if evidence.Attempt < 1 {
		return fmt.Errorf("tool invocation attempt must be at least 1")
	}
	if evidence.Attempt == 1 && evidence.RetryOfInvocationID != "" {
		return fmt.Errorf("tool invocation attempt 1 cannot reference a retry predecessor")
	}
	if evidence.Attempt > 1 && evidence.RetryOfInvocationID == "" {
		return fmt.Errorf("tool invocation attempt %d requires a retry predecessor", evidence.Attempt)
	}
	if evidence.Required == nil && evidence.RequiredSource != "" {
		return fmt.Errorf("tool invocation required source requires an explicit required value")
	}
	if evidence.Required != nil && evidence.RequiredSource == "" {
		return fmt.Errorf("tool invocation required value requires a source")
	}
	return nil
}

// InvocationIDFromContext returns the structured invocation currently executing.
func InvocationIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(toolInvocationContextKey{}).(string)
	return value
}

func wrapToolsWithTrace(toolList []*tools.Tool, trace func(ToolTraceEvent)) []*tools.Tool {
	if trace == nil || len(toolList) == 0 {
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
				evidence := InvocationEvidenceFromContext(ctx)
				invocationID := evidence.InvocationID
				if invocationID == "" {
					invocationID = uuid.NewString()
				}
				start := time.Now().UTC()
				if evidenceErr := validateToolInvocationEvidence(evidence); evidenceErr != nil {
					trace(ToolTraceEvent{
						Kind: ToolTraceStart, InvocationID: invocationID, Attempt: evidence.Attempt,
						RetryOfInvocationID: evidence.RetryOfInvocationID, Required: evidence.Required,
						RequiredSource: evidence.RequiredSource, Name: toolName, Arguments: traceToolArguments(request), OccurredAt: start,
					})
					trace(ToolTraceEvent{
						Kind: ToolTraceError, InvocationID: invocationID, Attempt: evidence.Attempt,
						RetryOfInvocationID: evidence.RetryOfInvocationID, Required: evidence.Required,
						RequiredSource: evidence.RequiredSource, Name: toolName, Arguments: traceToolArguments(request),
						Error: evidenceErr.Error(), Result: evidenceErr.Error(), IsSystemError: true, OccurredAt: time.Now().UTC(),
					})
					return nil, evidenceErr
				}
				trace(ToolTraceEvent{
					Kind:                ToolTraceStart,
					InvocationID:        invocationID,
					Attempt:             evidence.Attempt,
					RetryOfInvocationID: evidence.RetryOfInvocationID,
					Required:            evidence.Required,
					RequiredSource:      evidence.RequiredSource,
					Name:                toolName,
					Arguments:           traceToolArguments(request),
					OccurredAt:          start,
				})

				result, err := handler(context.WithValue(ctx, toolInvocationContextKey{}, invocationID), request)
				duration := time.Since(start)
				finishedAt := time.Now().UTC()
				var resultExitCode *int
				if result != nil {
					resultExitCode = result.ExitCode
				}

				if err != nil {
					cancelled := errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
					trace(ToolTraceEvent{
						Kind:                ToolTraceError,
						InvocationID:        invocationID,
						Attempt:             evidence.Attempt,
						RetryOfInvocationID: evidence.RetryOfInvocationID,
						Required:            evidence.Required,
						RequiredSource:      evidence.RequiredSource,
						Name:                toolName,
						Arguments:           traceToolArguments(request),
						Error:               err.Error(),
						Result:              truncateString(summarizeToolError(result), maxToolTraceResultLen),
						Duration:            duration,
						IsSystemError:       true,
						Cancelled:           cancelled,
						OccurredAt:          finishedAt,
					})
					return result, err
				}
				if result != nil && result.IsError {
					trace(ToolTraceEvent{
						Kind:                ToolTraceError,
						InvocationID:        invocationID,
						Attempt:             evidence.Attempt,
						RetryOfInvocationID: evidence.RetryOfInvocationID,
						Required:            evidence.Required,
						RequiredSource:      evidence.RequiredSource,
						Name:                toolName,
						Arguments:           traceToolArguments(request),
						Error:               summarizeToolError(result),
						Result:              truncateString(summarizeToolError(result), maxToolTraceResultLen),
						Duration:            duration,
						IsSystemError:       false,
						ExitCode:            resultExitCode,
						Cancelled:           result.Cancelled,
						OccurredAt:          finishedAt,
					})
				} else {
					trace(ToolTraceEvent{
						Kind:                ToolTraceEnd,
						InvocationID:        invocationID,
						Attempt:             evidence.Attempt,
						RetryOfInvocationID: evidence.RetryOfInvocationID,
						Required:            evidence.Required,
						RequiredSource:      evidence.RequiredSource,
						Name:                toolName,
						Arguments:           traceToolArguments(request),
						Result:              truncateString(summarizeToolResult(result), maxToolTraceResultLen),
						ExitCode:            resultExitCode,
						Duration:            duration,
						OccurredAt:          finishedAt,
					})
				}
				return result, nil
			}
		}(tool.Name, originalHandler)
		wrapped = append(wrapped, &clone)
	}

	return wrapped
}

// maskedToolArgumentValue replaces sensitive argument values in trace events.
const maskedToolArgumentValue = "[REDACTED]"

var sensitiveArgumentKeyMarkers = []string{
	"token", "key", "secret", "password", "credential", "authorization",
}

func isSensitiveToolArgumentKey(key string) bool {
	lower := strings.ToLower(key)
	for _, marker := range sensitiveArgumentKeyMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// sensitiveAssignmentPattern matches env-style assignments such as
// `API_KEY=abc123` embedded in argument values (e.g. bash command strings).
var sensitiveAssignmentPattern = regexp.MustCompile(`(?i)\b([\w-]*(?:token|key|secret|password|credential|authorization)[\w-]*)\s*=\s*("[^"]*"|'[^']*'|\S+)`)

// sensitiveHeaderPattern matches header/JSON-style `key: value` pairs such as
// `Authorization: Bearer tok123` embedded in argument values.
var sensitiveHeaderPattern = regexp.MustCompile(`(?i)\b((?:authorization|token|secret|password|credential|key)[\w-]*)\s*:\s*("[^"]*"|'[^']*'|\S+(?:\s+\S+)?)`)

// scrubSensitiveAssignments masks secret-looking assignments inside string
// argument values (e.g. `API_KEY=...` in a bash command).
func scrubSensitiveAssignments(text string) string {
	text = sensitiveAssignmentPattern.ReplaceAllString(text, "${1}="+maskedToolArgumentValue)
	return sensitiveHeaderPattern.ReplaceAllString(text, "${1}: "+maskedToolArgumentValue)
}

// traceToolArguments copies tool arguments for a trace event, masking
// sensitive values and capping string sizes like results are capped.
func traceToolArguments(request *tools.Request) map[string]interface{} {
	if request == nil || len(request.Arguments) == 0 {
		return map[string]interface{}{}
	}

	cloned := make(map[string]interface{}, len(request.Arguments))
	for key, value := range request.Arguments {
		if isSensitiveToolArgumentKey(key) {
			cloned[key] = maskedToolArgumentValue
			continue
		}
		if text, ok := value.(string); ok {
			cloned[key] = truncateString(scrubSensitiveAssignments(text), maxToolTraceResultLen)
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func summarizeToolError(result *tools.Result) string {
	if result == nil || len(result.Content) == 0 {
		return "tool returned an error"
	}
	for _, content := range result.Content {
		if text, ok := content.(tools.TextContent); ok {
			return text.Text
		}
	}
	return "tool returned an error"
}

func summarizeToolResult(result *tools.Result) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(tools.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Back off to a UTF-8 rune boundary so multi-byte characters are not split.
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...[truncated]"
}

// toolTraceLoggerSink returns a trace sink that emits events through the
// provided logger. The logger interface has no debug level, so events are
// tagged DEBUG and emitted at info level.
func toolTraceLoggerSink(l logger.Logger) func(ToolTraceEvent) {
	if l == nil {
		return nil
	}
	return func(evt ToolTraceEvent) {
		l.Infow("DEBUG tool trace",
			"kind", string(evt.Kind),
			"invocation_id", evt.InvocationID,
			"attempt", evt.Attempt,
			"retry_of", evt.RetryOfInvocationID,
			"name", evt.Name,
			"error", evt.Error,
			"duration_ms", evt.Duration.Milliseconds(),
			"cancelled", evt.Cancelled,
		)
	}
}
