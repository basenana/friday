package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Workspace struct {
	basePath      string
	fallbackPaths []string
	memPath       string
	specs         []FileSpec
}

// NewWorkspace creates a writable workspace with optional lower-priority read
// fallbacks. Reads check workspacePath first and then each fallback in order;
// writes and deletes always target workspacePath.
func NewWorkspace(workspacePath, memoryPath string, fallbackPaths ...string) *Workspace {
	fallbacks := make([]string, 0, len(fallbackPaths))
	for _, path := range fallbackPaths {
		path = expandHome(path)
		if path != "" && filepath.Clean(path) != filepath.Clean(workspacePath) {
			fallbacks = append(fallbacks, path)
		}
	}
	return &Workspace{
		basePath:      expandHome(workspacePath),
		fallbackPaths: fallbacks,
		memPath:       expandHome(memoryPath),
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

	if err := os.MkdirAll(w.basePath, 0755); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(w.memPath, 0755); err != nil {
		return nil, err
	}

	for filename, tmpl := range DefaultContents {
		filePath := filepath.Join(w.basePath, filename)
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
	return filepath.Join(w.basePath, "skills")
}

// SkillsPaths returns skill directories from lowest to highest priority so it
// can be passed directly to skills.NewLoader (later directories override
// earlier ones).
func (w *Workspace) SkillsPaths() []string {
	paths := make([]string, 0, len(w.fallbackPaths)+1)
	for i := len(w.fallbackPaths) - 1; i >= 0; i-- {
		paths = append(paths, filepath.Join(w.fallbackPaths[i], "skills"))
	}
	paths = append(paths, w.SkillsPath())
	return paths
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
	absPath := w.absolutePath(filePath)

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(absPath, []byte(data), 0644)
}

func (w *Workspace) Delete(path string) error {
	absPath := w.absolutePath(path)
	return os.RemoveAll(absPath)
}

func (w *Workspace) MkdirAll(dirPath string) error {
	absPath := w.absolutePath(dirPath)
	return os.MkdirAll(absPath, 0755)
}

func (w *Workspace) EnsureDir(dirPath string) error {
	absPath := w.absolutePath(dirPath)
	return os.MkdirAll(absPath, 0755)
}

func (w *Workspace) SetRoot(root string) {
	w.basePath = expandHome(root)
}

func (w *Workspace) Root() string {
	return w.basePath
}

func (w *Workspace) absolutePath(path string) string {
	return w.absolutePathAt(w.basePath, path)
}

func (w *Workspace) absolutePathAt(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func (w *Workspace) readPaths() []string {
	paths := make([]string, 0, len(w.fallbackPaths)+1)
	paths = append(paths, w.basePath)
	paths = append(paths, w.fallbackPaths...)
	return paths
}
