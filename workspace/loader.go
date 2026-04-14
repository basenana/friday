package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// Load reads all workspace files and returns composed content
func (w *Workspace) Load() (*LoadedContent, error) {
	content := &LoadedContent{
		SystemPrompts: make([]string, 0),
	}

	for _, spec := range w.specs {
		// Load system prompt files
		if spec.Role == FileRoleSystemPrompt {
			data, err := w.loadFile(spec.Name)
			if err != nil && spec.Required {
				return nil, fmt.Errorf("failed to load required file %s: %w", spec.Name, err)
			}
			if data != "" {
				content.SystemPrompts = append(content.SystemPrompts, data)
			}
		}
	}

	return content, nil
}

// LoadFile reads a single file from the workspace directory (exported)
func (w *Workspace) LoadFile(name string) (string, error) {
	return w.loadFile(name)
}

// loadFile reads a single file from the workspace directory
func (w *Workspace) loadFile(name string) (string, error) {
	filePath := filepath.Join(w.basePath, name)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // File doesn't exist, return empty string
		}
		return "", err
	}
	return string(data), nil
}
