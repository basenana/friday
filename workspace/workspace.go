package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// Workspace manages workspace files and memory integration.
// It also implements core/fs.FileSystem interface for use as session workdir.
type Workspace struct {
	basePath string // Path to workspace directory
	memPath  string // Path to memory directory (shared with memory package)
	specs    []FileSpec
}

// NewWorkspace creates a new Workspace instance
func NewWorkspace(workspacePath, memoryPath string) *Workspace {
	return &Workspace{
		basePath: expandHome(workspacePath),
		memPath:  expandHome(memoryPath),
		specs: []FileSpec{
			{Name: "AGENTS.md", Role: FileRoleSystemPrompt, Required: true},
			{Name: "SOUL.md", Role: FileRoleSystemPrompt},
			{Name: "USER.md", Role: FileRoleSystemPrompt},
			{Name: "IDENTITY.md", Role: FileRoleSystemPrompt},
			{Name: "MEMORY.md", Role: FileRoleMemory},
			{Name: "TOOLS.md", Role: FileRoleGuidance},
			{Name: "HEARTBEAT.md", Role: FileRoleOptional},
		},
	}
}

// expandHome expands ~ to home directory
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

// Exists checks if the workspace directory exists
func (w *Workspace) Exists() bool {
	_, err := os.Stat(w.basePath)
	return err == nil
}

// Init creates the workspace directory structure with default files.
// It creates missing directories and files without overwriting existing ones.
func (w *Workspace) Init() ([]string, error) {
	var created []string

	// Create workspace directory
	if err := os.MkdirAll(w.basePath, 0755); err != nil {
		return nil, err
	}

	// Create memory directory if it doesn't exist
	if err := os.MkdirAll(w.memPath, 0755); err != nil {
		return nil, err
	}

	// Create default files if they don't exist
	for filename, content := range DefaultContents {
		filePath := filepath.Join(w.basePath, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
				return nil, err
			}
			created = append(created, filename)
		}
	}

	return created, nil
}

// BasePath returns the workspace base path
func (w *Workspace) BasePath() string {
	return w.basePath
}

// MemoryPath returns the memory path
func (w *Workspace) MemoryPath() string {
	return w.memPath
}

// SkillsPath returns the skills directory path
func (w *Workspace) SkillsPath() string {
	return filepath.Join(w.basePath, "skills")
}

// ========================================
// core/fs.FileSystem interface implementation
// ========================================

// Ls lists files in a directory relative to workspace root
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

// MkdirAll creates a directory and all parent directories
func (w *Workspace) MkdirAll(dirPath string) error {
	absPath := w.absolutePath(dirPath)
	return os.MkdirAll(absPath, 0755)
}

// Read reads a file's content
func (w *Workspace) Read(filePath string) (string, error) {
	absPath := w.absolutePath(filePath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Write writes content to a file, creating parent directories if needed
func (w *Workspace) Write(filePath string, data string) error {
	absPath := w.absolutePath(filePath)

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(absPath, []byte(data), 0644)
}

// Delete removes a file or directory
func (w *Workspace) Delete(path string) error {
	absPath := w.absolutePath(path)
	return os.RemoveAll(absPath)
}

// absolutePath returns the absolute path within workspace
func (w *Workspace) absolutePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(w.basePath, path)
}

// EnsureDir ensures a directory exists
func (w *Workspace) EnsureDir(dirPath string) error {
	return w.MkdirAll(dirPath)
}

// SetRoot sets the workspace root path
func (w *Workspace) SetRoot(root string) {
	w.basePath = expandHome(root)
}

// Root returns the workspace root path
func (w *Workspace) Root() string {
	return w.basePath
}
