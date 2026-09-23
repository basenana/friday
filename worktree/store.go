package worktree

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Metadata is the durable association between a logical project and one Git worktree.
// Git state is discovered at runtime and is deliberately not stored here.
type Metadata struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	SessionID  string    `json:"session_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Store persists the worktrees owned by one logical project.
type Store interface {
	Ensure(Metadata) (Metadata, error)
	Get(target string) (Metadata, error)
	List() ([]Metadata, error)
	UpdateMetadata(id string, update func(*Metadata) error) error
	UpdateSession(id, sessionID string) error
	Touch(id string) error
	Remove(id string) error
}

type fileStore struct {
	projectDir string
	syncDir    func(string) error
}

// NewStore stores worktree metadata below projectsPath/<projectID>/worktrees.
func NewStore(projectsPath, projectID string) (Store, error) {
	if !validWorktreeID(projectID) {
		return nil, fmt.Errorf("invalid project id")
	}
	return &fileStore{projectDir: filepath.Join(projectsPath, projectID), syncDir: syncDirectory}, nil
}

func (s *fileStore) worktreesDir() string { return filepath.Join(s.projectDir, "worktrees") }
func (s *fileStore) lockPath() string     { return filepath.Join(s.worktreesDir(), ".lock") }
func (s *fileStore) metadataPath(id string) string {
	return filepath.Join(s.worktreesDir(), id, "worktree.json")
}

func (s *fileStore) Ensure(meta Metadata) (Metadata, error) {
	meta, err := normalizeMetadata(meta)
	if err != nil {
		return Metadata{}, err
	}
	var result Metadata
	err = withRegistryLock(s.lockPath(), func() error {
		items, err := s.list()
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == meta.ID {
				if !sameCanonicalPath(item.Path, meta.Path) {
					return fmt.Errorf("worktree metadata identity collision for %s", meta.ID)
				}
				meta = reconcileMetadata(item, meta)
				return s.write(meta)
			}
			if sameCanonicalPath(item.Path, meta.Path) {
				meta = reconcileMetadata(item, meta)
				return s.write(meta)
			}
		}
		if err := s.write(meta); err != nil {
			return err
		}
		result = meta
		return nil
	})
	if err == nil && result.ID == "" {
		result = meta
	}
	return result, err
}

// importLegacy persists migration metadata without applying the normal
// last-used touch performed by Ensure reconciliation. Existing runtime-owned
// mutable fields win; migration only restores older creation history, newer
// legacy use history, and a missing soft session reference.
func (s *fileStore) importLegacy(meta Metadata) (Metadata, error) {
	meta, err := normalizeMetadata(meta)
	if err != nil {
		return Metadata{}, err
	}
	var result Metadata
	err = withRegistryLock(s.lockPath(), func() error {
		items, err := s.list()
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == meta.ID && !sameCanonicalPath(item.Path, meta.Path) {
				return fmt.Errorf("worktree metadata identity collision for %s", meta.ID)
			}
			if item.ID == meta.ID || sameCanonicalPath(item.Path, meta.Path) {
				result = reconcileLegacyMetadata(item, meta)
				return s.write(result)
			}
		}
		result = meta
		return s.write(result)
	})
	return result, err
}

func (s *fileStore) Get(target string) (Metadata, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return Metadata{}, fmt.Errorf("worktree target is required")
	}
	items, err := s.List()
	if err != nil {
		return Metadata{}, err
	}
	var matches []Metadata
	for _, item := range items {
		if item.ID == target || item.Name == target || item.Path == target || item.Branch == target {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Metadata{}, fmt.Errorf("ambiguous worktree %q", target)
	}
	for _, item := range items {
		if strings.HasPrefix(item.ID, target) || strings.HasPrefix(item.Name, target) || strings.HasPrefix(item.Branch, target) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Metadata{}, fmt.Errorf("ambiguous worktree %q", target)
	}
	return Metadata{}, fmt.Errorf("worktree not found: %s", target)
}

func (s *fileStore) List() ([]Metadata, error) {
	var result []Metadata
	err := withRegistryLock(s.lockPath(), func() error {
		items, err := s.list()
		result = items
		return err
	})
	return result, err
}

func (s *fileStore) UpdateSession(id, sessionID string) error {
	return s.UpdateMetadata(id, func(meta *Metadata) error {
		meta.SessionID = strings.TrimSpace(sessionID)
		return nil
	})
}

func (s *fileStore) Touch(id string) error {
	return s.UpdateMetadata(id, func(*Metadata) error { return nil })
}

func (s *fileStore) Remove(id string) error {
	if !validWorktreeID(id) {
		return fmt.Errorf("invalid worktree id")
	}
	return withRegistryLock(s.lockPath(), func() error {
		err := os.RemoveAll(filepath.Dir(s.metadataPath(id)))
		if err != nil {
			return fmt.Errorf("remove worktree metadata: %w", err)
		}
		return nil
	})
}

// UpdateMetadata updates one metadata record while holding the store lock.
// Callers that need to read and replace a soft reference atomically must use
// this instead of separate Get and UpdateSession calls.
func (s *fileStore) UpdateMetadata(id string, update func(*Metadata) error) error {
	if !validWorktreeID(id) {
		return fmt.Errorf("invalid worktree id")
	}
	if update == nil {
		return errors.New("worktree metadata update is required")
	}
	return withRegistryLock(s.lockPath(), func() error {
		meta, err := s.read(id)
		if err != nil {
			return err
		}
		if err := update(&meta); err != nil {
			return err
		}
		meta.LastUsedAt = time.Now()
		return s.write(meta)
	})
}

func (s *fileStore) list() ([]Metadata, error) {
	entries, err := os.ReadDir(s.worktreesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.read(entry.Name())
		if err != nil {
			return nil, err
		}
		items = append(items, meta)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *fileStore) read(id string) (Metadata, error) {
	if !validWorktreeID(id) {
		return Metadata{}, fmt.Errorf("invalid worktree id")
	}
	data, err := os.ReadFile(s.metadataPath(id))
	if err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Metadata{}, fmt.Errorf("decode worktree metadata: %w", err)
	}
	meta, err = normalizeMetadata(meta)
	if err != nil {
		return Metadata{}, fmt.Errorf("worktree metadata identity mismatch for %s: %w", id, err)
	}
	if meta.ID != id {
		return Metadata{}, fmt.Errorf("worktree metadata identity mismatch for %s", id)
	}
	return meta, nil
}

func (s *fileStore) write(meta Metadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := s.metadataPath(meta.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return s.syncMetadataHierarchy(path)
}

func (s *fileStore) syncMetadataHierarchy(path string) error {
	syncDir := s.syncDir
	if syncDir == nil {
		syncDir = syncDirectory
	}
	dirs := []string{
		filepath.Dir(path),
		s.worktreesDir(),
		s.projectDir,
		filepath.Dir(s.projectDir),
		filepath.Dir(filepath.Dir(s.projectDir)),
	}
	last := ""
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if dir == last {
			continue
		}
		if err := syncDir(dir); err != nil {
			return fmt.Errorf("sync worktree metadata directory %s: %w", dir, err)
		}
		last = dir
	}
	return nil
}

func normalizeMetadata(meta Metadata) (Metadata, error) {
	if meta.Version == 0 {
		meta.Version = 1
	}
	if meta.Version != 1 {
		return Metadata{}, fmt.Errorf("unsupported worktree metadata version")
	}
	if !validWorktreeID(meta.ID) {
		return Metadata{}, fmt.Errorf("invalid worktree id")
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Branch = strings.TrimSpace(meta.Branch)
	if meta.Name == "" || meta.Branch == "" {
		return Metadata{}, fmt.Errorf("worktree name and branch are required")
	}
	path, err := canonicalWorktreePath(meta.Path)
	if err != nil {
		return Metadata{}, err
	}
	meta.Path = path
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now()
	}
	if meta.LastUsedAt.IsZero() {
		meta.LastUsedAt = meta.CreatedAt
	}
	return meta, nil
}

func canonicalWorktreePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("worktree path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	remaining := []string(nil)
	for {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			return filepath.Clean(filepath.Join(append([]string{resolved}, remaining...)...)), nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("resolve worktree path symlinks: %s", path)
		}
		remaining = append([]string{filepath.Base(abs)}, remaining...)
		abs = parent
	}
}

func reconcileMetadata(existing, incoming Metadata) Metadata {
	existing.Name = incoming.Name
	existing.Branch = incoming.Branch
	if incoming.SessionID != "" {
		existing.SessionID = incoming.SessionID
	}
	existing.LastUsedAt = time.Now()
	return existing
}

func reconcileLegacyMetadata(existing, incoming Metadata) Metadata {
	existing.CreatedAt = earliestNonZero(existing.CreatedAt, incoming.CreatedAt)
	if incoming.LastUsedAt.After(existing.LastUsedAt) {
		existing.LastUsedAt = incoming.LastUsedAt
	}
	if existing.SessionID == "" && incoming.SessionID != "" {
		existing.SessionID = incoming.SessionID
	}
	return existing
}

func validWorktreeID(id string) bool {
	return id != "" && id == strings.TrimSpace(id) && id != "." && id != ".." && !strings.ContainsAny(id, `/\\`)
}

func worktreeID(path string) string {
	canonical, err := canonicalWorktreePath(path)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	name := slug(filepath.Base(canonical))
	sum := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%s-%x", name, sum[:6])
}
