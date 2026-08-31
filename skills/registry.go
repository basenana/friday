package skills

import (
	"errors"
	"io/fs"
	"sync"
)

// ErrDeleteUnsupported is returned when the registry's underlying provider
// cannot delete skills (only the filesystem Loader supports deletion).
var ErrDeleteUnsupported = errors.New("delete is not supported by this skills provider")

// Registry manages loaded skills and provides thread-safe access
type Registry struct {
	provider Provider
	mu       sync.RWMutex
}

// NewRegistry creates a new skill registry from a provider
func NewRegistry(provider Provider) *Registry {
	return &Registry{
		provider: provider,
	}
}

// List returns all loaded skills
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Defensive copy so callers cannot mutate the provider's slice.
	skills := r.provider.List()
	out := make([]*Skill, len(skills))
	copy(out, skills)
	return out
}

// Get returns a skill by name
func (r *Registry) Get(name string) (*Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.provider.Get(name)
}

// LoadResource loads a resource file from a skill
func (r *Registry) LoadResource(skillName, resourcePath string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.provider.LoadResource(skillName, resourcePath)
}

// ListResources lists all resources in a skill
func (r *Registry) ListResources(skillName string) ([]*Resource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.provider.ListResources(skillName)
}

// ListFiles lists directory entries within a skill at the given sub-path.
func (r *Registry) ListFiles(skillName, subPath string) ([]fs.DirEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.provider.ListFiles(skillName, subPath)
}

// ReadFile reads an arbitrary file within a skill's directory.
func (r *Registry) ReadFile(skillName, filePath string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

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
	return nil
}

// Locations returns all skills directory paths.
// Only works when the underlying provider is a *Loader; otherwise returns nil.
func (r *Registry) Locations() []string {
	loader, ok := r.provider.(*Loader)
	if !ok {
		return nil
	}
	return loader.SkillsPaths()
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

// ErrSkillNotFound is returned when a skill is not found
type ErrSkillNotFound struct {
	Name string
}

func (e ErrSkillNotFound) Error() string {
	return "skill not found: " + e.Name
}
