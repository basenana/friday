package teams

import "sync"

// Registry is a thread-safe in-memory index of loaded teams, plus a notion
// of an "active" team (set via team_load tool).
type Registry struct {
	mu       sync.RWMutex
	loader   *Loader
	teams    map[string]*Team
	activeID string
}

// NewRegistry wraps a Loader into a Registry. If the loader has not yet been
// Load()ed, the registry will lazily reflect whatever the loader has cached.
func NewRegistry(loader *Loader) *Registry {
	return &Registry{loader: loader, teams: map[string]*Team{}}
}

// Loader returns the underlying loader (for callers that need to reload).
func (r *Registry) Loader() *Loader {
	return r.loader
}

// Refresh pulls the latest snapshot from the underlying loader.
func (r *Registry) Refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.teams = map[string]*Team{}
	for _, t := range r.loader.List() {
		r.teams[t.Name] = t
	}
}

// List returns all teams currently known to the registry.
func (r *Registry) List() []*Team {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Team, 0, len(r.teams))
	for _, t := range r.teams {
		out = append(out, t)
	}
	return out
}

// Get returns a team by name.
func (r *Registry) Get(name string) (*Team, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.teams[name]
	return t, ok
}

// SetActive records the active team by name. Returns false if unknown.
func (r *Registry) SetActive(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.teams[name]; !ok {
		return false
	}
	r.activeID = name
	return true
}

// Active returns the currently active team, or nil if none.
func (r *Registry) Active() *Team {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.teams[r.activeID]
}

// ActiveName returns the name of the active team, or "".
func (r *Registry) ActiveName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.activeID
}
