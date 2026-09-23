package worktree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/sessions"
)

// SessionCatalog binds a worktree to exactly one active root session. The
// session entity remains in the global session store; worktree metadata holds
// only its replaceable reference.
type SessionCatalog struct {
	store      Store
	worktreeID string
	sessions   *sessions.Manager
}

var _ sessions.RootCatalog = (*SessionCatalog)(nil)

func NewSessionCatalog(store Store, worktreeID string, manager *sessions.Manager) *SessionCatalog {
	return &SessionCatalog{store: store, worktreeID: strings.TrimSpace(worktreeID), sessions: manager}
}

// EnsureRoot returns the active root bound to this worktree. A missing or
// archived soft reference is replaced while the worktree metadata lock is
// held, so concurrent callers share one replacement root.
func (c *SessionCatalog) EnsureRoot(ctx context.Context, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, string, bool, error) {
	return c.ensureRoot(ctx, "", client, opts...)
}

// CreateRoot implements sessions.RootCatalog. A worktree always owns one root,
// so creation reuses that root when it is already active.
func (c *SessionCatalog) CreateRoot(ctx context.Context, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, error) {
	lifecycle, _, _, err := c.EnsureRoot(ctx, client, opts...)
	return lifecycle, err
}

// OpenRoot implements sessions.RootCatalog and refuses root IDs not owned by
// this worktree.
func (c *SessionCatalog) OpenRoot(ctx context.Context, id string, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("root session id is required")
	}
	lifecycle, _, _, err := c.ensureRoot(ctx, id, client, opts...)
	return lifecycle, err
}

func (c *SessionCatalog) ensureRoot(ctx context.Context, expectedID string, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	if c.store == nil {
		return nil, "", false, errors.New("worktree store is required")
	}
	if c.sessions == nil {
		return nil, "", false, errors.New("session manager is required")
	}
	if c.worktreeID == "" {
		return nil, "", false, errors.New("worktree id is required")
	}

	var (
		lifecycle sessions.SessionLifecycle
		rootID    string
		created   bool
		notOwned  bool
	)
	err := c.store.UpdateMetadata(c.worktreeID, func(meta *Metadata) error {
		active, err := c.sessions.IsActive(meta.SessionID)
		if err != nil {
			return fmt.Errorf("validate worktree session: %w", err)
		}
		if !active {
			lifecycle, err = c.sessions.CreateRoot(ctx, client, opts...)
			if err != nil {
				return fmt.Errorf("create worktree root: %w", err)
			}
			rootID = lifecycle.RootID()
			meta.SessionID = rootID
			created = true
		} else {
			rootID = meta.SessionID
		}

		if expectedID != "" && expectedID != rootID {
			notOwned = true
			return nil
		}
		if lifecycle != nil {
			return nil
		}
		lifecycle, err = c.sessions.OpenRoot(ctx, rootID, client, opts...)
		if err != nil {
			return fmt.Errorf("open worktree root: %w", err)
		}
		return nil
	})
	if err != nil {
		var cleanupErr error
		if lifecycle != nil {
			cleanupErr = lifecycle.Close()
		}
		if created {
			deleteErr := c.sessions.DeleteRoot(rootID)
			cleanupErr = errors.Join(cleanupErr, deleteErr)
		}
		if cleanupErr != nil {
			return nil, "", false, errors.Join(err, fmt.Errorf("clean up replacement root %s: %w", rootID, cleanupErr))
		}
		return nil, "", false, err
	}
	if notOwned {
		if lifecycle != nil {
			_ = lifecycle.Close()
		}
		return nil, "", false, fmt.Errorf("session is not referenced by worktree: %s", expectedID)
	}
	return lifecycle, rootID, created, nil
}
