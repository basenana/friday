package worktreectx

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/promptcontext"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type Context struct {
	ProjectName  string
	ProjectRoot  string
	WorktreeName string
	Branch       string
	WorktreeRoot string
	SessionID    string
}

const blockTemplate = `<worktree-context>
Project: %s
Project code root: %s
Active worktree: %s
Active branch: %s
Active worktree root: %s
Worktree session: %s

- Relative filesystem and shell paths resolve from the active worktree root above.
- The full project code root is writable. Other checkouts may be inspected when cross-branch context is useful.
- Default to modifying only the active worktree. Do not modify another checkout unless the user explicitly requests it.
- A foreground worktree selection does not change this runtime's worktree, cwd, or context while it continues in the background.
</worktree-context>`

// Hook injects the worktree binding of one runtime into every model request.
// One runtime serves a whole scope of sessions, so the session line is rendered
// from the live session instead of the build-time binding.
type Hook struct {
	info    Context
	branch  string
	content string
}

var _ session.BeforeModelHook = (*Hook)(nil)

func New(info Context) (*Hook, error) {
	if strings.TrimSpace(info.ProjectName) == "" || strings.TrimSpace(info.ProjectRoot) == "" ||
		strings.TrimSpace(info.WorktreeName) == "" || strings.TrimSpace(info.WorktreeRoot) == "" ||
		strings.TrimSpace(info.SessionID) == "" {
		return nil, fmt.Errorf("complete worktree context is required")
	}
	branch := strings.TrimSpace(info.Branch)
	if branch == "" {
		branch = "detached"
	}
	hook := &Hook{info: info, branch: branch}
	hook.content = hook.block(info.SessionID)
	return hook, nil
}

func (h *Hook) block(sessionID string) string {
	return fmt.Sprintf(blockTemplate, h.info.ProjectName, h.info.ProjectRoot, h.info.WorktreeName, h.branch, h.info.WorktreeRoot, sessionID)
}

func (h *Hook) BeforeModel(_ context.Context, sess *session.Session, req providers.Request) error {
	if h == nil {
		return nil
	}
	content := h.content
	if sess != nil {
		if id := strings.TrimSpace(sess.ID); id != "" && id != h.info.SessionID {
			content = h.block(id)
		}
	}
	promptcontext.SetBlock(req, promptcontext.WorktreeContext, content)
	return nil
}

func (h *Hook) ReservedTokens(_ *session.Session) int64 {
	if h == nil || h.content == "" {
		return 0
	}
	return session.EstimateHistoryTokens([]types.Message{{Role: types.RoleAgent, Content: h.content}})
}
