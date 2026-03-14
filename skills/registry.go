package skills

import (
	"sync"
)

// Registry manages loaded skills and provides thread-safe access
type Registry struct {
	loader *Loader
	mu     sync.RWMutex
}

// NewRegistry creates a new skill registry from an existing loader
func NewRegistry(loader *Loader) *Registry {
	return &Registry{
		loader: loader,
	}
}

// List returns all loaded skills
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent race conditions if skills are modified
	skills := r.loader.List()
	result := make([]*Skill, len(skills))
	copy(result, skills)
	return result
}

// Get returns a skill by name
func (r *Registry) Get(name string) (*Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skill := r.loader.Get(name)
	if skill == nil {
		return nil, ErrSkillNotFound{name}
	}
	return skill, nil
}

// LoadResource loads a resource file from a skill
func (r *Registry) LoadResource(skillName, resourcePath string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.loader.LoadResource(skillName, resourcePath)
}

// ListResources lists all resources in a skill
func (r *Registry) ListResources(skillName string) ([]*Resource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.loader.ListResources(skillName)
}

// Refresh reloads all skills from disk
func (r *Registry) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create a new loader and reload
	newLoader := NewLoader(r.loader.SkillsPaths()...)
	if err := newLoader.Load(); err != nil {
		return err
	}

	r.loader = newLoader
	return nil
}

// Locations returns all skills directory paths
func (r *Registry) Locations() []string {
	return r.loader.SkillsPaths()
}

// Delete removes a skill
func (r *Registry) Delete(skillName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.loader.Delete(skillName)
}

// ErrSkillNotFound is returned when a skill is not found
type ErrSkillNotFound struct {
	Name string
}

func (e ErrSkillNotFound) Error() string {
	return "skill not found: " + e.Name
}
