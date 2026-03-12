package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basenana/friday/core/types"
)

const (
	// DefaultMemoryDays is the default number of days of memory to load
	DefaultMemoryDays = 2 // today + yesterday
)

// Load reads all workspace files and returns composed content
func (w *Workspace) Load() (*LoadedContent, error) {
	content := &LoadedContent{
		SystemPrompts: make([]string, 0),
		MemoryHistory: make([]types.Message, 0),
	}

	// Load system prompt files
	for _, spec := range w.specs {
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

	// Load daily memory (today + yesterday)
	memLogs := w.loadRecentMemoryLogs(DefaultMemoryDays)
	if len(memLogs) > 0 {
		combinedMemory := strings.Join(memLogs, "\n\n---\n\n")
		content.MemoryHistory = append(content.MemoryHistory, types.Message{
			Role:    types.RoleUser,
			Content: fmt.Sprintf("[Memory Context]\n\n%s", combinedMemory),
		})
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

// loadRecentMemoryLogs loads memory logs from the last N days
func (w *Workspace) loadRecentMemoryLogs(days int) []string {
	// Ensure memory directory exists
	if _, err := os.Stat(w.memPath); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(w.memPath)
	if err != nil {
		return nil
	}

	var logs []string
	now := time.Now()

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filename := strings.TrimSuffix(entry.Name(), ".md")
		logDate, err := time.Parse("2006-01-02", filename)
		if err != nil {
			continue
		}

		daysDiff := int(now.Sub(logDate).Hours() / 24)
		if daysDiff >= 0 && daysDiff < days {
			data, err := os.ReadFile(filepath.Join(w.memPath, entry.Name()))
			if err != nil {
				continue
			}
			logs = append(logs, string(data))
		}
	}

	return logs
}

// ComposeSystemPrompt combines the default prompt with workspace content
func ComposeSystemPrompt(content *LoadedContent, defaultPrompt string) string {
	if content == nil || len(content.SystemPrompts) == 0 {
		return defaultPrompt
	}

	var parts []string

	// Start with default prompt
	if defaultPrompt != "" {
		parts = append(parts, defaultPrompt)
	}

	// Add custom sections
	for _, prompt := range content.SystemPrompts {
		prompt = strings.TrimSpace(prompt)
		if prompt != "" {
			parts = append(parts, prompt)
		}
	}

	return strings.Join(parts, "\n\n")
}
