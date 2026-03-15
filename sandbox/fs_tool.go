package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/tools"
)

const (
	toolFsRead   = "fs_read"
	toolFsWrite  = "fs_write"
	toolFsList   = "fs_list"
	toolFsDelete = "fs_delete"
	toolFsMkdir  = "fs_mkdir"
)

// NewFsTools creates file system tools that execute through the sandbox.
// workdir is the current working directory, which will be injected into tool descriptions.
func NewFsTools(exec *Executor, workdir string) []*tools.Tool {
	return []*tools.Tool{
		newFsReadTool(exec, workdir),
		newFsWriteTool(exec, workdir),
		newFsListTool(exec, workdir),
		newFsDeleteTool(exec, workdir),
		newFsMkdirTool(exec, workdir),
	}
}

func newFsReadTool(exec *Executor, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Read the contents of a file.

Current working directory: %s

Parameters:
- path: relative to working directory, or absolute path`, workdir)

	return tools.NewTool(toolFsRead,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file"), tools.Required()),
		tools.WithToolHandler(fsReadHandler(exec)),
	)
}

func fsReadHandler(exec *Executor) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultError("path is required"), nil
		}

		cmd := fmt.Sprintf("cat %s", escapePath(path))
		result, err := exec.Run(ctx, cmd, ExecOptions{})
		if err != nil {
			if IsDenied(err) {
				return tools.NewToolResultError(result.Stderr), nil
			}
			return nil, err
		}

		if result.ExitCode != 0 {
			return tools.NewToolResultError(fmt.Sprintf("failed to read file: %s", result.Stderr)), nil
		}

		return tools.NewToolResultText(result.Stdout), nil
	}
}

func newFsWriteTool(exec *Executor, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Parent directories are created automatically.

Current working directory: %s

Parameters:
- path: relative to working directory, or absolute path
- content: the content to write to the file`, workdir)

	return tools.NewTool(toolFsWrite,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file"), tools.Required()),
		tools.WithString("content", tools.Description("The content to write to the file"), tools.Required()),
		tools.WithToolHandler(fsWriteHandler(exec)),
	)
}

func fsWriteHandler(exec *Executor) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultError("path is required"), nil
		}
		content, ok := req.Arguments["content"].(string)
		if !ok {
			return tools.NewToolResultError("content is required"), nil
		}

		// Use heredoc to write content, escaping single quotes in content
		escapedContent := strings.ReplaceAll(content, "'", "'\\''")
		cmd := fmt.Sprintf("mkdir -p $(dirname %s) && cat > %s << 'EOF'\n%s\nEOF", escapePath(path), escapePath(path), escapedContent)

		result, err := exec.Run(ctx, cmd, ExecOptions{})
		if err != nil {
			if IsDenied(err) {
				return tools.NewToolResultError(result.Stderr), nil
			}
			return nil, err
		}

		if result.ExitCode != 0 {
			return tools.NewToolResultError(fmt.Sprintf("failed to write file: %s", result.Stderr)), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)), nil
	}
}

func newFsListTool(exec *Executor, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`List files and directories in a directory. Use '.' to list the current directory.

Current working directory: %s

Parameters:
- path: the directory path to list. Use '.' for current directory.`, workdir)

	return tools.NewTool(toolFsList,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The directory path to list. Use '.' for current directory."), tools.Required()),
		tools.WithToolHandler(fsListHandler(exec)),
	)
}

func fsListHandler(exec *Executor) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			path = "."
		}

		cmd := fmt.Sprintf("ls -1 %s", escapePath(path))
		result, err := exec.Run(ctx, cmd, ExecOptions{})
		if err != nil {
			if IsDenied(err) {
				return tools.NewToolResultError(result.Stderr), nil
			}
			return nil, err
		}

		if result.ExitCode != 0 {
			return tools.NewToolResultError(fmt.Sprintf("failed to list directory: %s", result.Stderr)), nil
		}

		output := strings.TrimSpace(result.Stdout)
		if output == "" {
			return tools.NewToolResultText("Directory is empty"), nil
		}

		return tools.NewToolResultText(output), nil
	}
}

func newFsDeleteTool(exec *Executor, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Delete a file or directory. WARNING: This cannot be undone.

Current working directory: %s

Parameters:
- path: the path to the file or directory to delete`, workdir)

	return tools.NewTool(toolFsDelete,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file or directory to delete"), tools.Required()),
		tools.WithToolHandler(fsDeleteHandler(exec)),
	)
}

func fsDeleteHandler(exec *Executor) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultError("path is required"), nil
		}

		cmd := fmt.Sprintf("rm -rf %s", escapePath(path))
		result, err := exec.Run(ctx, cmd, ExecOptions{})
		if err != nil {
			if IsDenied(err) {
				return tools.NewToolResultError(result.Stderr), nil
			}
			return nil, err
		}

		if result.ExitCode != 0 {
			return tools.NewToolResultError(fmt.Sprintf("failed to delete: %s", result.Stderr)), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully deleted %s", path)), nil
	}
}

func newFsMkdirTool(exec *Executor, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Create a directory. Parent directories are created automatically.

Current working directory: %s

Parameters:
- path: the directory path to create`, workdir)

	return tools.NewTool(toolFsMkdir,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The directory path to create"), tools.Required()),
		tools.WithToolHandler(fsMkdirHandler(exec)),
	)
}

func fsMkdirHandler(exec *Executor) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultError("path is required"), nil
		}

		cmd := fmt.Sprintf("mkdir -p %s", escapePath(path))
		result, err := exec.Run(ctx, cmd, ExecOptions{})
		if err != nil {
			if IsDenied(err) {
				return tools.NewToolResultError(result.Stderr), nil
			}
			return nil, err
		}

		if result.ExitCode != 0 {
			return tools.NewToolResultError(fmt.Sprintf("failed to create directory: %s", result.Stderr)), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully created directory %s", path)), nil
	}
}

// escapePath escapes a path for safe shell usage
func escapePath(path string) string {
	// Wrap in single quotes and escape any single quotes
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
