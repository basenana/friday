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

const (
	FsReadToolName   = toolFsRead
	FsWriteToolName  = toolFsWrite
	FsListToolName   = toolFsList
	FsDeleteToolName = toolFsDelete
	FsMkdirToolName  = toolFsMkdir
	FsEditToolName   = toolFsEdit
)

type fsAccessMode int

const (
	fsAccessRead fsAccessMode = iota
	fsAccessWrite
)

type FileAccessMode int

const (
	FileAccessRead FileAccessMode = iota
	FileAccessWrite
)

// FileSystem is the domain-level backend used by the native filesystem tools.
// Resolve applies backend path and access policy and returns a canonical path;
// the remaining methods operate on paths returned by Resolve.
type FileSystem interface {
	Resolve(context.Context, string, FileAccessMode) (string, error)
	Stat(context.Context, string) (os.FileInfo, error)
	ReadFile(context.Context, string) ([]byte, error)
	ReadDir(context.Context, string) ([]os.DirEntry, error)
	WriteFile(context.Context, string, []byte) error
	Remove(context.Context, string) error
	Mkdir(context.Context, string) error
}

type localFileSystem struct {
	exec    *Executor
	workdir string
}

func NewLocalFileSystem(exec *Executor, workdir string) FileSystem {
	return &localFileSystem{exec: exec, workdir: workdir}
}

func (f *localFileSystem) Resolve(ctx context.Context, path string, mode FileAccessMode) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	access := fsAccessRead
	if mode == FileAccessWrite {
		access = fsAccessWrite
	}
	return resolveToolPath(f.exec.config, f.workdir, path, access)
}

func (f *localFileSystem) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Stat(path)
}

func (f *localFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (f *localFileSystem) ReadDir(ctx context.Context, path string) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadDir(path)
}

func (f *localFileSystem) WriteFile(ctx context.Context, path string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeFileAtomic(path, content, 0o644)
}

func (f *localFileSystem) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func (f *localFileSystem) Mkdir(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

// NewFsTools creates file system tools that operate directly on the filesystem.
// workdir is the current working directory, which will be injected into tool descriptions.
func NewFsTools(exec *Executor, workdir string) []*tools.Tool {
	return NewFsToolsWithFileSystem(NewLocalFileSystem(exec, workdir), workdir)
}

// NewFsToolsWithFileSystem creates the native filesystem tools over fs.
func NewFsToolsWithFileSystem(fs FileSystem, workdir string) []*tools.Tool {
	return []*tools.Tool{
		newFsReadTool(fs, workdir),
		newFsWriteTool(fs, workdir),
		newFsListTool(fs, workdir),
		newFsDeleteTool(fs, workdir),
		newFsMkdirTool(fs, workdir),
		newFsEditTool(fs, workdir),
	}
}

func newFsReadTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Read the contents of a file.

Current working directory: %s

Parameters:
- path: relative to working directory, or absolute path`, workdir)

	return tools.NewTool(toolFsRead,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file"), tools.Required()),
		tools.WithToolHandler(fsReadFileSystemHandler(fs)),
	)
}

func fsReadHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsReadFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func fsReadFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultActionableError("path is required and must be a non-empty string", "provide a file path relative to the workdir or an allowed absolute path"), nil
		}

		absPath, err := fs.Resolve(ctx, path, FileAccessRead)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a readable path inside the allowed workdir"), nil
		}

		info, err := fs.Stat(ctx, absPath)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to inspect file %q: %s", path, err), "use fs_list to verify the path and then retry"), nil
		}
		if info.IsDir() {
			return tools.NewToolResultActionableError(fmt.Sprintf("cannot read %q as a file because it is a directory", path), "use fs_list for directories or provide a file path"), nil
		}

		content, err := fs.ReadFile(ctx, absPath)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to read file %q: %s", path, err), "verify that the file exists and is readable, then retry"), nil
		}

		return tools.NewToolResultText(truncateOutput(string(content))), nil
	}
}

func newFsWriteTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Parent directories are created automatically.

Current working directory: %s

Parameters:
- path: relative to working directory, or absolute path
- content: the content to write to the file`, workdir)

	return tools.NewTool(toolFsWrite,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file"), tools.Required()),
		tools.WithString("content", tools.Description("The content to write to the file"), tools.Required()),
		tools.WithToolHandler(fsWriteFileSystemHandler(fs)),
	)
}

func fsWriteHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsWriteFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func fsWriteFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultActionableError("path is required and must be a non-empty string", "provide the destination file path"), nil
		}
		content, ok := req.Arguments["content"].(string)
		if !ok {
			return tools.NewToolResultActionableError("content is required and must be a string", "provide content; use an empty string when intentionally writing an empty file"), nil
		}

		absPath, err := fs.Resolve(ctx, path, FileAccessWrite)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a writable path inside the allowed workdir"), nil
		}

		if err := fs.WriteFile(ctx, absPath, []byte(content)); err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to write file %q: %s", path, err), "verify the parent path and write permission, then retry"), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)), nil
	}
}

func newFsListTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`List files and directories in a directory. Use '.' to list the current directory.

Current working directory: %s

Parameters:
- path: the directory path to list. Use '.' for current directory.`, workdir)

	return tools.NewTool(toolFsList,
		tools.WithDescription(desc),
		tools.WithString("path", tools.DefaultString("."), tools.Description("Directory path. Defaults to the current directory.")),
		tools.WithToolHandler(fsListFileSystemHandler(fs)),
	)
}

func fsListHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsListFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func fsListFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			path = "."
		}

		absPath, err := fs.Resolve(ctx, path, FileAccessRead)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a readable directory inside the allowed workdir"), nil
		}

		entries, err := fs.ReadDir(ctx, absPath)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to list directory %q: %s", path, err), "verify that the path exists and is a readable directory"), nil
		}
		if len(entries) == 0 {
			return tools.NewToolResultText("Directory is empty"), nil
		}

		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}

		return tools.NewToolResultText(strings.Join(names, "\n")), nil
	}
}

func newFsDeleteTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Delete a file or directory. WARNING: This cannot be undone.

Current working directory: %s

Parameters:
- path: the path to the file or directory to delete`, workdir)

	return tools.NewTool(toolFsDelete,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The path to the file or directory to delete"), tools.Required()),
		tools.WithToolHandler(fsDeleteFileSystemHandler(fs)),
	)
}

func fsDeleteHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsDeleteFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func fsDeleteFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultActionableError("path is required and must be a non-empty string", "provide the file or directory path to delete"), nil
		}

		absPath, err := fs.Resolve(ctx, path, FileAccessWrite)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a writable path inside the allowed workdir"), nil
		}

		if err := fs.Remove(ctx, absPath); err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to delete %q: %s", path, err), "verify the path with fs_list and ensure deletion is permitted"), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully deleted %s", path)), nil
	}
}

func newFsMkdirTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Create a directory. Parent directories are created automatically.

Current working directory: %s

Parameters:
- path: the directory path to create`, workdir)

	return tools.NewTool(toolFsMkdir,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("The directory path to create"), tools.Required()),
		tools.WithToolHandler(fsMkdirFileSystemHandler(fs)),
	)
}

func fsMkdirHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsMkdirFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func fsMkdirFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultActionableError("path is required and must be a non-empty string", "provide the directory path to create"), nil
		}

		absPath, err := fs.Resolve(ctx, path, FileAccessWrite)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a writable path inside the allowed workdir"), nil
		}

		if err := fs.Mkdir(ctx, absPath); err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to create directory %q: %s", path, err), "verify the parent path and write permission, then retry"), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully created directory %s", path)), nil
	}
}

func newFsEditTool(fs FileSystem, workdir string) *tools.Tool {
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
		tools.WithToolHandler(fsEditFileSystemHandler(fs)),
	)
}

func fsEditHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsEditFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func fsEditFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, ok := req.Arguments["path"].(string)
		if !ok || path == "" {
			return tools.NewToolResultActionableError("path is required and must be a non-empty string", "provide the file path to edit"), nil
		}

		searchString, ok := req.Arguments["search_string"].(string)
		if !ok || searchString == "" {
			return tools.NewToolResultActionableError("search_string is required and must be a non-empty string", "provide text that exactly matches the current file contents"), nil
		}

		replaceString, ok := req.Arguments["replace_string"].(string)
		if !ok {
			return tools.NewToolResultActionableError("replace_string is required and must be a string", "provide replacement text; use an empty string to delete the match"), nil
		}

		occurrences, ok := req.Arguments["occurrences"].(string)
		if !ok {
			occurrences = ""
		}

		replaceAll := occurrences == "all"

		absPath, err := fs.Resolve(ctx, path, FileAccessWrite)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a writable file path inside the allowed workdir"), nil
		}

		fileInfo, err := fs.Stat(ctx, absPath)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to inspect file %q: %s", path, err), "use fs_list to verify the path and then retry"), nil
		}
		if fileInfo.IsDir() {
			return tools.NewToolResultActionableError(fmt.Sprintf("cannot edit %q because it is a directory", path), "use fs_list for directories or provide a file path"), nil
		}
		if fileInfo.Size() > maxEditFileSize {
			return tools.NewToolResultActionableError(fmt.Sprintf("file is too large to edit (%d bytes; maximum %d)", fileInfo.Size(), maxEditFileSize), "use bash with a targeted streaming edit or reduce the file size"), nil
		}

		content, err := fs.ReadFile(ctx, absPath)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to read file %q: %s", path, err), "verify that the file is readable and then retry"), nil
		}

		contentStr := string(content)
		count := strings.Count(contentStr, searchString)
		if count == 0 {
			return tools.NewToolResultActionableError(fmt.Sprintf("search_string was not found in %q: %q", path, truncateForError(searchString)), "call fs_read, copy the exact current text including whitespace, and retry"), nil
		}

		var newContent string
		var replacedCount int
		if replaceAll {
			newContent = strings.ReplaceAll(contentStr, searchString, replaceString)
			replacedCount = count
		} else {
			newContent = strings.Replace(contentStr, searchString, replaceString, 1)
			replacedCount = 1
		}

		if len(newContent) > maxEditFileSize {
			return tools.NewToolResultActionableError(fmt.Sprintf("edited file would be too large (%d bytes; maximum %d)", len(newContent), maxEditFileSize), "reduce the replacement size or use a different editing approach"), nil
		}

		if err := fs.WriteFile(ctx, absPath, []byte(newContent)); err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to write edited file %q: %s", path, err), "verify write permission and retry after confirming the file is unchanged"), nil
		}

		msg := fmt.Sprintf("Successfully replaced %d occurrence(s) in %s", replacedCount, path)
		if count > 1 && !replaceAll {
			msg += fmt.Sprintf(" (of %d total matches)", count)
		}

		return tools.NewToolResultText(msg), nil
	}
}

func resolveToolPath(cfg *Config, workdir, path string, mode fsAccessMode) (string, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	absPath, err := resolveLocalFsPath(workdir, path)
	if err != nil {
		return "", err
	}

	// Containment and deny checks below are path-based, so symlinks must be
	// resolved first: otherwise a symlink planted inside the workdir by a
	// previously allowed command could point at files outside it.
	absPath, err = resolveSymlinkedPath(absPath)
	if err != nil {
		return "", err
	}
	if cfg.IsolationDisabled() {
		return absPath, nil
	}
	resolvedWorkdir := workdir
	if strings.TrimSpace(workdir) != "" {
		if resolved, err := resolveSymlinkedPath(workdir); err == nil {
			resolvedWorkdir = resolved
		}
	}

	inWorkdir := isWithinWorkdir(resolvedWorkdir, absPath)
	inWriteRoots := matchesAnyPath(cfg.Sandbox.Filesystem.Write, resolvedWorkdir, absPath)
	inReadOnlyRoots := matchesAnyPath(cfg.Sandbox.Filesystem.ReadOnly, resolvedWorkdir, absPath)
	inProtectedRoots := matchesAnyPath(cfg.Sandbox.Filesystem.Protected, resolvedWorkdir, absPath)

	if matchesAnyPath(cfg.Sandbox.Filesystem.Deny, resolvedWorkdir, absPath) {
		return "", fmt.Errorf("path is denied by sandbox rules")
	}

	if mode == fsAccessWrite {
		if inProtectedRoots || inReadOnlyRoots {
			return "", fmt.Errorf("path is read-only")
		}
		if !inWorkdir && !inWriteRoots {
			return "", fmt.Errorf("path is outside writable roots")
		}
		return absPath, nil
	}

	if !inWorkdir && !inWriteRoots && !inReadOnlyRoots && !inProtectedRoots {
		return "", fmt.Errorf("path is outside readable roots")
	}

	return absPath, nil
}

func resolveLocalFsPath(workdir, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	resolved := expandPath(path, workdir, "")
	if !filepath.IsAbs(resolved) {
		absPath, err := filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
		resolved = absPath
	}

	return filepath.Clean(resolved), nil
}

// resolveSymlinkedPath resolves all symlinks in a path. If the final
// component does not exist (e.g. a file about to be created), the nearest
// existing ancestor is resolved and the remainder appended lexically. A
// dangling symlink is an error, because its destination cannot be verified
// against the sandbox policy.
func resolveSymlinkedPath(path string) (string, error) {
	// A symlink whose destination does not exist (dangling) must be rejected:
	// its target is outside the resolved parent and cannot be verified.
	if _, lstatErr := os.Lstat(path); lstatErr == nil {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("failed to resolve path %q: %w", path, err)
		}
		return resolved, nil
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to resolve path %q: %w", path, err)
	}

	parent := filepath.Dir(path)
	if parent == path {
		// Reached the filesystem root without finding an existing ancestor.
		return "", fmt.Errorf("failed to resolve path %q: %w", path, err)
	}
	resolvedParent, err := resolveSymlinkedPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

func isWithinWorkdir(workdir, absPath string) bool {
	if strings.TrimSpace(workdir) == "" {
		return false
	}

	workdirRoot, err := resolveLocalFsPath("", workdir)
	if err != nil {
		return false
	}

	return pathWithinRoot(absPath, workdirRoot)
}

func matchesAnyPath(patterns []string, workdir, absPath string) bool {
	for _, pattern := range patterns {
		expanded := expandPath(pattern, workdir, "")
		if !strings.ContainsAny(expanded, "*?[]") && !filepath.IsAbs(expanded) {
			absPattern, err := filepath.Abs(expanded)
			if err == nil {
				expanded = absPattern
			}
		}
		if matchesDeniedPath(expanded, absPath) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	cleanup = false
	return nil
}

// truncateForError truncates a long string for error messages (by runes to avoid breaking UTF-8)
func truncateForError(s string) string {
	runes := []rune(s)
	if len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	return s
}
