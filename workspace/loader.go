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

// LoadOption customizes Load behavior.
type LoadOption func(*loadOptions)

type loadOptions struct {
	memoryDays int
}

// WithMemoryDays overrides the number of days of daily memory logs to load.
// Values < 1 fall back to DefaultMemoryDays.
func WithMemoryDays(days int) LoadOption {
	return func(o *loadOptions) {
		o.memoryDays = days
	}
}

// Load reads all workspace files and returns composed content
func (w *Workspace) Load(opts ...LoadOption) (*LoadedContent, error) {
	options := loadOptions{memoryDays: DefaultMemoryDays}
	for _, opt := range opts {
		opt(&options)
	}
	if options.memoryDays < 1 {
		options.memoryDays = DefaultMemoryDays
	}

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

	// Load long-term memory.
	for _, spec := range w.specs {
		if spec.Role != FileRoleMemory {
			continue
		}

		data, err := w.loadFile(spec.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to load memory file %s: %w", spec.Name, err)
		}
		if memoryMessage := formatMemoryMessage("[Long-Term Memory]", data); memoryMessage != "" {
			content.MemoryHistory = append(content.MemoryHistory, types.Message{
				Role:    types.RoleAgent,
				Content: memoryMessage,
			})
		}
	}

	// Load daily memory logs according to the configured retention window
	memLogs := w.loadRecentMemoryLogs(options.memoryDays)
	if len(memLogs) > 0 {
		combinedMemory := strings.Join(memLogs, "\n\n---\n\n")
		content.MemoryHistory = append(content.MemoryHistory, types.Message{
			Role:    types.RoleAgent,
			Content: fmt.Sprintf("[Recent Memory Context]\n\n%s", combinedMemory),
		})
	}

	return content, nil
}

func formatMemoryMessage(header, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "# MEMORY.md" {
		return ""
	}
	return fmt.Sprintf("%s\n\n%s", header, trimmed)
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

// loadRecentMemoryLogs loads memory logs from the last N days,
// skipping files that only contain the empty daily template header.
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
			if trimmed := strings.TrimSpace(string(data)); trimmed == "" || trimmed == "# "+filename {
				continue
			}
			logs = append(logs, string(data))
		}
	}

	return logs
}
