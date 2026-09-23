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

type Hook struct {
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
	content := fmt.Sprintf(`<worktree-context>
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
</worktree-context>`, info.ProjectName, info.ProjectRoot, info.WorktreeName, branch, info.WorktreeRoot, info.SessionID)
	return &Hook{content: content}, nil
}

func (h *Hook) BeforeModel(_ context.Context, _ *session.Session, req providers.Request) error {
	if h != nil {
		promptcontext.SetBlock(req, promptcontext.WorktreeContext, h.content)
	}
	return nil
}

func (h *Hook) ReservedTokens(_ *session.Session) int64 {
	if h == nil || h.content == "" {
		return 0
	}
	return session.EstimateHistoryTokens([]types.Message{{Role: types.RoleAgent, Content: h.content}})
}
