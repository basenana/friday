package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/basenana/friday/core/tools"
)

const (
	toolFsRead   = "fs_read"
	toolFsWrite  = "fs_write"
	toolFsList   = "fs_list"
	toolFsDelete = "fs_delete"
	toolFsMkdir  = "fs_mkdir"
	toolFsEdit   = "fs_edit"

	// maxEditFileSize is the maximum file size allowed for editing (10MB)
	maxEditFileSize = 10 * 1024 * 1024
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
		newFsEditTool(workdir),
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

func newFsEditTool(workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Edit a file by searching and replacing text.

Current working directory: %s

Parameters:
- path: relative to working directory, or absolute path
- search_string: the text to search for (must match exactly)
- replace_string: the text to replace with
- occurrences: "first" (default) to replace only the first match, "all" to replace all matches

Usage notes:
- The search_string must match EXACTLY, including whitespace and line breaks
- If search_string is not found, the tool will return an error
- By default, only the first match is replaced; use occurrences="all" to replace all matches`, workdir)

	return tools.NewTool(toolFsEdit,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file"), tools.Required()),
		tools.WithString("search_string", tools.Description("The text to search for"), tools.Required()),
		tools.WithString("replace_string", tools.Description("The text to replace with"), tools.Required()),
		tools.WithString("occurrences", tools.Description(`Replace scope: "first" (default) or "all"`), tools.Enum("first", "all")),
		tools.WithToolHandler(fsEditHandler(workdir)),
	)
}

func fsEditHandler(workdir string) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		// 1. Parse and validate parameters with proper type checking
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultError("path is required and must be a string"), nil
		}

		searchString, ok := req.Arguments["search_string"].(string)
		if !ok || searchString == "" {
			return tools.NewToolResultError("search_string is required and must be a string"), nil
		}

		replaceString, ok := req.Arguments["replace_string"].(string)
		if !ok {
			return tools.NewToolResultError("replace_string must be a string"), nil
		}

		occurrences, ok := req.Arguments["occurrences"].(string)
		if !ok {
			occurrences = "" // default to "first"
		}

		// Default: replace only the first occurrence
		replaceAll := occurrences == "all"

		// 2. Resolve and validate file path
		absPath, err := resolveFsPath(workdir, path)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("invalid path: %s", err)), nil
		}

		// 3. Check file size before reading
		fileInfo, err := os.Stat(absPath)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("failed to stat file: %s", err)), nil
		}
		if fileInfo.Size() > maxEditFileSize {
			return tools.NewToolResultError(fmt.Sprintf("file too large (%d bytes), maximum allowed is %d bytes", fileInfo.Size(), maxEditFileSize)), nil
		}

		// 4. Read file
		content, err := os.ReadFile(absPath)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("failed to read file: %s", err)), nil
		}

		// 5. Search for matches
		contentStr := string(content)
		count := strings.Count(contentStr, searchString)

		if count == 0 {
			return tools.NewToolResultError(fmt.Sprintf("search_string not found in file: %q", truncateForError(searchString))), nil
		}

		// 6. Perform replacement
		var newContent string
		var replacedCount int
		if replaceAll {
			newContent = strings.ReplaceAll(contentStr, searchString, replaceString)
			replacedCount = count
		} else {
			newContent = strings.Replace(contentStr, searchString, replaceString, 1)
			replacedCount = 1
		}

		// 7. Check if new content exceeds size limit
		if len(newContent) > maxEditFileSize {
			return tools.NewToolResultError(fmt.Sprintf("result file too large (%d bytes), maximum allowed is %d bytes", len(newContent), maxEditFileSize)), nil
		}

		// 8. Write back to file
		err = os.WriteFile(absPath, []byte(newContent), 0644)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("failed to write file: %s", err)), nil
		}

		// 9. Build result message
		msg := fmt.Sprintf("Successfully replaced %d occurrence(s) in %s", replacedCount, path)
		if count > 1 && !replaceAll {
			msg += fmt.Sprintf(" (of %d total matches)", count)
		}

		return tools.NewToolResultText(msg), nil
	}
}

// resolveFsPath resolves a relative path to an absolute path and validates it stays within workdir
func resolveFsPath(workdir, path string) (string, error) {
	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath = filepath.Clean(filepath.Join(workdir, path))
	}

	// Security check: ensure path doesn't escape workdir
	relPath, err := filepath.Rel(workdir, absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve relative path: %w", err)
	}
	if strings.HasPrefix(relPath, "..") || strings.HasPrefix(relPath, "/") {
		return "", fmt.Errorf("path escapes working directory")
	}

	return absPath, nil
}

// truncateForError truncates a long string for error messages (by runes to avoid breaking UTF-8)
func truncateForError(s string) string {
	runes := []rune(s)
	if len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	return s
}

// escapePath escapes a path for safe shell usage
func escapePath(path string) string {
	// Wrap in single quotes and escape any single quotes
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
