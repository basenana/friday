package codebase

import (
	"context"
	"strings"
	"time"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/shellcmd"
)

const readOnlyGitToolName = "codebase_git_history"

func newReadOnlyGitTool(exec *sandbox.Executor, root string) *tools.Tool {
	return tools.NewTool(readOnlyGitToolName,
		tools.WithDescription("Inspect bounded, read-only Git history for architectural rationale. This tool cannot modify the repository."),
		tools.WithString("operation", tools.Required(), tools.Enum("log", "pickaxe"), tools.Description("Use log for recent/path history or pickaxe to find commits that changed a literal string.")),
		tools.WithString("path", tools.MaxLength(4096), tools.Description("Optional project-relative path to limit history.")),
		tools.WithString("search", tools.MaxLength(1000), tools.Description("Literal text required for pickaxe, or optional commit-message text for log.")),
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			op, _ := request.Arguments["operation"].(string)
			path, _ := request.Arguments["path"].(string)
			search, _ := request.Arguments["search"].(string)
			path = strings.TrimSpace(path)
			search = strings.TrimSpace(search)
			if path != "" {
				if err := validateGitPath(path); err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}
			}
			args := []string{"log", "--date=short", "--format=%H%x09%ad%x09%s", "-n", "30"}
			switch op {
			case "log":
				if search != "" {
					args = append(args, "--grep="+search)
				}
			case "pickaxe":
				if search == "" {
					return tools.NewToolResultError("search is required for pickaxe history"), nil
				}
				args = append(args, "-S"+search)
			default:
				return tools.NewToolResultError("operation must be log or pickaxe"), nil
			}
			if path != "" {
				args = append(args, "--", path)
			}
			result, err := exec.Run(ctx, shellcmd.Join("git", args...), sandbox.ExecOptions{Workdir: root, Env: []string{"LC_ALL=C"}, Timeout: time.Minute})
			if err != nil {
				return tools.NewToolResultError(err.Error()), nil
			}
			if result.ExitCode != 0 {
				return tools.NewToolResultError(strings.TrimSpace(result.Stderr)), nil
			}
			if result.StdoutTruncated || result.StderrTruncated {
				return tools.NewToolResultError("Git history output exceeded the bounded capture limit"), nil
			}
			out := boundOutputChars(result.Stdout, 32_000)
			if out == "" {
				out = "No matching Git history found."
			}
			return tools.NewToolResultText(out), nil
		}),
	)
}
