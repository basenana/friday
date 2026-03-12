package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MemoryType string

const (
	MemoryTypeDaily   MemoryType = "daily"
	MemoryTypeCurated MemoryType = "curated"
)

type MemorySystem struct {
	basePath string
	days     int
}

func NewMemorySystem(basePath string, days int) *MemorySystem {
	return &MemorySystem{
		basePath: basePath,
		days:     days,
	}
}

func (m *MemorySystem) EnsureDir() error {
	return os.MkdirAll(m.basePath, 0755)
}

func (m *MemorySystem) todayFilename() string {
	return time.Now().Format(time.DateOnly) + ".md"
}

func (m *MemorySystem) todayPath() string {
	return filepath.Join(m.basePath, m.todayFilename())
}

func (m *MemorySystem) EnsureTodayMemory() error {
	if err := m.EnsureDir(); err != nil {
		return err
	}

	todayPath := m.todayPath()
	if _, err := os.Stat(todayPath); os.IsNotExist(err) {
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		return os.WriteFile(todayPath, []byte(header), 0644)
	}
	return nil
}

func (m *MemorySystem) LoadRecentLogs() ([]string, error) {
	if err := m.EnsureDir(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.basePath)
	if err != nil {
		return nil, err
	}

	var logs []string
	now := time.Now()

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if entry.Name() == "MEMORY.md" {
			continue
		}

		filename := strings.TrimSuffix(entry.Name(), ".md")
		logDate, err := time.Parse("2006-01-02", filename)
		if err != nil {
			continue
		}

		daysDiff := int(now.Sub(logDate).Hours() / 24)
		if daysDiff >= 0 && daysDiff < m.days {
			data, err := os.ReadFile(filepath.Join(m.basePath, entry.Name()))
			if err != nil {
				continue
			}
			logs = append(logs, string(data))
		}
	}

	return logs, nil
}

func (m *MemorySystem) Search(query string) ([]string, error) {
	if err := m.EnsureDir(); err != nil {
		return nil, err
	}

	memoryPath := filepath.Join(m.basePath, "MEMORY.md")
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	content := string(data)
	query = strings.ToLower(query)
	if strings.Contains(strings.ToLower(content), query) {
		return []string{content}, nil
	}

	return []string{}, nil
}

func (m *MemorySystem) Write(content string, memType MemoryType) error {
	if err := m.EnsureDir(); err != nil {
		return err
	}

	if memType == MemoryTypeDaily {
		if err := m.EnsureTodayMemory(); err != nil {
			return err
		}

		entry := fmt.Sprintf("## %s\n\n%s\n\n", time.Now().Format(time.RFC3339), content)
		fs, err := os.OpenFile(m.todayPath(), os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer fs.Close()

		_, err = fs.WriteString(entry)
		return err
	}

	memoryPath := filepath.Join(m.basePath, "MEMORY.md")
	entry := fmt.Sprintf("\n## %s\n\n%s\n", time.Now().Format("2006-01-02"), content)

	fs, err := os.OpenFile(memoryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer fs.Close()

	_, err = fs.WriteString(entry)
	return err
}
