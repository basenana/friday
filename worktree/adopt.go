package worktree

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/sessions"
)

// ScopeForWorktree maps one worktree to its session scope. The main checkout
// shares the project scope, so project sessions and a main-worktree session are
// the same thing; every linked worktree owns a private scope.
func ScopeForWorktree(meta Metadata, projectRoot string) string {
	canonical, err := canonicalWorktreePath(projectRoot)
	if err != nil {
		canonical = filepath.Clean(projectRoot)
	}
	if sameCanonicalPath(meta.Path, canonical) {
		return project.MainScope
	}
	return meta.ID
}

// SessionPool is the project-side ownership surface used while adopting legacy
// references. The worktree package depends on this narrow interface instead of
// the project manager, so worktree metadata can never grow a second ownership
// model again. A *project.Project satisfies it.
type SessionPool interface {
	HasSession(id string) (bool, error)
	AddSession(scope, id string) error
	CurrentSessionID(scope string) (string, error)
	SetCurrentSession(scope, id string) error
}

// AdoptSessions imports the legacy one-session-per-worktree reference into the
// project session pool and clears the legacy field. Ownership then has exactly
// one source of truth: the project session references and their scope pointers.
//
// Adoption is idempotent. A legacy reference to a session that no longer exists
// is dropped, and a reference to an archived session is adopted as ownership
// without becoming the scope's current session.
func AdoptSessions(ctx context.Context, store Store, projectRoot string, pool SessionPool, manager *sessions.Manager) error {
	if ctx == nil {
		return fmt.Errorf("adoption context is required")
	}
	if store == nil {
		return fmt.Errorf("worktree store is required")
	}
	if pool == nil {
		return fmt.Errorf("session pool is required")
	}
	if manager == nil {
		return fmt.Errorf("session manager is required")
	}
	items, err := store.List()
	if err != nil {
		return fmt.Errorf("list worktrees for adoption: %w", err)
	}
	for _, meta := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := adoptWorktreeSession(store, meta, projectRoot, pool, manager); err != nil {
			return fmt.Errorf("adopt worktree %s session: %w", meta.ID, err)
		}
	}
	return nil
}

func adoptWorktreeSession(store Store, meta Metadata, projectRoot string, pool SessionPool, manager *sessions.Manager) error {
	id := strings.TrimSpace(meta.SessionID)
	if id == "" {
		return nil
	}
	exists, err := manager.Exists(id)
	if err != nil {
		return err
	}
	if exists {
		scope := ScopeForWorktree(meta, projectRoot)
		// An already present reference declares the same ownership this adoption
		// would record, so only the first run writes it and AddedAt stays stable.
		has, err := pool.HasSession(id)
		if err != nil {
			return err
		}
		if !has {
			if err := pool.AddSession(scope, id); err != nil {
				return err
			}
		}
		active, err := manager.IsActive(id)
		if err != nil {
			return err
		}
		if active {
			if err := adoptScopeCurrent(pool, manager, scope, id); err != nil {
				return err
			}
		}
	}
	return store.UpdateSession(meta.ID, "")
}

// adoptScopeCurrent installs the adopted session as the current session of its
// scope. The legacy worktree reference is the newest record of how that scope
// was used, so it wins over an older existing pointer, while a pointer that was
// updated later is left alone.
func adoptScopeCurrent(pool SessionPool, manager *sessions.Manager, scope, id string) error {
	current, err := pool.CurrentSessionID(scope)
	if err != nil {
		return err
	}
	if current == id {
		return nil
	}
	if current != "" && sessionUpdatedAt(manager, current).After(sessionUpdatedAt(manager, id)) {
		return nil
	}
	return pool.SetCurrentSession(scope, id)
}

// sessionUpdatedAt reports when one session was last updated. An unreadable
// session sorts before everything else, so a dangling pointer never blocks
// adoption.
func sessionUpdatedAt(manager *sessions.Manager, id string) time.Time {
	meta, err := manager.GetStore().GetMeta(id)
	if err != nil || meta == nil {
		return time.Time{}
	}
	return meta.UpdatedAt
}
