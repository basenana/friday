package sandbox

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/tools"
)

const (
	toolFsRead   = "fs_read"
	toolFsWrite  = "fs_write"
	toolFsList   = "fs_list"
	toolFsFind   = "fs_find"
	toolFsSearch = "fs_search"
	toolFsDelete = "fs_delete"
	toolFsEdit   = "fs_edit"

	maxFindMatches       = 1000
	maxFindOutputBytes   = 512 * 1024
	maxSearchMatches     = 1000
	maxSearchOutputBytes = 512 * 1024
)

const (
	FsReadToolName   = toolFsRead
	FsWriteToolName  = toolFsWrite
	FsListToolName   = toolFsList
	FsFindToolName   = toolFsFind
	FsSearchToolName = toolFsSearch
	FsDeleteToolName = toolFsDelete
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

type recursiveFileRemover interface {
	RemoveAll(context.Context, string) error
}

type linkReader interface {
	Readlink(context.Context, string) (string, error)
}

type linkStatter interface {
	Lstat(context.Context, string) (os.FileInfo, error)
}

type fileOpener interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type findDirectoryRoot interface {
	ReadDir(context.Context, string) ([]os.DirEntry, error)
	Close() error
}

type findDirectoryRootOpener interface {
	OpenFindRoot(context.Context, string, os.FileInfo) (findDirectoryRoot, error)
}

type exactFileEditor interface {
	EditExact(context.Context, string, string, string, bool) (int, error)
}

type deletePathResolver interface {
	ResolveDelete(context.Context, string) (string, error)
	ValidateDeleteTarget(string) error
}

type localFileSystem struct {
	exec    *Executor
	workdir string

	// rootResolveOnce caches resolveSymlinkedPath(workdir). Search scans now
	// run concurrently and used to repeat that walk-the-path syscall for every
	// file operation and every rendered match path.
	rootResolveOnce sync.Once
	rootPath        string
	rootResolveErr  error
}

// resolvedWorkdirRoot returns the symlink-resolved workdir, computed at most
// once per filesystem instance. The workdir is fixed for the instance's
// lifetime, so caching cannot change behavior.
func (f *localFileSystem) resolvedWorkdirRoot() (string, error) {
	f.rootResolveOnce.Do(func() {
		f.rootPath, f.rootResolveErr = resolveSymlinkedPath(f.workdir)
	})
	return f.rootPath, f.rootResolveErr
}

func NewLocalFileSystem(exec *Executor, workdir string) FileSystem {
	return &localFileSystem{exec: exec, workdir: workdir}
}

func (f *localFileSystem) openWorkdirRoot(path string) (*os.Root, string, bool, error) {
	if f == nil || f.exec == nil || f.exec.config == nil || f.exec.config.IsolationDisabled() {
		return nil, "", false, nil
	}
	rootPath, err := f.resolvedWorkdirRoot()
	if err != nil || !pathWithinRoot(path, rootPath) {
		return nil, "", false, err
	}
	rel, err := filepath.Rel(rootPath, path)
	if err != nil {
		return nil, "", false, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, "", false, err
	}
	return root, rel, true, nil
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
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return nil, err
	} else if ok {
		defer root.Close()
		return root.Stat(rel)
	}
	return os.Stat(path)
}

func (f *localFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return nil, err
	} else if ok {
		defer root.Close()
		return root.ReadFile(rel)
	}
	return os.ReadFile(path)
}

func (f *localFileSystem) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return nil, err
	} else if ok {
		file, openErr := root.Open(rel)
		if openErr != nil {
			root.Close()
			return nil, openErr
		}
		return &rootedReadCloser{File: file, root: root}, nil
	}
	return os.Open(path)
}

func (f *localFileSystem) ReadDir(ctx context.Context, path string) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return nil, err
	} else if ok {
		defer root.Close()
		dir, openErr := root.Open(rel)
		if openErr != nil {
			return nil, openErr
		}
		defer dir.Close()
		return dir.ReadDir(-1)
	}
	return os.ReadDir(path)
}

type localFindDirectoryRoot struct {
	root     *os.Root
	rootPath string
}

func (f *localFileSystem) OpenFindRoot(ctx context.Context, rootPath string, expected os.FileInfo) (findDirectoryRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	if expected == nil || !os.SameFile(expected, actual) {
		root.Close()
		return nil, fmt.Errorf("search root changed after validation")
	}
	return &localFindDirectoryRoot{root: root, rootPath: rootPath}, nil
}

func (r *localFindDirectoryRoot) ReadDir(ctx context.Context, path string) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(r.rootPath, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("directory escaped the search root")
	}
	dir, err := r.root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

func (r *localFindDirectoryRoot) Close() error {
	return r.root.Close()
}

func (f *localFileSystem) WriteFile(ctx context.Context, path string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return err
	} else if ok {
		defer root.Close()
		if info, statErr := root.Stat(rel); statErr == nil {
			perm = info.Mode().Perm()
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		return writeFileAtomicRoot(root, rel, content, perm)
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeFileAtomic(path, content, perm)
}

func (f *localFileSystem) EditExact(ctx context.Context, path, oldText, newText string, replaceAll bool) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return 0, err
	} else if ok {
		defer root.Close()
		info, statErr := root.Stat(rel)
		if statErr != nil {
			return 0, statErr
		}
		return editFileAtomicExactRoot(ctx, root, rel, []byte(oldText), []byte(newText), replaceAll, info.Mode().Perm())
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	} else {
		return 0, err
	}
	return editFileAtomicExact(ctx, path, []byte(oldText), []byte(newText), replaceAll, perm)
}

func (f *localFileSystem) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return err
	} else if ok {
		defer root.Close()
		return root.Remove(rel)
	}
	return os.Remove(path)
}

func (f *localFileSystem) RemoveAll(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return err
	} else if ok {
		defer root.Close()
		return root.RemoveAll(rel)
	}
	return os.RemoveAll(path)
}

func (f *localFileSystem) Readlink(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return "", err
	} else if ok {
		defer root.Close()
		return root.Readlink(rel)
	}
	return os.Readlink(path)
}

func (f *localFileSystem) Lstat(ctx context.Context, path string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return nil, err
	} else if ok {
		defer root.Close()
		return root.Lstat(rel)
	}
	return os.Lstat(path)
}

func (f *localFileSystem) ResolveDelete(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	absPath, err := resolveLocalFsPath(f.workdir, path)
	if err != nil {
		return "", err
	}
	parent, err := resolveSymlinkedPath(filepath.Dir(absPath))
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(parent, filepath.Base(absPath))
	if f.exec.config.IsolationDisabled() {
		return resolved, nil
	}
	if err := validateResolvedToolPath(f.exec.config, f.workdir, resolved, fsAccessWrite); err != nil {
		return "", err
	}
	return resolved, nil
}

func (f *localFileSystem) ValidateDeleteTarget(path string) error {
	clean := filepath.Clean(path)
	if filepath.Dir(clean) == clean || sameCanonicalPath(path, f.workdir) || isFilesystemPolicyRoot(f.exec.config, f.workdir, path) {
		return fmt.Errorf("refusing to delete a filesystem authorization root")
	}
	return nil
}

func (f *localFileSystem) Mkdir(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if root, rel, ok, err := f.openWorkdirRoot(path); err != nil {
		return err
	} else if ok {
		defer root.Close()
		return root.MkdirAll(rel, 0o755)
	}
	return os.MkdirAll(path, 0o755)
}

type rootedReadCloser struct {
	*os.File
	root *os.Root
}

func (r *rootedReadCloser) Close() error {
	fileErr := r.File.Close()
	rootErr := r.root.Close()
	if fileErr != nil {
		return fileErr
	}
	return rootErr
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
		newFsFindTool(fs, workdir),
		newFsSearchTool(fs, workdir),
		newFsDeleteTool(fs, workdir),
		newFsEditTool(fs, workdir),
	}
}

func newFsReadTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Read one existing regular text file.

Current working directory: %s

Use this after confirming the path with fs_list, fs_find, or fs_search. Use fs_list for direct directory inspection, fs_find to locate names or paths recursively, and fs_search to find text across files. By default the complete file is returned. Set the optional 1-based inclusive start_line and end_line fields to read only part of a file. Output is limited only by the shared tool-result budget; any truncation is reported explicitly.`, workdir)

	return tools.NewTool(toolFsRead,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("Existing file path, relative to the current working directory or an allowed absolute path."), tools.MinLength(1), tools.Required()),
		tools.WithInteger("start_line", tools.Description("Optional 1-based first line to return, inclusive."), tools.Min(1)),
		tools.WithInteger("end_line", tools.Description("Optional 1-based last line to return, inclusive."), tools.Min(1)),
		tools.WithExample(map[string]interface{}{"path": "core/actor/inbox.go"}),
		tools.WithExample(map[string]interface{}{"path": "core/actor/inbox.go", "start_line": 120, "end_line": 180}),
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
		startLine, hasStart, err := optionalPositiveLineArgument(req.Arguments, "start_line")
		if err != nil {
			return tools.NewToolResultActionableError(err.Error(), "provide a positive integer line number"), nil
		}
		endLine, hasEnd, err := optionalPositiveLineArgument(req.Arguments, "end_line")
		if err != nil {
			return tools.NewToolResultActionableError(err.Error(), "provide a positive integer line number"), nil
		}
		if hasStart && hasEnd && startLine > endLine {
			return tools.NewToolResultActionableError("start_line must be less than or equal to end_line", "swap the line bounds or omit one of them"), nil
		}

		absPath, err := fs.Resolve(ctx, path, FileAccessRead)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a readable path inside the allowed workdir"), nil
		}

		info, err := fs.Stat(ctx, absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return tools.NewToolResultActionableError(missingPathCause(ctx, fs, path, absPath), "use fs_list, fs_find, or fs_search from the nearest existing directory; do not repeat the same guessed path"), nil
			}
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to inspect file %q: %s", path, err), "use fs_list to verify the path and then retry"), nil
		}
		if info.IsDir() {
			return tools.NewToolResultActionableError(fmt.Sprintf("cannot read %q as a file because it is a directory", path), "use fs_list for directories or provide a file path"), nil
		}

		var content []byte
		var truncated bool
		if hasStart || hasEnd {
			var linesRead int
			content, truncated, linesRead, err = readFileLineRangeWithinBudget(ctx, fs, absPath, startLine, hasStart, endLine, hasEnd, req.MaxOutputChars)
			if err == nil && hasStart && linesRead < startLine {
				return tools.NewToolResultActionableError(
					fmt.Sprintf("start_line %d exceeds the file's %d lines", startLine, linesRead),
					"choose a start_line within the file or omit the line range",
				), nil
			}
		} else {
			content, truncated, err = readFileWithinBudget(ctx, fs, absPath, info.Size(), req.MaxOutputChars)
		}
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to read file %q: %s", path, err), "verify that the file exists and is readable, then retry"), nil
		}
		text := string(content)
		if truncated {
			text += fmt.Sprintf("\n[File content truncated by shared tool-result budget; file size is %d bytes]", info.Size())
		}
		return tools.NewToolResultText(text), nil
	}
}

func optionalPositiveLineArgument(arguments map[string]interface{}, name string) (int, bool, error) {
	raw, ok := arguments[name]
	if !ok || raw == nil {
		return 0, false, nil
	}

	maxInt := uint64(^uint(0) >> 1)
	value := reflect.ValueOf(raw)
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number := value.Int()
		if number >= 1 && uint64(number) <= maxInt {
			return int(number), true, nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number := value.Uint()
		if number >= 1 && number <= maxInt {
			return int(number), true, nil
		}
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if !math.IsNaN(number) && !math.IsInf(number, 0) && number >= 1 && math.Trunc(number) == number {
			converted := int(number)
			if converted >= 1 && float64(converted) == number {
				return converted, true, nil
			}
		}
	}
	return 0, false, fmt.Errorf("%s must be a positive integer", name)
}

func readFileLineRangeWithinBudget(ctx context.Context, fs FileSystem, path string, startLine int, hasStart bool, endLine int, hasEnd bool, maxChars int64) ([]byte, bool, int, error) {
	reader, err := openSearchFile(ctx, fs, path)
	if err != nil {
		return nil, false, 0, err
	}
	defer reader.Close()

	if !hasStart {
		startLine = 1
	}
	const noticeReserve = int64(160)
	visibleChars := maxChars
	if visibleChars > 0 {
		visibleChars -= noticeReserve
		if visibleChars < 1 {
			visibleChars = 1
		}
	}

	buffered := bufio.NewReader(reader)
	var content strings.Builder
	var contentRunes int64
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, lineNumber, err
		}
		line, readErr := buffered.ReadString('\n')
		if len(line) > 0 {
			lineNumber++
			if lineNumber >= startLine && (!hasEnd || lineNumber <= endLine) {
				if visibleChars <= 0 {
					content.WriteString(line)
				} else {
					lineRunes := []rune(line)
					remaining := visibleChars - contentRunes
					if int64(len(lineRunes)) > remaining {
						content.WriteString(string(lineRunes[:remaining]))
						return []byte(content.String()), true, lineNumber, nil
					}
					content.WriteString(line)
					contentRunes += int64(len(lineRunes))
				}
			}
		}
		if readErr == io.EOF || (hasEnd && lineNumber >= endLine) {
			break
		}
		if readErr != nil {
			return nil, false, lineNumber, readErr
		}
	}
	return []byte(content.String()), false, lineNumber, nil
}

func readFileWithinBudget(ctx context.Context, fs FileSystem, path string, size, maxChars int64) ([]byte, bool, error) {
	if maxChars <= 0 {
		content, err := fs.ReadFile(ctx, path)
		return content, false, err
	}
	const noticeReserve = int64(160)
	visibleChars := maxChars - noticeReserve
	if visibleChars < 1 {
		visibleChars = 1
	}
	var content []byte
	var err error
	if opener, ok := fs.(fileOpener); ok {
		reader, openErr := opener.Open(ctx, path)
		if openErr != nil {
			return nil, false, openErr
		}
		defer reader.Close()
		content, err = io.ReadAll(io.LimitReader(reader, visibleChars*utf8.UTFMax+1))
	} else {
		content, err = fs.ReadFile(ctx, path)
	}
	if err != nil {
		return nil, false, err
	}
	runes := []rune(string(content))
	truncated := int64(len(runes)) > visibleChars || size > int64(len(content))
	if int64(len(runes)) > visibleChars {
		runes = runes[:visibleChars]
		content = []byte(string(runes))
	}
	return content, truncated, nil
}

func newFsWriteTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Create a file or completely overwrite an existing file.

Current working directory: %s

Use fs_edit for a targeted change to an existing file. Parent directories are created automatically, existing permission bits are preserved, and an empty content string intentionally creates an empty file.`, workdir)

	return tools.NewTool(toolFsWrite,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("Destination file path, relative to the current working directory or an allowed absolute path."), tools.MinLength(1), tools.Required()),
		tools.WithString("content", tools.Description("Complete replacement content. Use an empty string only to write an empty file."), tools.Required()),
		tools.WithExample(map[string]interface{}{"path": "docs/notes.md", "content": "# Notes\n"}),
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
	desc := fmt.Sprintf(`Inspect the direct children of one existing directory.

Current working directory: %s

Use this before guessing a path or reading a file. This tool is not recursive; use fs_find to locate names or paths recursively, or fs_search to search text across a directory tree. Hidden entries are included. The result is a JSON string with stable, path-sorted entries and file metadata.`, workdir)

	return tools.NewTool(toolFsList,
		tools.WithDescription(desc),
		tools.WithString("path", tools.DefaultString("."), tools.Description("Existing directory path. Defaults to the current working directory.")),
		tools.WithExample(map[string]interface{}{"path": "core/actor"}),
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
			if os.IsNotExist(err) {
				return tools.NewToolResultActionableError(missingPathCause(ctx, fs, path, absPath), "list the nearest existing directory or use fs_find; do not repeat the same guessed path"), nil
			}
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to list directory %q: %s", path, err), "verify that the path exists and is a readable directory"), nil
		}
		display := newRootedDisplayPathFn(fs, path, absPath)
		result := fsListResult{Path: display(absPath), Entries: make([]fsListEntry, 0, len(entries))}
		for _, entry := range entries {
			entryPath := filepath.Join(absPath, entry.Name())
			info, infoErr := entry.Info()
			if entry.Type()&os.ModeSymlink != 0 {
				if statter, ok := fs.(linkStatter); ok {
					info, infoErr = statter.Lstat(ctx, entryPath)
				}
			}
			if infoErr != nil {
				item := fsListEntry{
					Name: entry.Name(), Path: display(entryPath), Type: "unknown", Error: infoErr.Error(),
				}
				if listResultWouldOverflow(result, item, req.MaxOutputChars) {
					result.Truncated = true
					break
				}
				result.Entries = append(result.Entries, item)
				continue
			}
			item := fsListEntry{
				Name: entry.Name(), Path: display(entryPath), Type: fileTypeName(info.Mode()),
				Mode: info.Mode().String(), SizeBytes: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
			}
			item.UID, item.GID = fileOwnership(info)
			if entry.Type()&os.ModeSymlink != 0 {
				if reader, ok := fs.(linkReader); ok {
					item.SymlinkTarget, _ = reader.Readlink(ctx, entryPath)
				}
			}
			if listResultWouldOverflow(result, item, req.MaxOutputChars) {
				result.Truncated = true
				break
			}
			result.Entries = append(result.Entries, item)
		}
		sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path })
		return jsonTextResult(result)
	}
}

type fsListResult struct {
	Path      string        `json:"path"`
	Entries   []fsListEntry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

type fsListEntry struct {
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Type          string  `json:"type"`
	Mode          string  `json:"mode,omitempty"`
	SizeBytes     int64   `json:"size_bytes,omitempty"`
	ModifiedAt    string  `json:"modified_at,omitempty"`
	SymlinkTarget string  `json:"symlink_target,omitempty"`
	UID           *uint64 `json:"uid,omitempty"`
	GID           *uint64 `json:"gid,omitempty"`
	Error         string  `json:"error,omitempty"`
}

func fileOwnership(info os.FileInfo) (*uint64, *uint64) {
	if info == nil || info.Sys() == nil {
		return nil, nil
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil, nil
	}
	read := func(name string) *uint64 {
		field := value.FieldByName(name)
		if !field.IsValid() {
			return nil
		}
		var number uint64
		switch field.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			number = field.Uint()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if field.Int() < 0 {
				return nil
			}
			number = uint64(field.Int())
		default:
			return nil
		}
		return &number
	}
	return read("Uid"), read("Gid")
}

func listResultWouldOverflow(result fsListResult, item fsListEntry, maxChars int64) bool {
	if maxChars <= 0 {
		return false
	}
	result.Entries = append(append([]fsListEntry(nil), result.Entries...), item)
	raw, _ := json.Marshal(result)
	return int64(len(raw)) > maxChars-256
}

func fileTypeName(mode os.FileMode) string {
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	default:
		return "other"
	}
}

func requestedDisplayPath(fs FileSystem, requested string) string {
	displayPath := filepath.ToSlash(filepath.Clean(requested))
	if local, ok := fs.(*localFileSystem); ok {
		if logical, err := resolveLocalFsPath(local.workdir, requested); err == nil {
			if workdir, rootErr := resolveLocalFsPath("", local.workdir); rootErr == nil {
				if rel, relErr := filepath.Rel(workdir, logical); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
					return filepath.ToSlash(rel)
				}
				return filepath.ToSlash(logical)
			}
		}
	}
	return displayPath
}

func newRootedDisplayPathFn(fs FileSystem, requested, resolvedRoot string) func(string) string {
	displayRoot := requestedDisplayPath(fs, requested)
	return func(path string) string {
		rel, err := filepath.Rel(resolvedRoot, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return displayToolPath(fs, path)
		}
		if rel == "." {
			return displayRoot
		}
		if displayRoot == "." {
			return filepath.ToSlash(rel)
		}
		return filepath.ToSlash(filepath.Join(displayRoot, rel))
	}
}

func displayToolPath(fs FileSystem, path string) string {
	if local, ok := fs.(*localFileSystem); ok {
		root, err := local.resolvedWorkdirRoot()
		if err == nil {
			if rel, relErr := filepath.Rel(root, path); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				if rel == "." {
					return "."
				}
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(path)
}

func jsonTextResult(value interface{}) (*tools.Result, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return tools.NewToolResultText(string(raw)), nil
}

type findPattern struct {
	raw      string
	segments []string
}

func newFsFindTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Recursively locate files and directories by relative-path glob.

Current working directory: %s

Use this to find names or paths when their exact location is unknown. The pattern is matched against slash-separated paths relative to directory; ** as a complete segment matches zero or more directory levels. Ordinary segments use Go path.Match syntax. Hidden entries and vendor are included, while .git, symbolic links, and special nodes are skipped. Results are stably path-sorted and stop after 1000 matches or 512 KiB.`, workdir)
	return tools.NewTool(toolFsFind,
		tools.WithDescription(desc),
		tools.WithString("pattern", tools.Description("Glob matched against slash-separated paths relative to directory."), tools.MinLength(1), tools.Required()),
		tools.WithString("directory", tools.DefaultString("."), tools.Description("Existing directory to search recursively. Defaults to the current working directory.")),
		tools.WithExample(map[string]interface{}{"pattern": "**/*.py"}),
		tools.WithToolHandler(fsFindFileSystemHandler(fs)),
	)
}

func fsFindHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsFindFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func compileFindPattern(value string) (findPattern, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return findPattern{}, fmt.Errorf("pattern is required and must be a non-empty string")
	}
	if pathpkg.IsAbs(value) || filepath.IsAbs(value) {
		return findPattern{}, fmt.Errorf("pattern must be relative to directory")
	}
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	if value == "" {
		return findPattern{}, fmt.Errorf("pattern must identify a path below directory")
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == ".." {
			return findPattern{}, fmt.Errorf("pattern must not contain a parent-directory segment")
		}
		if segment == "**" {
			continue
		}
		if _, err := pathpkg.Match(segment, ""); err != nil {
			return findPattern{}, fmt.Errorf("invalid glob segment %q: %w", segment, err)
		}
	}
	return findPattern{raw: value, segments: segments}, nil
}

func findPatternMatches(pattern findPattern, relativePath string) bool {
	pathSegments := strings.Split(filepath.ToSlash(relativePath), "/")
	type state struct{ pattern, path int }
	memo := make(map[state]bool)
	seen := make(map[state]bool)
	var match func(int, int) bool
	match = func(patternIndex, pathIndex int) bool {
		key := state{patternIndex, pathIndex}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var ok bool
		switch {
		case patternIndex == len(pattern.segments):
			ok = pathIndex == len(pathSegments)
		case pattern.segments[patternIndex] == "**":
			ok = match(patternIndex+1, pathIndex) || pathIndex < len(pathSegments) && match(patternIndex, pathIndex+1)
		case pathIndex < len(pathSegments):
			segmentMatch, _ := pathpkg.Match(pattern.segments[patternIndex], pathSegments[pathIndex])
			ok = segmentMatch && match(patternIndex+1, pathIndex+1)
		}
		memo[key] = ok
		return ok
	}
	return match(0, 0)
}

type fsFindMatch struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type fsFindResult struct {
	Directory     string        `json:"directory"`
	Pattern       string        `json:"pattern"`
	Matches       []fsFindMatch `json:"matches"`
	ErrorsSkipped int           `json:"errors_skipped"`
	Truncated     bool          `json:"truncated"`
	StoppedReason string        `json:"stopped_reason,omitempty"`
}

type findDirJob struct {
	abs string
	rel string
}

type findDirOutput struct {
	children      []findDirJob
	matches       []fsFindMatch
	errorsSkipped int
	limitExceeded bool
	err           error
}

// findWorkerOverride pins the directory worker count in tests; zero uses the
// production default of runtime.NumCPU(), capped at 16.
var findWorkerOverride int

func fsFindFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		directory, _ := req.Arguments["directory"].(string)
		if strings.TrimSpace(directory) == "" {
			directory = "."
		}
		patternValue, ok := req.Arguments["pattern"].(string)
		if !ok {
			return tools.NewToolResultActionableError("pattern is required and must be a non-empty string", "provide a relative-path glob such as **/*.go"), nil
		}
		pattern, err := compileFindPattern(patternValue)
		if err != nil {
			return tools.NewToolResultActionableError(err.Error(), "provide a valid relative-path glob; use ** only as a complete segment for recursive matching"), nil
		}
		root, err := fs.Resolve(ctx, directory, FileAccessRead)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid directory: %s", err), "use fs_list to find a readable directory and retry"), nil
		}
		info, err := fs.Stat(ctx, root)
		if err != nil {
			if os.IsNotExist(err) {
				return tools.NewToolResultActionableError(missingPathCause(ctx, fs, directory, root), "use fs_list or fs_find from the nearest existing directory; do not repeat the same guessed path"), nil
			}
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to inspect directory %q: %s", directory, err), "use fs_list to verify the path and retry"), nil
		}
		if !info.IsDir() {
			return tools.NewToolResultActionableError(fmt.Sprintf("cannot find below %q because it is not a directory", directory), "provide a directory path"), nil
		}

		display := newRootedDisplayPathFn(fs, directory, root)
		matches, errorsSkipped, matchLimitExceeded, err := runParallelFind(ctx, fs, root, info, display, pattern)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("find failed: %s", err), "narrow the directory or correct unreadable paths and retry"), nil
		}
		result := fsFindResult{
			Directory: display(root), Pattern: pattern.raw, Matches: []fsFindMatch{}, ErrorsSkipped: errorsSkipped,
		}
		applyFindOutputLimit(matches, &result, req.MaxOutputChars, matchLimitExceeded)
		return jsonTextResult(result)
	}
}

func findWorkerCount() int {
	workers := findWorkerOverride
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

func runParallelFind(ctx context.Context, fs FileSystem, root string, rootInfo os.FileInfo, display func(string) string, pattern findPattern) ([]fsFindMatch, int, bool, error) {
	readDir := fs.ReadDir
	if opener, ok := fs.(findDirectoryRootOpener); ok {
		rootReader, err := opener.OpenFindRoot(ctx, root, rootInfo)
		if err != nil {
			return nil, 0, false, err
		}
		defer rootReader.Close()
		readDir = rootReader.ReadDir
	}
	rootEntries, err := readDir(ctx, root)
	if err != nil {
		return nil, 0, false, err
	}
	initial := classifyFindEntries(root, "", rootEntries, display, pattern)
	queue := initial.children
	matches := &findMatchHeap{}
	heap.Init(matches)
	limitExceeded := initial.limitExceeded
	addMatches := func(items []fsFindMatch) {
		for _, item := range items {
			if matches.Len() < maxFindMatches {
				heap.Push(matches, item)
				continue
			}
			limitExceeded = true
			if item.Path < (*matches)[0].Path {
				heap.Pop(matches)
				heap.Push(matches, item)
			}
		}
	}
	addMatches(initial.matches)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan findDirJob)
	outputs := make(chan findDirOutput)
	var wg sync.WaitGroup
	for range findWorkerCount() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					resolvedDir, resolveErr := fs.Resolve(workerCtx, job.abs, FileAccessRead)
					if resolveErr == nil && !pathWithinRoot(resolvedDir, root) {
						resolveErr = fmt.Errorf("resolved directory escaped the search root")
					}
					output := findDirOutput{err: resolveErr}
					if resolveErr == nil {
						entries, readErr := readDir(workerCtx, resolvedDir)
						output.err = readErr
						if readErr == nil {
							output = classifyFindEntries(resolvedDir, job.rel, entries, display, pattern)
						}
					}
					select {
					case outputs <- output:
					case <-workerCtx.Done():
						return
					}
				}
			}
		}()
	}

	inFlight := 0
	errorsSkipped := initial.errorsSkipped
	for len(queue) > 0 || inFlight > 0 {
		var send chan findDirJob
		var next findDirJob
		if len(queue) > 0 {
			send, next = jobs, queue[0]
		}
		select {
		case <-ctx.Done():
			cancel()
			close(jobs)
			wg.Wait()
			return nil, errorsSkipped, limitExceeded, ctx.Err()
		case send <- next:
			queue = queue[1:]
			inFlight++
		case output := <-outputs:
			inFlight--
			errorsSkipped += output.errorsSkipped
			limitExceeded = limitExceeded || output.limitExceeded
			if output.err != nil {
				if ctx.Err() != nil || errors.Is(output.err, context.Canceled) || errors.Is(output.err, context.DeadlineExceeded) {
					cancel()
					close(jobs)
					wg.Wait()
					if ctx.Err() != nil {
						return nil, errorsSkipped, limitExceeded, ctx.Err()
					}
					return nil, errorsSkipped, limitExceeded, output.err
				}
				errorsSkipped++
				continue
			}
			queue = append(queue, output.children...)
			addMatches(output.matches)
		}
	}
	close(jobs)
	wg.Wait()

	kept := make([]fsFindMatch, matches.Len())
	copy(kept, *matches)
	sort.Slice(kept, func(i, j int) bool { return kept[i].Path < kept[j].Path })
	return kept, errorsSkipped, limitExceeded, nil
}

func classifyFindEntries(absDir, relDir string, entries []os.DirEntry, display func(string) string, pattern findPattern) findDirOutput {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	output := findDirOutput{}
	matches := &findMatchHeap{}
	heap.Init(matches)
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.Name() == ".git" && entry.IsDir() {
			continue
		}
		absPath := filepath.Join(absDir, entry.Name())
		relPath := entry.Name()
		if relDir != "" {
			relPath = pathpkg.Join(relDir, entry.Name())
		}
		entryType := ""
		switch {
		case entry.IsDir():
			entryType = "directory"
			output.children = append(output.children, findDirJob{abs: absPath, rel: relPath})
		case entry.Type()&os.ModeType == 0:
			info, err := entry.Info()
			if err != nil {
				output.errorsSkipped++
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			entryType = "file"
		default:
			continue
		}
		if findPatternMatches(pattern, relPath) {
			item := fsFindMatch{Path: display(absPath), Type: entryType}
			if matches.Len() < maxFindMatches {
				heap.Push(matches, item)
			} else {
				output.limitExceeded = true
				if item.Path < (*matches)[0].Path {
					heap.Pop(matches)
					heap.Push(matches, item)
				}
			}
		}
	}
	output.matches = append(output.matches, *matches...)
	return output
}

type findMatchHeap []fsFindMatch

func (h findMatchHeap) Len() int           { return len(h) }
func (h findMatchHeap) Less(i, j int) bool { return h[i].Path > h[j].Path }
func (h findMatchHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *findMatchHeap) Push(value interface{}) {
	*h = append(*h, value.(fsFindMatch))
}
func (h *findMatchHeap) Pop() interface{} {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func applyFindOutputLimit(matches []fsFindMatch, result *fsFindResult, maxOutputChars int64, matchLimitExceeded bool) {
	limit := maxFindOutputBytes
	if maxOutputChars > 0 && maxOutputChars < int64(limit) {
		limit = int(maxOutputChars)
	}
	const metadataReserve = 256
	base, _ := json.Marshal(result)
	used := len(base)
	outputTruncated := false
	for _, match := range matches {
		encoded, _ := json.Marshal(match)
		extra := len(encoded)
		if len(result.Matches) > 0 {
			extra++
		}
		if used+extra+metadataReserve > limit {
			outputTruncated = true
			break
		}
		result.Matches = append(result.Matches, match)
		used += extra
	}
	switch {
	case outputTruncated:
		result.Truncated = true
		result.StoppedReason = "output_size_limit"
	case matchLimitExceeded:
		result.Truncated = true
		result.StoppedReason = "match_limit"
	}
}

func newFsSearchTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Recursively search regular text-file contents with a Go RE2 regular expression.

Current working directory: %s

Use this for structured code or text search across a directory tree. It searches file contents, not file names; use fs_find to locate names recursively or fs_list to inspect one directory. Hidden files and vendor are included, while .git, binary files, and symbolic links are skipped. The result is a JSON string and stops after 1000 matching lines or 512 KiB with explicit truncation metadata.`, workdir)
	return tools.NewTool(toolFsSearch,
		tools.WithDescription(desc),
		tools.WithString("directory", tools.Description("Existing directory whose readable text files will be searched recursively."), tools.MinLength(1), tools.Required()),
		tools.WithString("regex", tools.Description("Go RE2 regular expression matched independently against each text line."), tools.MinLength(1), tools.Required()),
		tools.WithExample(map[string]interface{}{"directory": "core/actor", "regex": `func\s+New[A-Za-z]+`}),
		tools.WithToolHandler(fsSearchFileSystemHandler(fs)),
	)
}

type fsSearchMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

type fsSearchResult struct {
	Directory          string          `json:"directory"`
	Regex              string          `json:"regex"`
	Matches            []fsSearchMatch `json:"matches"`
	FilesScanned       int             `json:"files_scanned"`
	BinaryFilesSkipped int             `json:"binary_files_skipped"`
	ErrorsSkipped      int             `json:"errors_skipped"`
	Truncated          bool            `json:"truncated"`
	StoppedReason      string          `json:"stopped_reason,omitempty"`
	encodedSize        int             `json:"-"`
}

func fsSearchFileSystemHandler(fs FileSystem) tools.ToolHandlerFunc {
	log := logger.New("fs.search")
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		directory, ok := req.Arguments["directory"].(string)
		if !ok || strings.TrimSpace(directory) == "" {
			return tools.NewToolResultActionableError("directory is required and must be a non-empty string", "provide an existing readable directory"), nil
		}
		pattern, ok := req.Arguments["regex"].(string)
		if !ok || strings.TrimSpace(pattern) == "" {
			return tools.NewToolResultActionableError("regex is required and must be a non-empty string", "provide a valid Go RE2 regular expression"), nil
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid regex: %s", err), "correct the Go RE2 expression and retry"), nil
		}
		root, err := fs.Resolve(ctx, directory, FileAccessRead)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid directory: %s", err), "use fs_list to find a readable directory and retry"), nil
		}
		info, err := fs.Stat(ctx, root)
		if err != nil {
			if os.IsNotExist(err) {
				return tools.NewToolResultActionableError(missingPathCause(ctx, fs, directory, root), "use fs_list from the nearest existing directory; do not repeat the same guessed path"), nil
			}
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to inspect directory %q: %s", directory, err), "use fs_list to verify the path and retry"), nil
		}
		if !info.IsDir() {
			return tools.NewToolResultActionableError(fmt.Sprintf("cannot search %q because it is not a directory", directory), "provide a directory path"), nil
		}

		display := newRootedDisplayPathFn(fs, directory, root)
		result := fsSearchResult{Directory: display(root), Regex: pattern, Matches: []fsSearchMatch{}}
		result.encodedSize = searchResultSize(&result)
		outputLimit := maxSearchOutputBytes
		if req.MaxOutputChars > 0 && int64(outputLimit) > req.MaxOutputChars-512 {
			outputLimit = int(req.MaxOutputChars - 512)
			if outputLimit < 1024 {
				outputLimit = 1024
			}
		}
		start := time.Now()
		workers, jobsScanned, err := runParallelSearch(ctx, fs, root, display, re, &result, outputLimit)
		log.Infow("search complete",
			"workers", workers,
			"jobs", jobsScanned,
			"scanned", result.FilesScanned,
			"matches", len(result.Matches),
			"truncated", result.Truncated,
			"reason", result.StoppedReason,
			"duration_ms", time.Since(start).Milliseconds())
		if err != nil && err != errSearchLimit {
			return tools.NewToolResultActionableError(fmt.Sprintf("search failed: %s", err), "narrow the directory or correct unreadable paths and retry"), nil
		}
		return jsonTextResult(result)
	}
}

var errSearchLimit = fmt.Errorf("search result limit reached")

// fileJob is one regular file discovered by the walker and queued for a
// scanning worker.
type fileJob struct {
	absPath string
}

// workerOutput is a worker's complete local accounting. Workers never touch
// shared state; everything is merged by the collector after they finish.
type workerOutput struct {
	matches       []fsSearchMatch
	filesScanned  int
	binarySkipped int
	errorsSkipped int
	localLimitHit bool
}

// searchWorkerOverride pins the worker-pool size for benchmarks; 0 means the
// production default of runtime.NumCPU() (capped at 16).
var searchWorkerOverride int

// runParallelSearch drives one walker goroutine plus a worker pool. The
// walker keeps the original depth-first, name-sorted discovery order for the
// .git / symlink / non-regular skip rules; workers scan file contents in
// parallel. Matches are merged and totally ordered by (path, line, column)
// before limits are applied, so the emitted JSON stays deterministic no
// matter how the scan interleaved.
func runParallelSearch(ctx context.Context, fs FileSystem, root string, display func(string) string, re *regexp.Regexp, result *fsSearchResult, outputLimit int) (workers, jobsEmitted int, err error) {
	workerCount := searchWorkerOverride
	if workerCount <= 0 {
		workerCount = runtime.NumCPU()
		if workerCount > 16 {
			workerCount = 16
		}
		if workerCount < 1 {
			workerCount = 1
		}
	}

	// The root ReadDir failure keeps the historical contract: an unreadable
	// search root is an actionable error, not a JSON result.
	entries, err := fs.ReadDir(ctx, root)
	if err != nil {
		return workerCount, 0, err
	}

	jobs := make(chan fileJob, 256)
	var stopped atomic.Bool
	var walkerErrors atomic.Int64
	outputs := make([]workerOutput, workerCount)

	var wg sync.WaitGroup
	for i := range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outputs[i] = scanFiles(ctx, fs, display, jobs, re, &stopped)
		}()
	}
	emitted := walkEntries(ctx, fs, root, entries, jobs, &stopped, &walkerErrors)
	close(jobs)
	wg.Wait()

	for i := range outputs {
		out := &outputs[i]
		result.FilesScanned += out.filesScanned
		result.BinaryFilesSkipped += out.binarySkipped
		result.ErrorsSkipped += out.errorsSkipped
	}
	result.ErrorsSkipped += int(walkerErrors.Load())

	matches := make([]fsSearchMatch, 0)
	workerLimitHit := false
	for i := range outputs {
		matches = append(matches, outputs[i].matches...)
		workerLimitHit = workerLimitHit || outputs[i].localLimitHit
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Column < matches[j].Column
	})
	applyGlobalLimits(matches, result, outputLimit, workerLimitHit)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return workerCount, emitted, ctxErr
	}
	return workerCount, emitted, nil
}

// walkEntries is the discovery half of the search. It mirrors the previous
// serial DFS exactly — entries sorted by name, .git directories and symlinks
// skipped, only regular files queued — but it never blocks: workers keep
// draining the jobs channel even after an early stop, so sends always
// complete. Unreadable subdirectories only increment the skip counter.
func walkEntries(ctx context.Context, fs FileSystem, dir string, entries []os.DirEntry, jobs chan<- fileJob, stopped *atomic.Bool, errCount *atomic.Int64) int {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	emitted := 0
	for _, entry := range entries {
		if ctx.Err() != nil || stopped.Load() {
			return emitted
		}
		if entry.Name() == ".git" && entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		abs := filepath.Join(dir, entry.Name())
		switch entryType := entry.Type(); {
		case entryType&os.ModeDir != 0:
			childEntries, err := fs.ReadDir(ctx, abs)
			if err != nil {
				errCount.Add(1)
				continue
			}
			emitted += walkEntries(ctx, fs, abs, childEntries, jobs, stopped, errCount)
		case entryType&os.ModeType == 0:
			// Regular file: type bits are all clear.
			jobs <- fileJob{absPath: abs}
			emitted++
		default:
			// Device, socket, named pipe, or other non-regular entry.
		}
	}
	return emitted
}

// scanFiles is the worker loop. It drains the jobs channel for its whole
// life — even once stopped is set — so the walker can never block on a full
// channel; skipping the scan is what makes an early stop cheap.
func scanFiles(ctx context.Context, fs FileSystem, display func(string) string, jobs <-chan fileJob, re *regexp.Regexp, stopped *atomic.Bool) workerOutput {
	var out workerOutput
	for job := range jobs {
		if stopped.Load() || ctx.Err() != nil {
			continue
		}
		matches, binary, scanErr := scanSingleFile(ctx, fs, job.absPath, display, re, &out)
		if scanErr != nil {
			if scanErr == errSearchLimit {
				// Keep the matches collected before the cap; then tell the
				// other workers and the walker to wind down.
				out.matches = append(out.matches, matches...)
				stopped.Store(true)
				continue
			}
			if errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
				continue
			}
			out.errorsSkipped++
			continue
		}
		if binary {
			out.binarySkipped++
			continue
		}
		out.filesScanned++
		if len(matches) > 0 {
			out.matches = append(out.matches, matches...)
		}
	}
	return out
}

// scanSingleFile opens one file, runs the binary probe, and collects
// per-line matches. A worker stops itself after maxSearchMatches local
// matches so no single worker can dwarf the global limit; the output-byte
// budget is enforced once, globally, after the merge.
func scanSingleFile(ctx context.Context, fs FileSystem, absPath string, display func(string) string, re *regexp.Regexp, out *workerOutput) ([]fsSearchMatch, bool, error) {
	reader, err := openSearchFile(ctx, fs, absPath)
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()

	buffered := bufio.NewReader(reader)
	probe, err := buffered.Peek(8192)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, false, err
	}
	if bytes.IndexByte(probe, 0) >= 0 || (!utf8.Valid(probe) && len(probe) > 0) {
		return nil, true, nil
	}

	displayPath := display(absPath)
	var matches []fsSearchMatch
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line, readErr := buffered.ReadString('\n')
		if len(line) > 0 {
			lineNumber++
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if location := re.FindStringIndex(line); location != nil {
				if len(out.matches)+len(matches) >= maxSearchMatches {
					out.localLimitHit = true
					out.filesScanned++
					return matches, false, errSearchLimit
				}
				matches = append(matches, fsSearchMatch{
					Path: displayPath, Line: lineNumber,
					Column: utf8.RuneCountInString(line[:location[0]]) + 1, Text: line,
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, false, readErr
		}
	}
	return matches, false, nil
}

// applyGlobalLimits applies the count and byte budgets to the fully sorted
// match list. Truncation keeps the smallest entries under the total order,
// which is deterministic across runs regardless of worker scheduling.
func applyGlobalLimits(matches []fsSearchMatch, result *fsSearchResult, outputLimit int, workerLimitHit bool) {
	const finalMetadataReserve = 256
	kept := 0
	encodedSize := 0
	truncated := false
	reason := ""
	for _, match := range matches {
		encoded, _ := json.Marshal(match)
		separatorBytes := 1
		if kept >= maxSearchMatches || encodedSize+separatorBytes+len(encoded)+finalMetadataReserve > outputLimit {
			truncated = true
			if kept >= maxSearchMatches {
				reason = "match_limit"
			} else {
				reason = "output_size_limit"
			}
			break
		}
		matches[kept] = match
		kept++
		encodedSize += separatorBytes + len(encoded)
	}
	if !truncated && workerLimitHit {
		// A worker filled its local share, so some files were never scanned;
		// report the result as truncated even if the kept slice is in budget.
		truncated = true
		reason = "match_limit"
	}
	result.Truncated = truncated
	result.StoppedReason = reason
	result.Matches = matches[:kept]
	result.encodedSize = searchResultSize(result)
}

func openSearchFile(ctx context.Context, fs FileSystem, path string) (io.ReadCloser, error) {
	if opener, ok := fs.(fileOpener); ok {
		return opener.Open(ctx, path)
	}
	content, err := fs.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func searchResultSize(result *fsSearchResult) int {
	raw, _ := json.Marshal(result)
	return len(raw)
}

func missingPathCause(ctx context.Context, fs FileSystem, requested, resolved string) string {
	dir := filepath.Dir(resolved)
	displayDir := filepath.Dir(requestedDisplayPath(fs, requested))
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Sprintf("path %q does not exist", requested)
		}
		info, err := fs.Stat(ctx, dir)
		if err == nil && info.IsDir() {
			cause := fmt.Sprintf("path %q does not exist; nearest existing directory is %q", requested, filepath.ToSlash(displayDir))
			entries, readErr := fs.ReadDir(ctx, dir)
			if readErr == nil {
				stem := strings.TrimSuffix(filepath.Base(resolved), filepath.Ext(resolved))
				stemKey := candidateStem(stem)
				var candidates []string
				for _, entry := range entries {
					if stem == "" {
						break
					}
					entryStem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
					if candidateStem(entryStem) == stemKey || strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(stem)) {
						candidates = append(candidates, filepath.ToSlash(filepath.Join(displayDir, entry.Name())))
						if len(candidates) == 10 {
							break
						}
					}
				}
				if len(candidates) > 0 {
					cause += "; possible existing paths: " + strings.Join(candidates, ", ")
				}
			}
			return cause
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		displayDir = filepath.Dir(displayDir)
	}
	return fmt.Sprintf("path %q does not exist", requested)
}

func candidateStem(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "test_")
	value = strings.TrimSuffix(value, "_test")
	value = strings.TrimSuffix(value, "_spec")
	return value
}

func newFsDeleteTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Permanently delete a file, symbolic link, or directory. This cannot be undone.

Current working directory: %s

Files, symbolic links, and empty directories do not require recursive mode. Set recursive=true explicitly for a non-empty directory. Filesystem roots, the current working directory, and configured authorization roots can never be deleted. A symbolic link is unlinked without deleting its target.`, workdir)

	return tools.NewTool(toolFsDelete,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("Existing file, symbolic link, or directory to delete."), tools.MinLength(1), tools.Required()),
		tools.WithBoolean("recursive", tools.DefaultBool(false), tools.Description("Set true only to delete a non-empty directory tree.")),
		tools.WithExample(map[string]interface{}{"path": "build/cache", "recursive": true}),
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

		var absPath string
		var err error
		if resolver, ok := fs.(deletePathResolver); ok {
			absPath, err = resolver.ResolveDelete(ctx, path)
		} else {
			absPath, err = fs.Resolve(ctx, path, FileAccessWrite)
		}
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("invalid path: %s", err), "use a writable path inside the allowed workdir"), nil
		}
		if resolver, ok := fs.(deletePathResolver); ok {
			if err := resolver.ValidateDeleteTarget(absPath); err != nil {
				return tools.NewToolResultActionableError(err.Error(), "delete a child path instead; authorization roots cannot be deleted"), nil
			}
		}
		var statErr error
		if statter, ok := fs.(linkStatter); ok {
			_, statErr = statter.Lstat(ctx, absPath)
		} else {
			_, statErr = fs.Stat(ctx, absPath)
		}
		if statErr != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to inspect delete target %q: %s", path, statErr), "use fs_list to verify the exact existing path before retrying"), nil
		}

		recursive, _ := req.Arguments["recursive"].(bool)
		if recursive {
			remover, ok := fs.(recursiveFileRemover)
			if !ok {
				return tools.NewToolResultActionableError("recursive deletion is not supported by this filesystem backend", "delete children separately or use a backend that supports recursive deletion"), nil
			}
			err = remover.RemoveAll(ctx, absPath)
		} else {
			err = fs.Remove(ctx, absPath)
		}
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to delete %q: %s", path, err), "verify the path with fs_list and ensure deletion is permitted"), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Successfully deleted %s", path)), nil
	}
}

func newFsEditTool(fs FileSystem, workdir string) *tools.Tool {
	desc := fmt.Sprintf(`Edit one existing text file using an exact, non-regex text replacement.

Current working directory: %s

Read the file first and copy old_text exactly, including whitespace and line breaks. By default old_text must occur exactly once; set replace_all=true only when every occurrence should change. Use new_text="" to delete the matched text, and use fs_write for a complete file replacement.`, workdir)

	return tools.NewTool(toolFsEdit,
		tools.WithDescription(desc),
		tools.WithString("path", tools.Description("Existing text file to edit."), tools.MinLength(1), tools.Required()),
		tools.WithString("old_text", tools.Description("Exact current text to replace. It must be unique unless replace_all is true."), tools.MinLength(1), tools.Required()),
		tools.WithString("new_text", tools.Description("Replacement text. Use an empty string to delete old_text."), tools.Required()),
		tools.WithBoolean("replace_all", tools.DefaultBool(false), tools.Description("Replace every occurrence instead of requiring one unique occurrence.")),
		tools.WithExample(map[string]interface{}{"path": "main.go", "old_text": "oldValue", "new_text": "newValue", "replace_all": false}),
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

		searchString, ok := req.Arguments["old_text"].(string)
		if !ok || searchString == "" {
			return tools.NewToolResultActionableError("old_text is required and must be a non-empty string", "provide text that exactly matches the current file contents"), nil
		}

		replaceString, ok := req.Arguments["new_text"].(string)
		if !ok {
			return tools.NewToolResultActionableError("new_text is required and must be a string", "provide replacement text; use an empty string to delete the match"), nil
		}

		replaceAll, _ := req.Arguments["replace_all"].(bool)

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
		if editor, ok := fs.(exactFileEditor); ok {
			count, err := editor.EditExact(ctx, absPath, searchString, replaceString, replaceAll)
			if err != nil {
				return tools.NewToolResultActionableError(fmt.Sprintf("failed to edit file %q: %s", path, err), "verify write permission and retry after confirming the file is unchanged"), nil
			}
			if count == 0 {
				return tools.NewToolResultActionableError(fmt.Sprintf("old_text was not found in %q: %q", path, truncateForError(searchString)), "call fs_read, copy the exact current text including whitespace, and retry"), nil
			}
			if count > 1 && !replaceAll {
				return tools.NewToolResultActionableError(fmt.Sprintf("old_text matches %d locations in %q", count, path), "provide a larger unique old_text or set replace_all=true only when every match should change"), nil
			}
			replacedCount := 1
			if replaceAll {
				replacedCount = count
			}
			return tools.NewToolResultText(fmt.Sprintf("Successfully replaced %d occurrence(s) in %s", replacedCount, path)), nil
		}

		content, err := fs.ReadFile(ctx, absPath)
		if err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to read file %q: %s", path, err), "verify that the file is readable and then retry"), nil
		}

		contentStr := string(content)
		count := strings.Count(contentStr, searchString)
		if count == 0 {
			return tools.NewToolResultActionableError(fmt.Sprintf("old_text was not found in %q: %q", path, truncateForError(searchString)), "call fs_read, copy the exact current text including whitespace, and retry"), nil
		}
		if count > 1 && !replaceAll {
			return tools.NewToolResultActionableError(fmt.Sprintf("old_text matches %d locations in %q", count, path), "provide a larger unique old_text or set replace_all=true only when every match should change"), nil
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

		if err := fs.WriteFile(ctx, absPath, []byte(newContent)); err != nil {
			return tools.NewToolResultActionableError(fmt.Sprintf("failed to write edited file %q: %s", path, err), "verify write permission and retry after confirming the file is unchanged"), nil
		}

		msg := fmt.Sprintf("Successfully replaced %d occurrence(s) in %s", replacedCount, path)
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
	if err := validateResolvedToolPath(cfg, workdir, absPath, mode); err != nil {
		return "", err
	}
	return absPath, nil
}

func validateResolvedToolPath(cfg *Config, workdir, absPath string, mode fsAccessMode) error {
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
		return fmt.Errorf("path is denied by sandbox rules")
	}

	if mode == fsAccessWrite {
		if inProtectedRoots || inReadOnlyRoots {
			return fmt.Errorf("path is read-only")
		}
		if !inWorkdir && !inWriteRoots {
			return fmt.Errorf("path is outside writable roots")
		}
		return nil
	}

	if !inWorkdir && !inWriteRoots && !inReadOnlyRoots && !inProtectedRoots {
		return fmt.Errorf("path is outside readable roots")
	}
	return nil
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
		if gitPath, ok, err := resolveLinkedWorktreeGitPath(strings.TrimSpace(pattern), workdir); err != nil {
			// A malformed linked-checkout control file must fail closed for
			// native writes just as command policy compilation does.
			return true
		} else if ok {
			pattern = gitPath
		}
		expanded := expandPath(pattern, workdir, "")
		if !strings.ContainsAny(expanded, "*?[]") && !filepath.IsAbs(expanded) {
			absPattern, err := filepath.Abs(expanded)
			if err == nil {
				expanded = absPattern
			}
		}
		expanded = canonicalizePolicyPattern(expanded)
		if matchesDeniedPath(expanded, absPath) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func canonicalizePolicyPattern(pattern string) string {
	if pattern == "" {
		return ""
	}
	if !strings.ContainsAny(pattern, "*?[]") {
		if resolved, err := resolveSymlinkedPath(pattern); err == nil {
			return filepath.Clean(resolved)
		}
		return filepath.Clean(pattern)
	}
	volume := filepath.VolumeName(pattern)
	rest := strings.TrimPrefix(pattern, volume)
	parts := strings.Split(rest, string(os.PathSeparator))
	prefixParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.ContainsAny(part, "*?[]") {
			break
		}
		prefixParts = append(prefixParts, part)
	}
	prefix := volume + strings.Join(prefixParts, string(os.PathSeparator))
	if prefix == "" || prefix == volume {
		return filepath.Clean(pattern)
	}
	resolved, err := resolveSymlinkedPath(prefix)
	if err != nil {
		return filepath.Clean(pattern)
	}
	suffix := strings.TrimPrefix(pattern, prefix)
	return filepath.Clean(resolved + suffix)
}

func sameCanonicalPath(left, right string) bool {
	leftResolved, leftErr := resolveSymlinkedPath(left)
	rightResolved, rightErr := resolveSymlinkedPath(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return filepath.Clean(leftResolved) == filepath.Clean(rightResolved)
}

func isFilesystemPolicyRoot(cfg *Config, workdir, target string) bool {
	if cfg == nil || cfg.IsolationDisabled() {
		return false
	}
	all := [][]string{
		cfg.Sandbox.Filesystem.Write,
		cfg.Sandbox.Filesystem.ReadOnly,
		cfg.Sandbox.Filesystem.Protected,
		cfg.Sandbox.Filesystem.Deny,
	}
	for _, patterns := range all {
		for _, pattern := range patterns {
			expanded := expandPath(pattern, workdir, "")
			if strings.ContainsAny(expanded, "*?[]") {
				continue
			}
			if sameCanonicalPath(target, expanded) {
				return true
			}
		}
	}
	return false
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

func writeFileAtomicRoot(root *os.Root, path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmpFile, tmpPath, err := createRootTemp(root, dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(perm.Perm()); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := root.Rename(tmpPath, path); err != nil {
		return err
	}

	cleanup = false
	return nil
}

func createRootTemp(root *os.Root, dir, prefix string) (*os.File, string, error) {
	const attempts = 100
	for range attempts {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := filepath.Join(dir, prefix+hex.EncodeToString(random[:]))
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("failed to create a unique temporary file")
}

func editFileAtomicExact(ctx context.Context, path string, oldText, newText []byte, replaceAll bool, perm os.FileMode) (int, error) {
	input, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer input.Close()

	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, "."+filepath.Base(path)+".edit-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmpFile.Name()
	committed := false
	defer func() {
		_ = tmpFile.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	count, err := replaceExactStream(ctx, input, tmpFile, oldText, newText, replaceAll)
	if err != nil {
		return 0, err
	}
	if count == 0 || (!replaceAll && count > 1) {
		return count, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := tmpFile.Chmod(perm); err != nil {
		return 0, err
	}
	if err := tmpFile.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return 0, err
	}
	committed = true
	return count, nil
}

func editFileAtomicExactRoot(ctx context.Context, root *os.Root, path string, oldText, newText []byte, replaceAll bool, perm os.FileMode) (int, error) {
	input, err := root.Open(path)
	if err != nil {
		return 0, err
	}
	defer input.Close()

	dir := filepath.Dir(path)
	tmpFile, tmpPath, err := createRootTemp(root, dir, "."+filepath.Base(path)+".edit-")
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		_ = tmpFile.Close()
		if !committed {
			_ = root.Remove(tmpPath)
		}
	}()

	count, err := replaceExactStream(ctx, input, tmpFile, oldText, newText, replaceAll)
	if err != nil {
		return 0, err
	}
	if count == 0 || (!replaceAll && count > 1) {
		return count, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := tmpFile.Chmod(perm.Perm()); err != nil {
		return 0, err
	}
	if err := tmpFile.Close(); err != nil {
		return 0, err
	}
	if err := root.Rename(tmpPath, path); err != nil {
		return 0, err
	}
	committed = true
	return count, nil
}

func replaceExactStream(ctx context.Context, input io.Reader, output io.Writer, oldText, newText []byte, replaceAll bool) (int, error) {
	const chunkSize = 64 * 1024
	chunk := make([]byte, chunkSize)
	pending := make([]byte, 0, chunkSize+len(oldText))
	count := 0
	replaced := 0
	writePending := func(eof bool) error {
		for {
			index := bytes.Index(pending, oldText)
			if index < 0 {
				keep := len(oldText) - 1
				if eof {
					keep = 0
				}
				flush := len(pending) - keep
				if flush > 0 {
					if _, err := output.Write(pending[:flush]); err != nil {
						return err
					}
					pending = append(pending[:0], pending[flush:]...)
				}
				return nil
			}
			count++
			if _, err := output.Write(pending[:index]); err != nil {
				return err
			}
			if replaceAll || replaced == 0 {
				if _, err := output.Write(newText); err != nil {
					return err
				}
				replaced++
			} else if _, err := output.Write(oldText); err != nil {
				return err
			}
			pending = append(pending[:0], pending[index+len(oldText):]...)
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, readErr := input.Read(chunk)
		if n > 0 {
			pending = append(pending, chunk[:n]...)
			if err := writePending(false); err != nil {
				return 0, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if err := writePending(true); err != nil {
		return 0, err
	}
	return count, nil
}

// truncateForError truncates a long string for error messages (by runes to avoid breaking UTF-8)
func truncateForError(s string) string {
	runes := []rune(s)
	if len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	return s
}
