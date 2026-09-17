package agents

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"gopkg.in/yaml.v3"
)

const SpecFilename = "AGENT-SPEC.md"

// Loader reads agent definitions from low-to-high-priority directories.
// Later definitions replace earlier definitions with the same normalized name.
type Loader struct {
	paths []string
}

func NewLoader(paths ...string) *Loader {
	return &Loader{paths: append([]string(nil), paths...)}
}

func (l *Loader) Load() (*Registry, error) {
	reg := NewRegistry()
	for _, root := range l.paths {
		if err := l.loadRoot(root, reg); err != nil {
			return nil, err
		}
	}
	reg.paths = append([]string(nil), l.paths...)
	reg.stamp, _ = agentFilesStamp(l.paths)
	return reg, nil
}

func (l *Loader) loadRoot(root string, reg *Registry) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read agents directory %s: %w", root, err)
	}

	// ReadDir is sorted, but sort explicitly so alternative filesystems and
	// test providers cannot change prompt/tool ordering.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	seen := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(root, entry.Name(), SpecFilename)
		spec, err := loadSpecFile(path, entry.Name())
		if err != nil {
			return fmt.Errorf("load agent %s: %w", entry.Name(), err)
		}
		if previous, exists := seen[spec.Name]; exists {
			return fmt.Errorf("duplicate agent name %q in %s and %s", spec.Name, previous, path)
		}
		seen[spec.Name] = path
		reg.Register(spec)
	}
	return nil
}

func loadSpecFile(path, dirName string) (*AgentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	frontmatter, body, err := parseSpecFile(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	directoryName, err := normalizeName(dirName)
	if err != nil || directoryName != dirName {
		return nil, fmt.Errorf("invalid agent directory name %q", dirName)
	}
	name := directoryName
	if raw, ok := frontmatter["name"]; ok {
		if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
			name, err = normalizeName(value)
			if err != nil {
				return nil, err
			}
		} else {
			warnIgnoredField(path, "name", "expected a non-empty string; using directory name")
		}
	}
	if name != directoryName {
		return nil, fmt.Errorf("agent name %q must match directory %q", name, dirName)
	}

	description := name
	if raw, ok := frontmatter["description"]; ok {
		if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
			description = strings.TrimSpace(value)
		} else {
			warnIgnoredField(path, "description", "expected a non-empty string; using agent name")
		}
	}

	maxLoopTimes := 100
	if raw, ok := frontmatter["max_loop_times"]; ok {
		if value, ok := positiveInt(raw); ok {
			maxLoopTimes = value
		} else {
			warnIgnoredField(path, "max_loop_times", "expected a positive integer; using 100")
		}
	}

	model := ""
	if raw, ok := frontmatter["model"]; ok {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("model must be a non-empty string")
		}
		model = strings.TrimSpace(value)
	}

	effort := ""
	if raw, ok := frontmatter["effort"]; ok {
		value, ok := raw.(string)
		if !ok || !providers.IsValidReasoningEffort(strings.ToLower(strings.TrimSpace(value))) {
			return nil, fmt.Errorf("effort must be one of default, none, low, medium, high, xhigh, max")
		}
		effort = strings.ToLower(strings.TrimSpace(value))
	}

	for key := range frontmatter {
		switch key {
		case "name", "description", "model", "effort", "max_loop_times":
		default:
			warnIgnoredField(path, key, "unknown field ignored for forward compatibility")
		}
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("system prompt body is required")
	}
	return &AgentSpec{
		Name:         name,
		Description:  description,
		Model:        model,
		Effort:       effort,
		SystemPrompt: body,
		MaxLoopTimes: maxLoopTimes,
		Mode:         ModeSubagent,
		SourcePath:   path,
	}, nil
}

func parseSpecFile(content []byte) (map[string]any, string, error) {
	s := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return map[string]any{}, strings.TrimSpace(s), nil
	}
	lines := strings.Split(s, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, "", fmt.Errorf("frontmatter is not closed")
	}
	frontmatter := make(map[string]any)
	rawFrontmatter := strings.Join(lines[1:end], "\n")
	if strings.TrimSpace(rawFrontmatter) != "" {
		if err := yaml.NewDecoder(bytes.NewBufferString(rawFrontmatter)).Decode(&frontmatter); err != nil {
			return nil, "", err
		}
	}
	return frontmatter, strings.TrimSpace(strings.Join(lines[end+1:], "\n")), nil
}

func normalizeName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\ \t\r\n") {
		return "", fmt.Errorf("invalid agent name %q", name)
	}
	for i, r := range name {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (i > 0 && (r == '-' || r == '_'))
		if !valid {
			return "", fmt.Errorf("invalid agent name %q", name)
		}
	}
	return name, nil
}

func positiveInt(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, value > 0
	case int64:
		return int(value), value > 0
	case uint:
		return int(value), value > 0 && uint(int(value)) == value
	case uint64:
		return int(value), value > 0 && uint64(int(value)) == value
	case float64:
		return int(value), value > 0 && value == float64(int(value))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		return parsed, err == nil && parsed > 0
	default:
		return 0, false
	}
}

func warnIgnoredField(path, field, reason string) {
	logger.New("agents").Warnw("ignored agent frontmatter field", "path", path, "field", field, "reason", reason)
}
