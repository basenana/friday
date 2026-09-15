package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Scope string

const (
	ScopeHome    Scope = "home"
	ScopeProject Scope = "project"
)

var ErrReadOnlyWorkspace = errors.New("workspace has no writable layer")

// Layer describes one workspace overlay root. Layers are stored from lowest
// to highest priority; only the active, non-shared layer is writable.
type Layer struct {
	Root     string
	Scope    Scope
	Writable bool
}

// ResourceRoot is a layer rooted at a specific workspace-relative resource.
type ResourceRoot struct {
	Path     string
	Scope    Scope
	Writable bool
}

// Config is the subset of application configuration needed to construct a
// correctly scoped workspace without coupling this package to config.Config.
type Config interface {
	WorkspacePath() string
	WorkspaceFallbackPaths() []string
	ProjectScoped() bool
	MemoryPath() string
}

type Workspace struct {
	basePath string
	layers   []Layer
	memPath  string
	specs    []FileSpec
}

// NewWorkspace creates a writable workspace with optional lower-priority read
// fallbacks. Reads check workspacePath first and then each fallback in order;
// writes and deletes always target workspacePath.
func NewWorkspace(workspacePath, memoryPath string, fallbackPaths ...string) *Workspace {
	return newWorkspace(workspacePath, memoryPath, len(fallbackPaths) > 0, false, fallbackPaths...)
}

// NewFromConfig constructs the canonical HOME -> project overlay. When a
// project explicitly points its active workspace at the HOME workspace, the
// duplicate is kept as one read-only HOME layer so project operations cannot
// accidentally mutate inherited global resources.
func NewFromConfig(config Config) *Workspace {
	return newWorkspace(config.WorkspacePath(), config.MemoryPath(), config.ProjectScoped(), true, config.WorkspaceFallbackPaths()...)
}

func newWorkspace(workspacePath, memoryPath string, projectScoped, protectShared bool, fallbackPaths ...string) *Workspace {
	active := normalizeRoot(workspacePath)
	layers := make([]Layer, 0, len(fallbackPaths)+1)
	seen := make(map[string]int, len(fallbackPaths)+1)
	for i := len(fallbackPaths) - 1; i >= 0; i-- {
		root := normalizeRoot(fallbackPaths[i])
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = len(layers)
		layers = append(layers, Layer{Root: root, Scope: ScopeHome})
	}
	if index, shared := seen[active]; shared {
		if !projectScoped || !protectShared {
			layers[index].Writable = true
		}
	} else if active != "" {
		scope := ScopeHome
		if projectScoped {
			scope = ScopeProject
		}
		layers = append(layers, Layer{Root: active, Scope: scope, Writable: true})
	}
	return &Workspace{
		basePath: active,
		layers:   layers,
		memPath:  normalizeRoot(memoryPath),
		specs: []FileSpec{
			{Name: "AGENTS.md", Role: FileRoleSystemPrompt, Required: true},
			{Name: "SOUL.md", Role: FileRoleSystemPrompt},
			{Name: "IDENTITY.md", Role: FileRoleSystemPrompt},
			{Name: "ENVIRONMENT.md", Role: FileRoleGuidance},
			{Name: "MEMORY.md", Role: FileRoleMemory},
			{Name: "TOOLS.md", Role: FileRoleGuidance},
			{Name: "HEARTBEAT.md", Role: FileRoleOptional},
		},
	}
}

func normalizeRoot(path string) string {
	path = expandHome(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func (w *Workspace) Exists() bool {
	_, err := os.Stat(w.basePath)
	return err == nil
}

func (w *Workspace) InitWithParams(params *TemplateParams) ([]string, error) {
	var created []string
	root, err := w.WritablePath("")
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(w.memPath, 0755); err != nil {
		return nil, err
	}

	for filename, tmpl := range DefaultContents {
		filePath := filepath.Join(root, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			content, renderErr := RenderTemplate(tmpl, params)
			if renderErr != nil {
				return nil, renderErr
			}
			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
				return nil, err
			}
			created = append(created, filename)
		}
	}

	return created, nil
}

func (w *Workspace) BasePath() string {
	return w.basePath
}

func (w *Workspace) MemoryPath() string {
	return w.memPath
}

func (w *Workspace) SkillsPath() string {
	path, _ := w.WritablePath("skills")
	return path
}

// SkillsPaths returns skill directories from lowest to highest priority so it
// can be passed directly to skills.NewLoader (later directories override
// earlier ones).
func (w *Workspace) SkillsPaths() []string {
	roots := w.ResourceRoots("skills")
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.Path)
	}
	return paths
}

// MCPPath returns the writable MCP configuration directory.
func (w *Workspace) MCPPath() string {
	path, _ := w.WritablePath("mcp")
	return path
}

// MCPPaths returns MCP configuration directories from lowest to highest
// priority. A server declared by the active workspace replaces an inherited
// server with the same name.
// Deprecated: use MCPRoots to retain each directory's trust scope.
func (w *Workspace) MCPPaths() []string {
	roots := w.ResourceRoots("mcp")
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.Path)
	}
	return paths
}

// Layers returns workspace roots from lowest to highest priority.
func (w *Workspace) Layers() []Layer {
	return append([]Layer(nil), w.layers...)
}

// ResourceRoots returns resource directories from lowest to highest priority.
func (w *Workspace) ResourceRoots(relativePath string) []ResourceRoot {
	result := make([]ResourceRoot, 0, len(w.layers))
	for _, layer := range w.layers {
		result = append(result, ResourceRoot{
			Path:     w.absolutePathAt(layer.Root, relativePath),
			Scope:    layer.Scope,
			Writable: layer.Writable,
		})
	}
	return result
}

// MCPRoots returns MCP configuration roots with their trust scope intact.
func (w *Workspace) MCPRoots() []ResourceRoot { return w.ResourceRoots("mcp") }

// WritablePath resolves a path in the active writable layer.
func (w *Workspace) WritablePath(relativePath string) (string, error) {
	for i := len(w.layers) - 1; i >= 0; i-- {
		if w.layers[i].Writable {
			return w.absolutePathAt(w.layers[i].Root, relativePath), nil
		}
	}
	return "", fmt.Errorf("%w: project workspace resolves to inherited HOME workspace; set project workspace to \"workspace\"", ErrReadOnlyWorkspace)
}

func (w *Workspace) Ls(dirPath string) ([]string, error) {
	names := make(map[string]struct{})
	found := false
	for _, root := range w.readPaths() {
		entries, err := os.ReadDir(w.absolutePathAt(root, dirPath))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		found = true
		for _, entry := range entries {
			names[entry.Name()] = struct{}{}
		}
	}
	if !found {
		return nil, os.ErrNotExist
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func (w *Workspace) Read(filePath string) (string, error) {
	for _, root := range w.readPaths() {
		data, err := os.ReadFile(w.absolutePathAt(root, filePath))
		if err == nil {
			return string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", os.ErrNotExist
}

func (w *Workspace) Write(filePath string, data string) error {
	absPath, err := w.WritablePath(filePath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(absPath, []byte(data), 0644)
}

func (w *Workspace) Delete(path string) error {
	absPath, err := w.WritablePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(absPath)
}

func (w *Workspace) MkdirAll(dirPath string) error {
	absPath, err := w.WritablePath(dirPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(absPath, 0755)
}

func (w *Workspace) EnsureDir(dirPath string) error {
	absPath, err := w.WritablePath(dirPath)
	if err != nil {
		for _, root := range w.ResourceRoots(dirPath) {
			if info, statErr := os.Stat(root.Path); statErr == nil && info.IsDir() {
				return nil
			}
		}
		return err
	}
	return os.MkdirAll(absPath, 0755)
}

// Deprecated: layered workspaces should be reconstructed from configuration.
func (w *Workspace) SetRoot(root string) {
	for i := len(w.layers) - 1; i >= 0; i-- {
		if w.layers[i].Writable {
			root = normalizeRoot(root)
			w.layers[i].Root = root
			w.basePath = root
			return
		}
	}
}

func (w *Workspace) Root() string {
	return w.basePath
}

func (w *Workspace) absolutePathAt(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func (w *Workspace) readPaths() []string {
	paths := make([]string, 0, len(w.layers))
	for i := len(w.layers) - 1; i >= 0; i-- {
		paths = append(paths, w.layers[i].Root)
	}
	return paths
}
