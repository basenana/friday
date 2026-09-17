package agents

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/basenana/friday/core/logger"
)

// SpecProvider supplies the current declarative agent catalog.
type SpecProvider interface {
	List() []*AgentSpec
}

// Registry stores AgentSpecs keyed by normalized, case-insensitive name.
type Registry struct {
	mu       sync.RWMutex
	specs    map[string]*AgentSpec
	paths    []string
	stamp    string
	validate SpecValidator
}

func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]*AgentSpec)}
}

func (r *Registry) Register(spec *AgentSpec) {
	if spec == nil || spec.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[strings.ToLower(spec.Name)] = spec
}

func (r *Registry) Get(name string) (*AgentSpec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()
	spec, ok := r.specs[strings.ToLower(strings.TrimSpace(name))]
	return spec, ok
}

func (r *Registry) List() []*AgentSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadIfChangedLocked()
	out := make([]*AgentSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Paths returns the low-to-high-priority filesystem roots backing this
// registry. Programmatically constructed registries return nil.
func (r *Registry) Paths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.paths...)
}

// SetValidator installs generation validation used by subsequent hot reloads
// and validates the current snapshot before returning.
func (r *Registry) SetValidator(validate SpecValidator) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if validate != nil {
		for _, spec := range r.specs {
			if err := validate(spec); err != nil {
				return err
			}
		}
	}
	r.validate = validate
	return nil
}

func (r *Registry) reloadIfChangedLocked() {
	if len(r.paths) == 0 {
		return
	}
	stamp, err := agentFilesStamp(r.paths)
	if err != nil || stamp == r.stamp {
		if err != nil {
			logger.New("agents").Warnw("failed to inspect agent specs", "error", err)
		}
		return
	}
	r.stamp = stamp
	loaded, err := NewLoader(r.paths...).Load()
	if err != nil {
		logger.New("agents").Warnw("failed to hot reload agent specs", "error", err)
		return
	}
	if r.validate != nil {
		for _, spec := range loaded.specs {
			if err := r.validate(spec); err != nil {
				logger.New("agents").Warnw("hot-loaded agent catalog is invalid; keeping previous snapshot", "error", err)
				return
			}
		}
	}
	r.specs = loaded.specs
}

func agentFilesStamp(roots []string) (string, error) {
	records := make([]string, 0)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read agents directory %s: %w", root, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path := filepath.Join(root, entry.Name(), SpecFilename)
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("stat agent spec %s: %w", path, err)
			}
			records = append(records, fmt.Sprintf("%s\x00%d\x00%d", filepath.Clean(path), info.ModTime().UnixNano(), info.Size()))
		}
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return fmt.Sprintf("%x", sum[:]), nil
}
