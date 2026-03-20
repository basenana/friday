package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type MemoryFiles struct {
	memoryPath    string
	workspacePath string
}

func NewMemoryFiles(memoryPath, workspacePath string) *MemoryFiles {
	return &MemoryFiles{
		memoryPath:    memoryPath,
		workspacePath: workspacePath,
	}
}

func (f *MemoryFiles) ReadDailyMemory(date time.Time) (string, error) {
	filename := date.Format(time.DateOnly) + ".md"
	path := filepath.Join(f.memoryPath, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (f *MemoryFiles) WriteDailyMemory(date time.Time, content string) error {
	if err := os.MkdirAll(f.memoryPath, 0755); err != nil {
		return err
	}
	filename := date.Format(time.DateOnly) + ".md"
	path := filepath.Join(f.memoryPath, filename)
	return os.WriteFile(path, []byte(content), 0644)
}

func (f *MemoryFiles) ListRecentDailyMemories(days int) ([]string, error) {
	if err := os.MkdirAll(f.memoryPath, 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(f.memoryPath)
	if err != nil {
		return nil, err
	}

	type datedMemory struct {
		date    time.Time
		content string
	}
	var memories []datedMemory
	now := time.Now()

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if entry.Name() == "MEMORY.md" {
			continue
		}

		filename := strings.TrimSuffix(entry.Name(), ".md")
		logDate, err := time.Parse(time.DateOnly, filename)
		if err != nil {
			continue
		}

		daysDiff := int(now.Sub(logDate).Hours() / 24)
		if daysDiff >= 0 && daysDiff < days {
			data, err := os.ReadFile(filepath.Join(f.memoryPath, entry.Name()))
			if err != nil {
				continue
			}
			memories = append(memories, datedMemory{
				date:    logDate,
				content: string(data),
			})
		}
	}

	sort.Slice(memories, func(i, j int) bool {
		return memories[i].date.After(memories[j].date)
	})

	result := make([]string, len(memories))
	for i, m := range memories {
		result[i] = m.content
	}
	return result, nil
}

func (f *MemoryFiles) ReadLongTermMemory() (string, error) {
	path := filepath.Join(f.memoryPath, "MEMORY.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (f *MemoryFiles) WriteLongTermMemory(content string) error {
	if err := os.MkdirAll(f.memoryPath, 0755); err != nil {
		return err
	}
	path := filepath.Join(f.memoryPath, "MEMORY.md")
	return os.WriteFile(path, []byte(content), 0644)
}

func (f *MemoryFiles) AppendLongTermMemory(content string) error {
	if err := os.MkdirAll(f.memoryPath, 0755); err != nil {
		return err
	}
	path := filepath.Join(f.memoryPath, "MEMORY.md")

	entry := fmt.Sprintf("\n## %s\n\n%s\n", time.Now().Format(time.DateOnly), content)
	fs, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer fs.Close()

	_, err = fs.WriteString(entry)
	return err
}

func (f *MemoryFiles) ReadEnvironment() (string, error) {
	path := filepath.Join(f.workspacePath, "ENVIRONMENT.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (f *MemoryFiles) WriteEnvironment(content string) error {
	if err := os.MkdirAll(f.workspacePath, 0755); err != nil {
		return err
	}
	path := filepath.Join(f.workspacePath, "ENVIRONMENT.md")
	return os.WriteFile(path, []byte(content), 0644)
}
