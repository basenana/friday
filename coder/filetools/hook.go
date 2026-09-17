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
	"github.com/basenana/friday/core/promptcontext"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
)

const (
	maxInstructionLines = 100
	maxInstructionRunes = 60_000  // approximately 30K tokens at the project estimator's 0.5 tokens/rune
	maxFYIRunes         = 100_000 // approximately 50K tokens
)

const projectInstructionsIntro = `As you answer the user's questions, you can use the following context:

# projectInstructions
Codebase and user instructions are shown below.`

const projectInstructionsOutro = `IMPORTANT: this context may or may not be relevant to the current request. Follow these instructions when they apply.`

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
	loaded map[string]instructionRecord
}

var _ session.BeforeAgentHook = (*Hook)(nil)
var _ session.BeforeModelHook = (*Hook)(nil)

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
		loaded:    make(map[string]instructionRecord),
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
		case sandbox.FsReadToolName, sandbox.FsListToolName, sandbox.FsSearchToolName:
			clone.Handler = h.withFYI(clone.Name, tool.Handler)
		case sandbox.FsWriteToolName, sandbox.FsEditToolName, sandbox.FsDeleteToolName:
			clone.Handler = h.withInstructionGuard(clone.Name, tool.Handler)
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

// BeforeModel refreshes project-root instructions on every request so edits
// made by people or external processes are visible without a file-tool call.
func (h *Hook) BeforeModel(ctx context.Context, _ *session.Session, req providers.Request) error {
	promptcontext.SetBlock(req, promptcontext.ProjectInstructions, h.projectInstructions(ctx))
	return nil
}

// ReservedTokens reports the stable request-local project context injected
// after context projection.
func (h *Hook) ReservedTokens(_ *session.Session) int64 {
	content := h.projectInstructions(context.Background())
	if content == "" {
		return 0
	}
	return session.EstimateHistoryTokens([]types.Message{{Role: types.RoleAgent, Content: content}})
}

func (h *Hook) withFYI(toolName string, next tools.ToolHandlerFunc) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		result, err := next(ctx, req)
		if err != nil || result == nil {
			return result, err
		}
		path := toolPathArgument(toolName, req.Arguments)
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

func (h *Hook) withInstructionGuard(toolName string, next tools.ToolHandlerFunc) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		path := toolPathArgument(toolName, req.Arguments)
		fyi, err := h.discover(ctx, req, toolName, path)
		if err != nil {
			h.logger.Warnw("failed to discover project instructions before mutation", "session", req.SessionID, "path", path, "error", err)
		} else if strings.TrimSpace(fyi) != "" {
			result := tools.NewToolResultActionableError(
				"new project instructions apply to this path; the mutation was not executed",
				"read the FYI instructions below, then retry only if the mutation complies",
			)
			result.FYI = fyi
			result.ErrorCode = "project_instructions_required"
			return result, nil
		}
		return next(ctx, req)
	}
}

func toolPathArgument(toolName string, arguments map[string]interface{}) string {
	if toolName == sandbox.FsSearchToolName {
		path, _ := arguments["directory"].(string)
		return path
	}
	path, _ := arguments["path"].(string)
	return path
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
	if toolName != sandbox.FsListToolName && toolName != sandbox.FsSearchToolName {
		dir = filepath.Dir(resolved)
	}
	dir = h.nearestExistingDirectory(ctx, dir)
	if dir == "" || !withinRoot(h.root, dir) {
		return "", nil
	}

	var candidates []instructionCandidate
	for _, candidateDir := range directoryChain(dir, h.root) {
		if h.isPersistentInstructionDir(candidateDir) {
			continue
		}
		candidate, scanErr := h.scanInstruction(ctx, candidateDir)
		if scanErr != nil {
			h.logger.Warnw("failed to inspect project instructions", "session", req.SessionID, "path", candidateDir, "error", scanErr)
			continue
		}
		candidates = append(candidates, candidate)
	}

	known, err := h.recordSnapshot(ctx, req)
	if err != nil {
		h.logger.Warnw("failed to read project instruction record; using memory fallback", "session", req.SessionID, "error", err)
		known = h.memorySnapshot(req.SessionID)
	}

	ready := make([]instructionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		stamp, exists := known[candidate.relativeDir]
		if exists && stamp == candidate.stamp {
			continue
		}
		if candidate.stamp.SelectedFile != "" {
			section, readErr := h.readFYISection(ctx, candidate)
			if readErr != nil {
				h.logger.Warnw("failed to read project instructions", "session", req.SessionID, "path", candidate.path, "error", readErr)
				continue
			}
			candidate.section = section
		}
		ready = append(ready, candidate)
	}

	claimed, err := h.claim(ctx, req, ready)
	if err != nil {
		h.logger.Warnw("failed to update project instruction record; using memory fallback", "session", req.SessionID, "error", err)
		claimed = h.claimInMemory(req.SessionID, ready)
	} else {
		h.syncMemory(req.SessionID, ready)
	}

	sections := make([]string, 0, len(claimed))
	for _, candidate := range claimed {
		if candidate.section != "" {
			sections = append(sections, candidate.section)
		}
	}
	return truncateWithNotice(strings.Join(sections, "\n\n"), maxFYIRunes), nil
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

type instructionStamp struct {
	SelectedFile    string `json:"selected_file"`
	ModTimeUnixNano int64  `json:"mod_time_unix_nano"`
}

type instructionRecord map[string]instructionStamp

type instructionCandidate struct {
	relativeDir string
	path        string
	stamp       instructionStamp
	section     string
}

func (h *Hook) scanInstruction(ctx context.Context, dir string) (instructionCandidate, error) {
	candidate := instructionCandidate{relativeDir: h.relativeDirectory(dir)}
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
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return candidate, err
		}
		if info.IsDir() {
			continue
		}
		candidate.path = resolved
		candidate.stamp = instructionStamp{SelectedFile: name, ModTimeUnixNano: info.ModTime().UnixNano()}
		return candidate, nil
	}
	return candidate, nil
}

func (h *Hook) readFYISection(ctx context.Context, candidate instructionCandidate) (string, error) {
	content, err := h.fs.ReadFile(ctx, candidate.path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(h.root, candidate.path)
	if err != nil {
		return "", err
	}
	return "## " + filepath.ToSlash(rel) + "\n\n" + truncateInstruction(string(content)), nil
}

func (h *Hook) recordSnapshot(ctx context.Context, req *tools.Request) (instructionRecord, error) {
	if req.SessionRecords == nil {
		return h.memorySnapshot(req.SessionID), nil
	}
	current, err := req.SessionRecords.ReadRecord(ctx, h.namespace)
	if errors.Is(err, session.ErrRecordNotFound) {
		return make(instructionRecord), nil
	}
	if err != nil {
		return nil, err
	}
	return decodeInstructionRecord(current), nil
}

func (h *Hook) claim(ctx context.Context, req *tools.Request, candidates []instructionCandidate) ([]instructionCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if req.SessionRecords == nil {
		return h.claimInMemory(req.SessionID, candidates), nil
	}
	var claimed []instructionCandidate
	err := req.SessionRecords.UpdateRecord(ctx, h.namespace, func(current []byte) ([]byte, error) {
		record := decodeInstructionRecord(current)
		claimed = claimed[:0]
		for _, candidate := range candidates {
			if stamp, ok := record[candidate.relativeDir]; ok && stamp == candidate.stamp {
				continue
			}
			record[candidate.relativeDir] = candidate.stamp
			claimed = append(claimed, candidate)
		}
		return json.Marshal(record)
	})
	return claimed, err
}

func decodeInstructionRecord(data []byte) instructionRecord {
	record := make(instructionRecord)
	if len(data) == 0 || json.Unmarshal(data, &record) != nil {
		return make(instructionRecord)
	}
	return record
}

func (h *Hook) memorySnapshot(sessionID string) instructionRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneInstructionRecord(h.loaded[sessionID])
}

func (h *Hook) claimInMemory(sessionID string, candidates []instructionCandidate) []instructionCandidate {
	h.mu.Lock()
	defer h.mu.Unlock()
	record := h.loaded[sessionID]
	if record == nil {
		record = make(instructionRecord)
		h.loaded[sessionID] = record
	}
	var claimed []instructionCandidate
	for _, candidate := range candidates {
		if stamp, ok := record[candidate.relativeDir]; ok && stamp == candidate.stamp {
			continue
		}
		record[candidate.relativeDir] = candidate.stamp
		claimed = append(claimed, candidate)
	}
	return claimed
}

func (h *Hook) syncMemory(sessionID string, candidates []instructionCandidate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	record := h.loaded[sessionID]
	if record == nil {
		record = make(instructionRecord)
		h.loaded[sessionID] = record
	}
	for _, candidate := range candidates {
		record[candidate.relativeDir] = candidate.stamp
	}
}

func cloneInstructionRecord(record instructionRecord) instructionRecord {
	cloned := make(instructionRecord, len(record))
	for dir, stamp := range record {
		cloned[dir] = stamp
	}
	return cloned
}

func (h *Hook) projectInstructions(ctx context.Context) string {
	var sections []string
	for _, dir := range []string{h.root, filepath.Join(h.root, ".friday")} {
		candidate, err := h.scanInstruction(ctx, dir)
		if err != nil {
			h.logger.Warnw("failed to inspect persistent project instructions", "path", dir, "error", err)
			continue
		}
		if candidate.stamp.SelectedFile == "" {
			continue
		}
		content, err := h.fs.ReadFile(ctx, candidate.path)
		if err != nil {
			h.logger.Warnw("failed to read persistent project instructions", "path", candidate.path, "error", err)
			continue
		}
		sections = append(sections, "Contents of "+candidate.path+":\n\n"+truncateInstruction(string(content)))
	}
	if len(sections) == 0 {
		return ""
	}
	body := truncateWithNotice(strings.Join(sections, "\n\n"), maxFYIRunes)
	return "<system-reminder>\n" + projectInstructionsIntro + "\n\n" + body + "\n\n" + projectInstructionsOutro + "\n</system-reminder>"
}

func (h *Hook) isPersistentInstructionDir(dir string) bool {
	dir = filepath.Clean(dir)
	return dir == h.root || dir == filepath.Join(h.root, ".friday")
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

func truncateInstruction(content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > maxInstructionLines {
		content = strings.Join(lines[:maxInstructionLines], "")
		return appendTruncationNotice(content, maxInstructionRunes)
	}
	return truncateWithNotice(content, maxInstructionRunes)
}

func truncateWithNotice(content string, limit int) string {
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return appendTruncationNotice(content, limit)
}

func appendTruncationNotice(content string, limit int) string {
	notice := []rune("\n\n[Instruction content truncated by Friday.]")
	bodyLimit := limit - len(notice)
	if bodyLimit < 0 {
		bodyLimit = 0
	}
	runes := []rune(strings.TrimRight(content, "\n"))
	if len(runes) > bodyLimit {
		runes = runes[:bodyLimit]
	}
	return string(runes) + string(notice)
}
