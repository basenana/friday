package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

var ErrLifecycleClosed = errors.New("session lifecycle is closed")

// AssociatedSpec identifies a persisted session private to this root's
// lifecycle. ResumeID is only for adopting an ID recovered from trusted
// trusted application state.
type AssociatedSpec struct {
	Key      string
	ResumeID string
}

// SessionLifecycle is a capability bound to exactly one root session. It does
// not expose global lookup, listing, current pointers, archive, or deletion.
type SessionLifecycle interface {
	Current() *coresession.Session
	RootID() string
	Fork() (*coresession.Session, error)
	CreateTemporary(opts ...coresession.Option) (*coresession.Session, error)
	GetOrCreateAssociated(ctx context.Context, spec AssociatedSpec, opts ...coresession.Option) (*coresession.Session, bool, error)
	Release(child *coresession.Session) error
	Close() error
}

// RootCatalog creates and opens top-level sessions. Callers choose a catalog
// (global or project-scoped); agents receive only the resulting lifecycle.
type RootCatalog interface {
	CreateRoot(context.Context, providers.Client, ...coresession.Option) (SessionLifecycle, error)
	OpenRoot(context.Context, string, providers.Client, ...coresession.Option) (SessionLifecycle, error)
}

type lifecycle struct {
	mu sync.Mutex

	root      *coresession.Session
	client    providers.Client
	store     Store
	relations RelationStore

	forks      map[string]*coresession.Session
	temporary  map[string]*coresession.Session
	associated map[string]*coresession.Session
	closed     bool
}

func newLifecycle(root *coresession.Session, client providers.Client, store Store) SessionLifecycle {
	relations, _ := store.(RelationStore)
	return &lifecycle{
		root:       root,
		client:     client,
		store:      store,
		relations:  relations,
		forks:      make(map[string]*coresession.Session),
		temporary:  make(map[string]*coresession.Session),
		associated: make(map[string]*coresession.Session),
	}
}

// BindLifecycle attaches root-local lifecycle capabilities to an already
// loaded session. It is primarily used by compatibility adapters while callers
// migrate from Manager-returned sessions to RootCatalog.
func BindLifecycle(root *coresession.Session, client providers.Client, store Store) SessionLifecycle {
	return newLifecycle(root, client, store)
}

func (l *lifecycle) Current() *coresession.Session { return l.root }
func (l *lifecycle) RootID() string                { return l.root.ID }

func (l *lifecycle) Fork() (*coresession.Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrLifecycleClosed
	}
	child := l.root.Fork()
	l.forks[child.ID] = child
	return child, nil
}

func (l *lifecycle) CreateTemporary(opts ...coresession.Option) (*coresession.Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrLifecycleClosed
	}
	child := l.root.NewTemporaryChild(opts...)
	l.temporary[child.ID] = child
	return child, nil
}

func (l *lifecycle) GetOrCreateAssociated(ctx context.Context, spec AssociatedSpec, opts ...coresession.Option) (*coresession.Session, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := strings.TrimSpace(spec.Key)
	if key == "" {
		return nil, false, errors.New("associated session key is required")
	}
	if l.relations == nil {
		return nil, false, errors.New("session store does not support lifecycle relations")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, false, ErrLifecycleClosed
	}
	if existing := l.associated[key]; existing != nil {
		return existing, false, nil
	}

	unlock, err := l.relations.AcquireRelationLock(l.root.ID, key)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	relation, err := l.relations.GetRelation(l.root.ID, key)
	if err != nil && !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("load session relation: %w", err)
	}
	if relation != nil {
		child, loadErr := l.store.Load(relation.SessionID, l.client, opts...)
		if loadErr == nil {
			if err := l.root.AttachChild(child); err != nil {
				return nil, false, err
			}
			l.associated[key] = child
			return child, false, nil
		}
	}

	// A trusted resume ID lets existing application metadata be adopted without
	// changing the persisted SessionMeta schema.
	if resumeID := strings.TrimSpace(spec.ResumeID); resumeID != "" {
		if child, loadErr := l.store.Load(resumeID, l.client, opts...); loadErr == nil {
			now := time.Now()
			createdAt := now
			if relation != nil && !relation.CreatedAt.IsZero() {
				createdAt = relation.CreatedAt
			}
			rel := Relation{Version: 1, RootID: l.root.ID, Key: key, SessionID: resumeID, Kind: RelationKindAssociated, CreatedAt: createdAt, UpdatedAt: now}
			if err := l.relations.PutRelation(rel); err != nil {
				return nil, false, err
			}
			if err := l.root.AttachChild(child); err != nil {
				return nil, false, err
			}
			l.associated[key] = child
			return child, false, nil
		}
	}

	id := types.NewID()
	child, err := l.store.Create(id, l.client, opts...)
	if err != nil {
		return nil, false, err
	}
	now := time.Now()
	createdAt := now
	if relation != nil && !relation.CreatedAt.IsZero() {
		createdAt = relation.CreatedAt
	}
	rel := Relation{Version: 1, RootID: l.root.ID, Key: key, SessionID: id, Kind: RelationKindAssociated, CreatedAt: createdAt, UpdatedAt: now}
	if err := l.relations.PutRelation(rel); err != nil {
		cleanupErr := l.store.Delete(id)
		if cleanupErr != nil {
			return nil, false, fmt.Errorf("save session relation: %w; cleanup: %v", err, cleanupErr)
		}
		return nil, false, err
	}
	if err := l.root.AttachChild(child); err != nil {
		_ = l.store.Delete(id)
		_ = l.relations.DeleteRelation(l.root.ID, key)
		return nil, false, err
	}
	l.associated[key] = child
	return child, true, nil
}

func (l *lifecycle) Release(child *coresession.Session) error {
	if child == nil {
		return errors.New("child session is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if child == l.root {
		return errors.New("cannot release lifecycle root")
	}
	if _, ok := l.forks[child.ID]; ok {
		delete(l.forks, child.ID)
		l.root.DetachChild(child)
		return nil
	}
	if _, ok := l.temporary[child.ID]; ok {
		delete(l.temporary, child.ID)
		l.root.DetachChild(child)
		return nil
	}
	for key, current := range l.associated {
		if current == child {
			delete(l.associated, key)
			l.root.DetachChild(child)
			return nil
		}
	}
	return errors.New("session is not a child of this lifecycle")
}

func (l *lifecycle) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	children := make([]*coresession.Session, 0, len(l.forks)+len(l.temporary)+len(l.associated))
	for _, child := range l.forks {
		children = append(children, child)
	}
	for _, child := range l.temporary {
		children = append(children, child)
	}
	for _, child := range l.associated {
		children = append(children, child)
	}
	l.forks = nil
	l.temporary = nil
	l.associated = nil
	l.mu.Unlock()

	for _, child := range children {
		l.root.DetachChild(child)
	}
	// Child Close would close the shared root event bus. Close exactly once.
	l.root.Close()
	return nil
}
