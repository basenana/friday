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
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Root      string    `json:"root"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	if store == nil {
		return nil, fmt.Errorf("project store is required")
	}
	now := time.Now()
	meta := Metadata{Version: 1, ID: ProjectID(canonical), Root: canonical, CreatedAt: now, UpdatedAt: now}
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
	base := filepath.Base(canonicalRoot)
	base = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, base)
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "project"
	}
	sum := sha256.Sum256([]byte(canonicalRoot))
	return fmt.Sprintf("%s-%x", base, sum[:6])
}

func (p *Project) ID() string   { return p.meta.ID }
func (p *Project) Root() string { return p.meta.Root }

func (p *Project) CurrentSessionID() (string, error) {
	return p.store.Current(p.ID())
}

func (p *Project) SetCurrentSession(id string) error {
	return p.store.SetCurrent(p.ID(), id)
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
