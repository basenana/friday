package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basenana/friday/core/tools"
)

const (
	pollWaitToolName              = "poll_wait"
	pollWaitDefaultInterval       = 5 * time.Second
	pollWaitAttemptTimeoutCeiling = 30 * time.Second
	pollWaitToolDescription       = `Execute bash commands in a sandboxed environment until they exit successfully.

IMPORTANT: Use this tool when a command may need to be retried until some external condition becomes ready.
Commands are executed with the same safety restrictions as the bash tool:
- Commands must be in the allow list
- Dangerous commands are blocked
- File system and network access may be restricted
- Each attempt has its own timeout
- Polling stops when max_timeout is reached

Usage notes:
- Execute commands using bash -c, so you can use pipes, redirects, and compound commands
- Avoid using bash commands that require interactive input
- Permission issues, invalid arguments, and invalid working directories fail immediately
- Non-zero exits and single-attempt timeouts are retried until max_timeout`
)

type execRunner interface {
	Run(ctx context.Context, cmd string, opts ExecOptions) (*Result, error)
}

// NewPollWaitTool creates a new polling bash tool.
func NewPollWaitTool(exec *Executor, workdir string) *tools.Tool {
	defaultMaxTimeout := exec.parseTimeout()
	defaultAttemptTimeout := defaultPollWaitAttemptTimeout(defaultMaxTimeout)

	desc := pollWaitToolDescription
	if permissions := buildPermissionDescription(exec); permissions != "" {
		desc += "\n\n" + permissions
	}

	return tools.NewTool(pollWaitToolName,
		tools.WithDescription(desc),
		tools.WithString("command", tools.Required(), tools.Description("The bash command to execute until it succeeds")),
		tools.WithString("interval",
			tools.DefaultString(pollWaitDefaultInterval.String()),
			tools.Description("Delay between retries. Default is 5s."),
		),
		tools.WithString("attempt_timeout",
			tools.DefaultString(defaultAttemptTimeout.String()),
			tools.Description("Timeout for each individual attempt."),
		),
		tools.WithString("max_timeout",
			tools.DefaultString(defaultMaxTimeout.String()),
			tools.Description("Maximum total wait time before giving up."),
		),
		tools.WithString("workdir", tools.Description("Working directory. Default is the agent workdir.")),
		tools.WithToolHandler(newPollWaitToolHandler(exec, workdir, defaultAttemptTimeout, defaultMaxTimeout)),
	)
}

func newPollWaitToolHandler(runner execRunner, baseWorkdir string, defaultAttemptTimeout, defaultMaxTimeout time.Duration) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		command, ok := req.Arguments["command"].(string)
		if !ok || strings.TrimSpace(command) == "" {
			return tools.NewToolResultError("command is required"), nil
		}

		interval, err := parseOptionalPositiveDuration(req.Arguments, "interval", pollWaitDefaultInterval)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		attemptTimeout, err := parseOptionalPositiveDuration(req.Arguments, "attempt_timeout", defaultAttemptTimeout)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		maxTimeout, err := parseOptionalPositiveDuration(req.Arguments, "max_timeout", defaultMaxTimeout)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		workdir, err := resolveToolWorkdir(baseWorkdir, req.Arguments)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		overallCtx, cancel := context.WithTimeout(ctx, maxTimeout)
		defer cancel()

		var (
			attempts   int
			lastResult *Result
		)

		for {
			if overallCtx.Err() != nil {
				break
			}

			attempts++
			result, err := runner.Run(overallCtx, command, ExecOptions{
				Workdir: workdir,
				Timeout: attemptTimeout,
			})
			if err != nil {
				if IsDenied(err) {
					if result != nil && strings.TrimSpace(result.Stderr) != "" {
						return tools.NewToolResultError(result.Stderr), nil
					}
					return tools.NewToolResultError("Permission denied"), nil
				}
				return tools.NewToolResultError(fmt.Sprintf("command execution failed: %v", err)), nil
			}
			if result == nil {
				return tools.NewToolResultError("command execution failed: empty result"), nil
			}

			lastResult = result
			if result.ExitCode == 0 && !result.TimedOut {
				return newPollWaitSuccessResult(attempts, result), nil
			}

			if !waitForNextAttempt(overallCtx, interval) {
				break
			}
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return tools.NewToolResultError(newPollWaitTimeoutMessage(maxTimeout, attempts, lastResult)), nil
	}
}

func defaultPollWaitAttemptTimeout(defaultMaxTimeout time.Duration) time.Duration {
	if defaultMaxTimeout <= 0 {
		return pollWaitAttemptTimeoutCeiling
	}
	if defaultMaxTimeout < pollWaitAttemptTimeoutCeiling {
		return defaultMaxTimeout
	}
	return pollWaitAttemptTimeoutCeiling
}

func parseOptionalPositiveDuration(args map[string]interface{}, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := args[key].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	d, err := parseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %v", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s: must be greater than zero", key)
	}
	return d, nil
}

func waitForNextAttempt(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func newPollWaitSuccessResult(attempts int, result *Result) *tools.Result {
	output := buildCommandOutput(result)
	if output == "" {
		return tools.NewToolResultText(fmt.Sprintf("Command succeeded after %d attempt(s) with no output.", attempts))
	}
	return tools.NewToolResultText(fmt.Sprintf("Command succeeded after %d attempt(s).\n%s", attempts, output))
}

func newPollWaitTimeoutMessage(maxTimeout time.Duration, attempts int, lastResult *Result) string {
	var output bytes.Buffer
	output.WriteString(fmt.Sprintf("Polling timed out after %s and %d attempt(s).", maxTimeout, attempts))

	if tail := buildCommandOutput(lastResult); tail != "" {
		output.WriteString("\n")
		output.WriteString(tail)
	}

	return output.String()
}
