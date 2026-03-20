package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

type Workspace struct {
	basePath string
	memPath  string
	specs    []FileSpec
}

func NewWorkspace(workspacePath, memoryPath string) *Workspace {
	return &Workspace{
		basePath: expandHome(workspacePath),
		memPath:  expandHome(memoryPath),
		specs: []FileSpec{
			{Name: "AGENTS.md", Role: FileRoleSystemPrompt, Required: true},
			{Name: "SOUL.md", Role: FileRoleSystemPrompt},
			{Name: "ENVIRONMENT.md", Role: FileRoleSystemPrompt},
			{Name: "IDENTITY.md", Role: FileRoleSystemPrompt},
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

func (w *Workspace) Ls(dirPath string) ([]string, error) {
	absPath := w.absolutePath(dirPath)

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result, nil
}

func (w *Workspace) Read(filePath string) (string, error) {
	absPath := w.absolutePath(filePath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(w.basePath, path)
}
