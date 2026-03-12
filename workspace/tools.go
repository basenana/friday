package workspace

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

// FsTools returns file system tools that operate within the workspace
func (w *Workspace) FsTools() []*tools.Tool {
	return []*tools.Tool{
		w.fsReadTool(),
		w.fsWriteTool(),
		w.fsListTool(),
		w.fsDeleteTool(),
		w.fsMkdirTool(),
	}
}

func (w *Workspace) fsReadTool() *tools.Tool {
	return tools.NewTool(toolFsRead,
		tools.WithDescription("Read the contents of a file from the workspace. Use this to examine existing files."),
		tools.WithString("path", tools.Description("The path to the file relative to workspace root"), tools.Required()),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			path, ok := req.Arguments["path"].(string)
			if !ok {
				return tools.NewToolResultError("path must be a string"), nil
			}

			content, err := w.Read(path)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("failed to read file: %s", err)), nil
			}

			return tools.NewToolResultText(content), nil
		}),
	)
}

func (w *Workspace) fsWriteTool() *tools.Tool {
	return tools.NewTool(toolFsWrite,
		tools.WithDescription("Write content to a file in the workspace. Creates the file if it doesn't exist, overwrites if it does. Parent directories are created automatically."),
		tools.WithString("path", tools.Description("The path to the file relative to workspace root"), tools.Required()),
		tools.WithString("content", tools.Description("The content to write to the file"), tools.Required()),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			path, ok := req.Arguments["path"].(string)
			if !ok {
				return tools.NewToolResultError("path must be a string"), nil
			}
			content, ok := req.Arguments["content"].(string)
			if !ok {
				return tools.NewToolResultError("content must be a string"), nil
			}

			if err := w.Write(path, content); err != nil {
				return tools.NewToolResultError(fmt.Sprintf("failed to write file: %s", err)), nil
			}

			return tools.NewToolResultText(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)), nil
		}),
	)
}

func (w *Workspace) fsListTool() *tools.Tool {
	return tools.NewTool(toolFsList,
		tools.WithDescription("List files and directories in a workspace directory. Use '.' to list the workspace root."),
		tools.WithString("path", tools.Description("The directory path to list. Use '.' for root directory."), tools.Required()),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			path, ok := req.Arguments["path"].(string)
			if !ok {
				return tools.NewToolResultError("path must be a string"), nil
			}

			if path == "" || path == "." {
				path = ""
			}

			entries, err := w.Ls(path)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("failed to list directory: %s", err)), nil
			}

			if len(entries) == 0 {
				return tools.NewToolResultText("Directory is empty"), nil
			}

			return tools.NewToolResultText(strings.Join(entries, "\n")), nil
		}),
	)
}

func (w *Workspace) fsDeleteTool() *tools.Tool {
	return tools.NewTool(toolFsDelete,
		tools.WithDescription("Delete a file or directory from the workspace. WARNING: This cannot be undone."),
		tools.WithString("path", tools.Description("The path to the file or directory to delete"), tools.Required()),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			path, ok := req.Arguments["path"].(string)
			if !ok {
				return tools.NewToolResultError("path must be a string"), nil
			}

			if err := w.Delete(path); err != nil {
				return tools.NewToolResultError(fmt.Sprintf("failed to delete: %s", err)), nil
			}

			return tools.NewToolResultText(fmt.Sprintf("Successfully deleted %s", path)), nil
		}),
	)
}

func (w *Workspace) fsMkdirTool() *tools.Tool {
	return tools.NewTool(toolFsMkdir,
		tools.WithDescription("Create a directory in the workspace. Parent directories are created automatically."),
		tools.WithString("path", tools.Description("The directory path to create"), tools.Required()),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			path, ok := req.Arguments["path"].(string)
			if !ok {
				return tools.NewToolResultError("path must be a string"), nil
			}

			if err := w.MkdirAll(path); err != nil {
				return tools.NewToolResultError(fmt.Sprintf("failed to create directory: %s", err)), nil
			}

			return tools.NewToolResultText(fmt.Sprintf("Successfully created directory %s", path)), nil
		}),
	)
}
