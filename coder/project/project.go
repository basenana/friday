package project

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type Metadata struct {
	Version         int       `json:"version"`
	ID              string    `json:"id"`
	Name            string    `json:"name,omitempty"`
	Root            string    `json:"root"`
	Repository      string    `json:"repository,omitempty"`
	CodebaseEnabled bool      `json:"codebase_enabled,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Identity identifies a logical project independently of its checkout path.
type Identity struct {
	ID         string
	Name       string
	Repository string
}

type SessionRef struct {
	Version   int       `json:"version"`
	SessionID string    `json:"session_id"`
	AddedAt   time.Time `json:"added_at"`
}

type Store interface {
	Ensure(Metadata) error
	Current(projectID string) (string, error)
	SetCurrent(projectID, sessionID string) error
	ListRefs(projectID string) ([]SessionRef, error)
	HasRef(projectID, sessionID string) (bool, error)
	AddRef(projectID string, ref SessionRef) error
	RemoveRef(projectID, sessionID string) error
	LoadUserHistory(projectID string) ([]UserHistoryEntry, error)
	AppendUserHistory(projectID string, entry UserHistoryEntry) (bool, error)
	CodebaseEnabled(projectID string) (bool, error)
	SetCodebaseEnabled(projectID string, enabled bool) error
}

type Project struct {
	meta  Metadata
	store Store
}

func Open(root string, store Store) (*Project, error) {
	canonical, err := CanonicalRoot(root)
	if err != nil {
		return nil, err
	}
	return OpenWithIdentity(canonical, Identity{
		ID:         ProjectID(canonical),
		Name:       filepath.Base(canonical),
		Repository: canonical,
	}, store)
}

func OpenWithIdentity(root string, identity Identity, store Store) (*Project, error) {
	canonical, err := CanonicalRoot(root)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, fmt.Errorf("project store is required")
	}
	if !validID(identity.ID) {
		return nil, fmt.Errorf("project identity id is required")
	}
	if strings.TrimSpace(identity.Name) == "" {
		return nil, fmt.Errorf("project identity name is required")
	}
	if strings.TrimSpace(identity.Repository) == "" {
		return nil, fmt.Errorf("project identity repository is required")
	}
	if migrator, ok := store.(interface {
		MigrateIdentity(string, Identity) error
	}); ok {
		if err := migrator.MigrateIdentity(canonical, identity); err != nil {
			return nil, fmt.Errorf("migrate project identity: %w", err)
		}
	}
	now := time.Now()
	meta := Metadata{
		Version: 2, ID: identity.ID, Name: identity.Name, Root: canonical, Repository: identity.Repository,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Ensure(meta); err != nil {
		return nil, err
	}
	return &Project{meta: meta, store: store}, nil
}

func CanonicalRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("project root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project root symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat project root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project root is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

func ProjectID(canonicalRoot string) string {
	base := ProjectIDPrefix(filepath.Base(canonicalRoot))
	sum := sha256.Sum256([]byte(canonicalRoot))
	return fmt.Sprintf("%s-%x", base, sum[:6])
}

// ProjectIDPrefix returns the safe display prefix used in project IDs.
func ProjectIDPrefix(name string) string {
	base := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "project"
	}
	return base
}

func (p *Project) ID() string   { return p.meta.ID }
func (p *Project) Root() string { return p.meta.Root }

func (p *Project) CurrentSessionID() (string, error) {
	return p.store.Current(p.ID())
}

func (p *Project) SetCurrentSession(id string) error {
	return p.store.SetCurrent(p.ID(), id)
}

func (p *Project) CodebaseEnabled() (bool, error) {
	return p.store.CodebaseEnabled(p.ID())
}

func (p *Project) SetCodebaseEnabled(enabled bool) error {
	return p.store.SetCodebaseEnabled(p.ID(), enabled)
}

func (p *Project) ListSessionRefs() ([]SessionRef, error) {
	return p.store.ListRefs(p.ID())
}

func (p *Project) HasSession(id string) (bool, error) {
	return p.store.HasRef(p.ID(), id)
}

func (p *Project) AddSession(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("session id is required")
	}
	return p.store.AddRef(p.ID(), SessionRef{Version: 1, SessionID: id, AddedAt: time.Now()})
}

func (p *Project) RemoveSession(id string) error {
	return p.store.RemoveRef(p.ID(), id)
}

func (p *Project) LoadUserHistory() ([]UserHistoryEntry, error) {
	return p.store.LoadUserHistory(p.ID())
}

func (p *Project) AppendUserHistory(text, sessionID string, createdAt time.Time) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return p.store.AppendUserHistory(p.ID(), UserHistoryEntry{
		Version: 1, Text: text, CreatedAt: createdAt, SessionID: sessionID,
	})
}
