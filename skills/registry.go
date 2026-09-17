package skills

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/basenana/friday/core/logger"
)

// ErrDeleteUnsupported is returned when the registry's underlying provider
// cannot delete skills (only the filesystem Loader supports deletion).
var ErrDeleteUnsupported = errors.New("delete is not supported by this skills provider")

// Registry manages a cached, mtime-refreshable skill snapshot and provides
// thread-safe access.
type Registry struct {
	provider Provider
	mu       sync.RWMutex
	stamp    string
}

// NewRegistry creates a new skill registry from a provider
func NewRegistry(provider Provider) *Registry {
	r := &Registry{
		provider: provider,
	}
	if loader, ok := provider.(*Loader); ok {
		r.stamp, _ = skillFilesStamp(loader.SkillsPaths())
	}
	return r
}

// List returns all loaded skills
func (r *Registry) List() []*Skill {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()

	// Defensive copy so callers cannot mutate the provider's slice.
	skills := r.provider.List()
	out := make([]*Skill, len(skills))
	copy(out, skills)
	return out
}

// Get returns a skill by name
func (r *Registry) Get(name string) (*Skill, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()

	return r.provider.Get(name)
}

// LoadResource loads a resource file from a skill
func (r *Registry) LoadResource(skillName, resourcePath string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()

	return r.provider.LoadResource(skillName, resourcePath)
}

// ListResources lists all resources in a skill
func (r *Registry) ListResources(skillName string) ([]*Resource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()

	return r.provider.ListResources(skillName)
}

// ListFiles lists directory entries within a skill at the given sub-path.
func (r *Registry) ListFiles(skillName, subPath string) ([]fs.DirEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()

	return r.provider.ListFiles(skillName, subPath)
}

// ReadFile reads an arbitrary file within a skill's directory.
func (r *Registry) ReadFile(skillName, filePath string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()

	return r.provider.ReadFile(skillName, filePath)
}

// Refresh reloads all skills from disk.
// Only works when the underlying provider is a *Loader; otherwise returns nil.
func (r *Registry) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	loader, ok := r.provider.(*Loader)
	if !ok {
		return nil
	}

	newLoader := NewLoader(loader.SkillsPaths()...)
	if err := newLoader.Load(); err != nil {
		return err
	}

	r.provider = newLoader
	r.stamp, _ = skillFilesStamp(loader.SkillsPaths())
	return nil
}

// Locations returns all skills directory paths.
// Only works when the underlying provider is a *Loader; otherwise returns nil.
func (r *Registry) Locations() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()
	loader, ok := r.provider.(*Loader)
	if !ok {
		return nil
	}
	return append([]string(nil), loader.SkillsPaths()...)
}

// Delete removes a skill.
// Only works when the underlying provider is a *Loader; otherwise returns an error.
func (r *Registry) Delete(skillName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	loader, ok := r.provider.(*Loader)
	if !ok {
		return ErrDeleteUnsupported
	}

	return loader.Delete(skillName)
}

// reloadIfChangedLocked refreshes filesystem-backed providers lazily. Reload
// failures leave the last usable snapshot installed.
func (r *Registry) reloadIfChangedLocked() {
	loader, ok := r.provider.(*Loader)
	if !ok {
		return
	}
	stamp, err := skillFilesStamp(loader.SkillsPaths())
	if err != nil || stamp == r.stamp {
		if err != nil {
			logger.New("skills").Warnw("failed to inspect skill files", "error", err)
		}
		return
	}
	r.stamp = stamp
	newLoader := NewLoader(loader.SkillsPaths()...)
	if err := newLoader.Load(); err != nil {
		logger.New("skills").Warnw("failed to hot reload skills", "error", err)
		return
	}
	if len(newLoader.loadErrors) > 0 {
		logger.New("skills").Warnw("skill hot reload contains invalid definitions; keeping previous snapshot", "error", errors.Join(newLoader.loadErrors...))
		return
	}
	r.provider = newLoader
}

func skillFilesStamp(roots []string) (string, error) {
	records := make([]string, 0)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read skills directory %s: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path := filepath.Join(root, entry.Name(), "SKILL.md")
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("stat skill file %s: %w", path, err)
			}
			records = append(records, fmt.Sprintf("%s\x00%d\x00%d", filepath.Clean(path), info.ModTime().UnixNano(), info.Size()))
		}
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return fmt.Sprintf("%x", sum[:]), nil
}

// ErrSkillNotFound is returned when a skill is not found
type ErrSkillNotFound struct {
	Name string
}

func (e ErrSkillNotFound) Error() string {
	return "skill not found: " + e.Name
}
