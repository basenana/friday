package filetools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/sandbox"
)

const (
	maxInstructionLines = 100
	maxInstructionRunes = 60_000  // approximately 30K tokens at the project estimator's 0.5 tokens/rune
	maxFYIRunes         = 100_000 // approximately 50K tokens
)

var instructionNames = []string{"AGENTS.md", "CLAUDE.md"}

type Option func(*Hook)

// WithFileSystem replaces the local sandbox-backed filesystem implementation.
func WithFileSystem(fs sandbox.FileSystem) Option {
	return func(h *Hook) { h.fs = fs }
}

// Hook owns the native filesystem tools and augments read-only results with
// lazily discovered project instructions.
type Hook struct {
	root      string
	fs        sandbox.FileSystem
	namespace string
	logger    logger.Logger

	mu     sync.Mutex
	loaded map[string]map[string]struct{}
}

var _ session.BeforeAgentHook = (*Hook)(nil)

// New constructs a filesystem tool hook. When WithFileSystem is omitted, the
// current sandbox-backed local implementation is used.
func New(exec *sandbox.Executor, root string, opts ...Option) (*Hook, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root symlinks: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat project root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project root is not a directory: %s", absRoot)
	}

	sum := sha256.Sum256([]byte(filepath.Clean(absRoot)))
	h := &Hook{
		root:      filepath.Clean(absRoot),
		namespace: fmt.Sprintf("file_instructions.%x", sum[:8]),
		logger:    logger.New("filetools"),
		loaded:    make(map[string]map[string]struct{}),
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.fs == nil {
		if exec == nil {
			return nil, fmt.Errorf("filesystem executor is required without a custom filesystem")
		}
		h.fs = sandbox.NewLocalFileSystem(exec, h.root)
	}
	return h, nil
}

// Tools returns fresh tool definitions backed by this hook.
func (h *Hook) Tools() []*tools.Tool {
	base := sandbox.NewFsToolsWithFileSystem(h.fs, h.root)
	wrapped := make([]*tools.Tool, 0, len(base))
	for _, tool := range base {
		clone := *tool
		switch clone.Name {
		case sandbox.FsReadToolName, sandbox.FsListToolName:
			clone.Handler = h.withFYI(clone.Name, tool.Handler)
		case sandbox.FsWriteToolName, sandbox.FsEditToolName, sandbox.FsDeleteToolName:
			clone.Handler = h.withInstructionInvalidation(tool.Handler)
		}
		wrapped = append(wrapped, &clone)
	}
	return wrapped
}

// BeforeAgent hydrates the persisted record before a root can be forked. Tool
// definitions still come from Tools so agent-level ToolPolicy remains in force.
func (h *Hook) BeforeAgent(ctx context.Context, sess *session.Session, _ session.AgentRequest) error {
	_, err := sess.ReadRecord(ctx, h.namespace)
	if err != nil && !errors.Is(err, session.ErrRecordNotFound) {
		h.logger.Warnw("failed to hydrate project instruction record", "session", sess.ID, "error", err)
	}
	return nil
}

func (h *Hook) withFYI(toolName string, next tools.ToolHandlerFunc) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		result, err := next(ctx, req)
		if err != nil || result == nil {
			return result, err
		}
		path, _ := req.Arguments["path"].(string)
		if toolName == sandbox.FsListToolName && strings.TrimSpace(path) == "" {
			path = "."
		}
		fyi, discoverErr := h.discover(ctx, req, toolName, path)
		if discoverErr != nil {
			h.logger.Warnw("failed to discover project instructions", "session", req.SessionID, "path", path, "error", discoverErr)
		} else {
			result.FYI = fyi
		}
		return result, nil
	}
}

func (h *Hook) withInstructionInvalidation(next tools.ToolHandlerFunc) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path, _ := req.Arguments["path"].(string)
		resolved, resolveErr := h.fs.Resolve(ctx, path, sandbox.FileAccessWrite)
		result, err := next(ctx, req)
		if err == nil && result != nil && !result.IsError && resolveErr == nil && isInstructionFile(resolved) {
			if invalidateErr := h.invalidate(ctx, req, filepath.Dir(resolved)); invalidateErr != nil {
				h.logger.Warnw("failed to invalidate project instructions", "session", req.SessionID, "path", resolved, "error", invalidateErr)
			}
		}
		return result, err
	}
}

func (h *Hook) discover(ctx context.Context, req *tools.Request, toolName, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	resolved, err := h.fs.Resolve(ctx, path, sandbox.FileAccessRead)
	if err != nil {
		return "", nil
	}
	dir := resolved
	if toolName == sandbox.FsReadToolName {
		dir = filepath.Dir(resolved)
	}
	dir = h.nearestExistingDirectory(ctx, dir)
	if dir == "" || !withinRoot(h.root, dir) {
		return "", nil
	}

	dirs := directoryChain(dir, h.root)
	unseen, err := h.claim(ctx, req, dirs)
	if err != nil {
		h.logger.Warnw("failed to update project instruction record; using memory fallback", "session", req.SessionID, "error", err)
		unseen = h.claimInMemory(req.SessionID, dirs)
		err = nil
	} else {
		_ = h.claimInMemory(req.SessionID, dirs)
	}

	var sections []string
	for _, candidateDir := range unseen {
		section := h.readInstructionSection(ctx, candidateDir)
		if section != "" {
			sections = append(sections, section)
		}
	}
	fyi := strings.Join(sections, "\n\n")
	return truncateRunes(fyi, maxFYIRunes), err
}

func (h *Hook) nearestExistingDirectory(ctx context.Context, dir string) string {
	for withinRoot(h.root, dir) {
		info, err := h.fs.Stat(ctx, dir)
		if err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func (h *Hook) readInstructionSection(ctx context.Context, dir string) string {
	for _, name := range instructionNames {
		path := filepath.Join(dir, name)
		resolved, err := h.fs.Resolve(ctx, path, sandbox.FileAccessRead)
		if err != nil {
			continue
		}
		if !withinRoot(h.root, resolved) {
			continue
		}
		info, err := h.fs.Stat(ctx, resolved)
		if err != nil || info.IsDir() {
			continue
		}
		content, err := h.fs.ReadFile(ctx, resolved)
		if err != nil {
			return ""
		}
		rel, err := filepath.Rel(h.root, resolved)
		if err != nil {
			return ""
		}
		body := truncateInstruction(string(content))
		return "## " + filepath.ToSlash(rel) + "\n\n" + body
	}
	return ""
}

type loadedRecord struct {
	Version     int      `json:"version"`
	Directories []string `json:"directories"`
}

func (h *Hook) claim(ctx context.Context, req *tools.Request, dirs []string) ([]string, error) {
	if req.SessionRecords == nil {
		return h.claimInMemory(req.SessionID, dirs), nil
	}
	var unseen []string
	err := req.SessionRecords.UpdateRecord(ctx, h.namespace, func(current []byte) ([]byte, error) {
		record := loadedRecord{Version: 1}
		if len(current) > 0 {
			if err := json.Unmarshal(current, &record); err != nil {
				return nil, err
			}
		}
		known := make(map[string]struct{}, len(record.Directories)+len(dirs))
		for _, dir := range record.Directories {
			known[dir] = struct{}{}
		}
		for _, dir := range dirs {
			rel := h.relativeDirectory(dir)
			if _, ok := known[rel]; ok {
				continue
			}
			known[rel] = struct{}{}
			record.Directories = append(record.Directories, rel)
			unseen = append(unseen, dir)
		}
		record.Version = 1
		return json.Marshal(record)
	})
	return unseen, err
}

func (h *Hook) invalidate(ctx context.Context, req *tools.Request, dir string) error {
	if !withinRoot(h.root, dir) {
		return nil
	}
	rel := h.relativeDirectory(dir)
	h.invalidateInMemory(req.SessionID, rel)
	if req.SessionRecords == nil {
		return nil
	}
	return req.SessionRecords.UpdateRecord(ctx, h.namespace, func(current []byte) ([]byte, error) {
		record := loadedRecord{Version: 1}
		if len(current) > 0 {
			if err := json.Unmarshal(current, &record); err != nil {
				return nil, err
			}
		}
		kept := record.Directories[:0]
		for _, existing := range record.Directories {
			if existing != rel {
				kept = append(kept, existing)
			}
		}
		record.Directories = kept
		return json.Marshal(record)
	})
}

func (h *Hook) claimInMemory(sessionID string, dirs []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	known := h.loaded[sessionID]
	if known == nil {
		known = make(map[string]struct{})
		h.loaded[sessionID] = known
	}
	var unseen []string
	for _, dir := range dirs {
		rel := h.relativeDirectory(dir)
		if _, ok := known[rel]; ok {
			continue
		}
		known[rel] = struct{}{}
		unseen = append(unseen, dir)
	}
	return unseen
}

func (h *Hook) invalidateInMemory(sessionID, rel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.loaded[sessionID], rel)
}

func (h *Hook) relativeDirectory(dir string) string {
	rel, err := filepath.Rel(h.root, dir)
	if err != nil || rel == "" {
		return "."
	}
	return filepath.ToSlash(rel)
}

func directoryChain(start, root string) []string {
	var dirs []string
	for dir := filepath.Clean(start); withinRoot(root, dir); dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == root {
			break
		}
	}
	return dirs
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isInstructionFile(path string) bool {
	base := filepath.Base(path)
	return base == instructionNames[0] || base == instructionNames[1]
}

func truncateInstruction(content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > maxInstructionLines {
		content = strings.Join(lines[:maxInstructionLines], "")
	}
	return truncateRunes(content, maxInstructionRunes)
}

func truncateRunes(content string, limit int) string {
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return string(runes[:limit])
}
