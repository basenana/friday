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
	bashToolName        = "bash"
	bashToolDescription = `Execute bash commands in a sandboxed environment.

IMPORTANT: Always use this tool for bash commands, even if you think you could answer directly.
Commands are executed with safety restrictions:
- Commands must be in the allow list
- Dangerous commands are blocked
- File system and network access may be restricted
- Commands have a timeout

Usage notes:
- Execute commands using bash -c, so you can use pipes, redirects, and compound commands
- Avoid using bash commands that require interactive input
- If a command fails, analyze the error and try a different approach
- Use absolute paths when possible for reliability`
)

// NewBashTool creates a new bash tool
func NewBashTool(exec *Executor) *tools.Tool {
	permissionBuf := bytes.NewBuffer(nil)

	if len(exec.config.Permissions.Allow) > 0 {
		permissionBuf.WriteString("Allowed commands:\n")
		for _, cmd := range exec.config.Permissions.Allow {
			permissionBuf.WriteString("- " + cmd + "\n")
		}
	}
	if len(exec.config.Permissions.Deny) > 0 {
		permissionBuf.WriteString("Denied commands:\n")
		for _, cmd := range exec.config.Permissions.Deny {
			permissionBuf.WriteString("- " + cmd + "\n")
		}
	}

	desc := bashToolDescription
	if permissionBuf.Len() > 0 {
		desc = bashToolDescription + "\n\n" + permissionBuf.String()
	}

	return tools.NewTool(bashToolName,
		tools.WithDescription(desc),
		tools.WithString("command", tools.Required(), tools.Description("The bash command to execute")),
		tools.WithString("timeout", tools.Description("Timeout duration (e.g. '30s', '5m'). Default is from config.")),
		tools.WithString("workdir", tools.Description("Working directory. Default is current directory.")),
		tools.WithToolHandler(bashToolHandler(exec)),
	)
}

// bashToolHandler creates the handler for the bash tool
func bashToolHandler(exec *Executor) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		// Extract command (required)
		command, ok := req.Arguments["command"].(string)
		if !ok || command == "" {
			return tools.NewToolResultError("command is required"), nil
		}

		// Extract optional timeout
		var timeout string
		if t, ok := req.Arguments["timeout"].(string); ok {
			timeout = t
		}

		// Extract optional workdir
		var workdir string
		if w, ok := req.Arguments["workdir"].(string); ok {
			workdir = w
		}

		// Build options
		opts := ExecOptions{
			Workdir: workdir,
		}

		// Parse timeout if provided
		if timeout != "" {
			d, err := parseDuration(timeout)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("invalid timeout: %v", err)), nil
			}
			opts.Timeout = d
		}

		// Execute command
		result, err := exec.Run(ctx, command, opts)
		if err != nil {
			if IsDenied(err) {
				return tools.NewToolResultError(result.Stderr), nil
			}
			return nil, err
		}

		// Build response
		var output strings.Builder
		if result.Stdout != "" {
			output.WriteString(result.Stdout)
		}
		if result.Stderr != "" {
			if output.Len() > 0 {
				output.WriteString("\n")
			}
			output.WriteString("stderr:\n")
			output.WriteString(result.Stderr)
		}

		if result.TimedOut {
			return tools.NewToolResultError(fmt.Sprintf("Command timed out.\n%s", output.String())), nil
		}

		if result.ExitCode != 0 {
			return tools.NewToolResultError(fmt.Sprintf("Command exited with code %d.\n%s", result.ExitCode, output.String())), nil
		}

		if output.Len() == 0 {
			return tools.NewToolResultText("Command completed successfully with no output."), nil
		}

		return tools.NewToolResultText(output.String()), nil
	}
}

// parseDuration parses a duration string, handling common formats
func parseDuration(s string) (time.Duration, error) {
	// Handle plain numbers as seconds
	if _, err := fmt.Sscanf(s, "%d", new(int)); err == nil {
		s = s + "s"
	}
	return time.ParseDuration(s)
}
