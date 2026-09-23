package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/basenana/friday/core/tools"
)

const (
	bashToolName        = "bash"
	bashToolDescription = `Execute a non-interactive shell command in a sandboxed environment.

Use this for builds, tests, Git, grep, pipelines, and shell workflows that the native filesystem tools do not express. Prefer fs_list, fs_find, fs_search, fs_read, fs_write, fs_edit, and fs_delete for file operations because they provide structured results and stronger path checks.
Commands are executed with safety restrictions:
- Commands must be in the allow list
- Dangerous commands are blocked
- File system and network access may be restricted
- Commands have a timeout of at most 15 minutes; use background_task for longer work

Usage notes:
- Pass the raw shell text in command; do not wrap it in another bash -c
- Pipes, redirects, and compound commands are supported
- Avoid using bash commands that require interactive input
- If a command fails, analyze the error and try a different approach
- A command denied only because it is missing from the allow list automatically asks the user for approval (project-wide or one-time) and is retried as soon as it is approved
- Relative workdir values are resolved from the agent working directory`
)

// NewBashTool creates a new bash tool. approver may be nil, in which case
// missing-allowlist denials fall back to the headless actionable error.
func NewBashTool(exec *Executor, workdir string, approver *CommandApprover) *tools.Tool {
	return tools.NewTool(bashToolName,
		tools.WithDescription(bashToolDescription),
		tools.WithString("command", tools.Required(), tools.MinLength(1), tools.Description("Raw shell command text. Do not add an outer bash -c wrapper.")),
		tools.WithString("timeout", tools.Description("Timeout duration (e.g. '30s', '5m'), up to 15m. Default is from config.")),
		tools.WithString("workdir", tools.Description("Working directory, relative to the agent root or an allowed absolute path. Defaults to the agent root.")),
		tools.WithExample(map[string]interface{}{"command": "go test ./core/actor", "workdir": ".", "timeout": "5m"}),
		tools.WithToolTimeout(exec.parseTimeout(), "timeout"),
		tools.WithToolHandler(bashToolHandler(exec, workdir, approver)),
	)
}

// bashToolHandler creates the handler for the bash tool
func bashToolHandler(exec *Executor, baseWorkdir string, approver *CommandApprover) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		// Extract command (required)
		command, ok := req.Arguments["command"].(string)
		if !ok || command == "" {
			return tools.NewToolResultActionableError("command is required and must be a non-empty string", "provide the shell command in the command field and retry"), nil
		}

		// Resolve and validate optional workdir
		workdir, err := resolveExecutorToolWorkdir(exec, baseWorkdir, req.Arguments)
		if err != nil {
			return tools.NewToolResultActionableError(err.Error(), "use an existing directory inside the agent workdir and retry"), nil
		}

		// Build options
		opts := ExecOptions{
			Workdir: workdir,
			// The Invoker owns the declared timeout and passes its deadline in
			// ctx. Keep only the executor's independent safety ceiling here so
			// a per-call timeout longer than the configured default is honored.
			Timeout: tools.MaxDeclaredToolTimeout,
		}

		// Execute command
		result, err := exec.Run(ctx, command, opts)
		if err != nil {
			if errors.Is(err, ErrSandboxUnavailable) {
				return tools.NewToolResultActionableError(err.Error(), "enable a supported OS sandbox or explicitly run Friday inside a trusted outer sandbox"), nil
			}
			if IsDenied(err) {
				if approver != nil {
					// Missing-allowlist denials go through the interactive
					// approval flow; on approval the command is retried here
					// and its real output is returned to the model.
					result, err = approver.Request(ctx, exec, command, opts)
					if err == nil {
						return bashOutputResult(result), nil
					}
					if !IsDenied(err) && !IsApprovalDenied(err) {
						return nil, err // approval infrastructure failure
					}
				}
				return bashDenialResult(result, err), nil
			}
			return nil, err
		}

		return bashOutputResult(result), nil
	}
}

// bashOutputResult renders an executed command result for the model.
func bashOutputResult(result *Result) *tools.Result {
	var output strings.Builder
	if result.Stdout != "" {
		output.WriteString(result.Stdout)
		if result.StdoutTruncated {
			output.WriteString("\n[stdout truncated by capture limits]")
		}
	}
	if result.Stderr != "" {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString("stderr:\n")
		output.WriteString(result.Stderr)
		if result.StderrTruncated {
			output.WriteString("\n[stderr truncated by capture limits]")
		}
	}

	if result.TimedOut {
		toolResult := tools.NewToolResultActionableError(fmt.Sprintf("Command timed out.\n%s", output.String()), "increase timeout, reduce the command workload, or use background_task for long-running work")
		toolResult.ExitCode = &result.ExitCode
		return toolResult
	}

	if result.ExitCode != 0 {
		toolResult := tools.NewToolResultActionableError(fmt.Sprintf("Command exited with code %d.\n%s", result.ExitCode, output.String()), "inspect stdout/stderr, correct the command or its inputs, and retry")
		toolResult.ExitCode = &result.ExitCode
		return toolResult
	}

	if output.Len() == 0 {
		return tools.NewToolResultText("Command completed successfully with no output.")
	}

	return tools.NewToolResultText(output.String())
}

// bashDenialResult renders a permission denial as an actionable tool result
// with a suggestion that matches why the command was denied.
func bashDenialResult(result *Result, err error) *tools.Result {
	var denied *DeniedError
	cause := err.Error()
	if result != nil && strings.TrimSpace(result.Stderr) != "" {
		cause = result.Stderr
	}
	suggestion := "use an allowed command instead"
	switch {
	case IsApprovalDenied(err):
		suggestion = "the user declined this command; use an allowed command or a different approach"
	case IsApprovalTimeout(err):
		suggestion = "nobody answered the approval form in time and the command was denied by default; retry when the user is available, or ask them to run `friday sandbox allow <command>` in the project directory"
	case errors.As(err, &denied) && !denied.ExplicitDeny:
		// Denied only because it is missing from the allow list, with no
		// interactive approval available (headless run or unbound approver).
		suggestion = HeadlessSuggestion
	}
	return tools.NewToolResultActionableError(cause, suggestion)
}

// resolveToolWorkdir resolves the effective workdir for a tool call: the
// per-call "workdir" argument takes precedence over the base workdir, and the
// result is always validated (exists, is a directory, absolute). Generic
// callers stay inside the base workdir; executor-backed tools may additionally
// use an explicit sandbox write root such as a worktree's project code root.
func resolveToolWorkdir(base string, args map[string]interface{}) (string, error) {
	return resolveToolWorkdirWithPolicy(base, args, false)
}

func resolveExecutorToolWorkdir(exec *Executor, base string, args map[string]interface{}) (string, error) {
	disabled := exec != nil && exec.config != nil && exec.config.IsolationDisabled()
	workdir, err := resolveToolWorkdirWithPolicy(base, args, true)
	if err != nil || disabled {
		return workdir, err
	}
	if err := validateResolvedToolPath(exec.config, base, workdir, fsAccessWrite); err != nil {
		return "", fmt.Errorf("invalid workdir: %w", err)
	}
	return workdir, nil
}

func resolveToolWorkdirWithPolicy(base string, args map[string]interface{}, isolationDisabled bool) (string, error) {
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("invalid workdir: resolve current directory: %w", err)
		}
		base = cwd
	}

	baseAbs, err := ValidateWorkdir(base)
	if err != nil {
		return "", fmt.Errorf("invalid workdir: %w", err)
	}

	workdir := baseAbs
	if raw, ok := args["workdir"].(string); ok && strings.TrimSpace(raw) != "" {
		raw = strings.TrimSpace(raw)
		if !filepath.IsAbs(expandPath(raw, "", "")) {
			raw = filepath.Join(baseAbs, raw)
		}
		validated, err := ValidateWorkdir(raw)
		if err != nil {
			return "", fmt.Errorf("invalid workdir: %w", err)
		}
		workdir = validated
	}

	if !isolationDisabled && !pathWithinRoot(workdir, baseAbs) {
		return "", fmt.Errorf("invalid workdir: %q is outside the agent workdir %q", workdir, baseAbs)
	}
	return workdir, nil
}
