package configtools

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/core/providers"
	fridaymcp "github.com/basenana/friday/mcp"
	"github.com/basenana/friday/workspace"
)

type Store interface {
	ListAgents() ([]*coderagents.AgentSpec, error)
	GetAgent(string) (*coderagents.AgentSpec, error)
	CreateAgent(AgentInput) (*coderagents.AgentSpec, error)
	UpdateAgent(string, map[string]any) (*coderagents.AgentSpec, error)
	DeleteAgent(string) error
	ListMCP() ([]fridaymcp.ServerConfig, error)
	GetMCP(string) (fridaymcp.ServerConfig, error)
	CreateMCP(string, map[string]any) (fridaymcp.ServerConfig, error)
	UpdateMCP(string, map[string]any) (fridaymcp.ServerConfig, error)
	DeleteMCP(string) error
}

type AgentInput struct {
	Name         string
	Description  string
	Instructions string
	Model        string
	Effort       string
	MaxLoopTimes int
}

type FileStore struct {
	agentPaths []string
	mcpRoots   []workspace.ResourceRoot
	hasModel   func(string) bool
}

func NewFileStore(agentPaths []string, mcpRoots []workspace.ResourceRoot, hasModel func(string) bool) *FileStore {
	return &FileStore{
		agentPaths: append([]string(nil), agentPaths...),
		mcpRoots:   append([]workspace.ResourceRoot(nil), mcpRoots...),
		hasModel:   hasModel,
	}
}

func (s *FileStore) ListAgents() ([]*coderagents.AgentSpec, error) {
	registry, err := coderagents.NewLoader(s.agentPaths...).Load()
	if err != nil {
		return nil, err
	}
	return registry.List(), nil
}

func (s *FileStore) GetAgent(name string) (*coderagents.AgentSpec, error) {
	registry, err := coderagents.NewLoader(s.agentPaths...).Load()
	if err != nil {
		return nil, err
	}
	spec, ok := registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", name)
	}
	return spec, nil
}

func (s *FileStore) CreateAgent(input AgentInput) (*coderagents.AgentSpec, error) {
	if err := validateAgentInput(input, true, s.hasModel); err != nil {
		return nil, err
	}
	registry, err := coderagents.NewLoader(s.agentPaths...).Load()
	if err != nil {
		return nil, err
	}
	if _, exists := registry.Get(input.Name); exists {
		return nil, fmt.Errorf("agent already exists: %s", input.Name)
	}
	root, err := s.writableAgentRoot()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, input.Name, coderagents.SpecFilename)
	if err := rejectSymlinkPath(root, filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := writeAgentSpec(path, input); err != nil {
		return nil, err
	}
	return s.GetAgent(input.Name)
}

func (s *FileStore) UpdateAgent(name string, fields map[string]any) (*coderagents.AgentSpec, error) {
	current, err := s.GetAgent(name)
	if err != nil {
		return nil, err
	}
	input := AgentInput{Name: current.Name, Description: current.Description, Instructions: current.SystemPrompt, Model: current.Model, Effort: current.Effort, MaxLoopTimes: current.MaxLoopTimes}
	applyAgentFields(&input, fields)
	if err := validateAgentInput(input, true, s.hasModel); err != nil {
		return nil, err
	}
	root, err := s.writableAgentRoot()
	if err != nil {
		return nil, err
	}
	path := current.SourcePath
	if !pathWithin(root, path) {
		path = filepath.Join(root, input.Name, coderagents.SpecFilename)
	}
	if err := rejectSymlinkPath(root, filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := writeAgentSpec(path, input); err != nil {
		return nil, err
	}
	return s.GetAgent(name)
}

func (s *FileStore) DeleteAgent(name string) error {
	current, err := s.GetAgent(name)
	if err != nil {
		return err
	}
	root, err := s.writableAgentRoot()
	if err != nil {
		return err
	}
	if !pathWithin(root, current.SourcePath) {
		return fmt.Errorf("cannot delete inherited agent %q outside writable layer", name)
	}
	dir := filepath.Dir(current.SourcePath)
	if filepath.Dir(dir) != filepath.Clean(root) {
		return fmt.Errorf("refusing to delete unexpected agent path %s", dir)
	}
	return os.RemoveAll(dir)
}

func (s *FileStore) writableAgentRoot() (string, error) {
	if len(s.agentPaths) == 0 {
		return "", errors.New("no writable agent directory configured")
	}
	root := filepath.Clean(s.agentPaths[len(s.agentPaths)-1])
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

func validateAgentInput(input AgentInput, requireInstructions bool, hasModel func(string) bool) error {
	if !validName(input.Name) {
		return fmt.Errorf("invalid agent name %q", input.Name)
	}
	if requireInstructions && strings.TrimSpace(input.Instructions) == "" {
		return errors.New("agent instructions are required")
	}
	if input.Model != "" && hasModel != nil && !hasModel(input.Model) {
		return fmt.Errorf("unknown model %q", input.Model)
	}
	if input.Effort != "" && !providers.IsValidReasoningEffort(input.Effort) {
		return fmt.Errorf("invalid reasoning effort %q", input.Effort)
	}
	if input.MaxLoopTimes < 0 {
		return errors.New("max_loop_times must be positive")
	}
	return nil
}

func applyAgentFields(input *AgentInput, fields map[string]any) {
	if value, ok := stringField(fields, "description"); ok {
		input.Description = value
	}
	if value, ok := stringField(fields, "instructions"); ok {
		input.Instructions = value
	}
	if value, ok := stringField(fields, "model"); ok {
		input.Model = value
	}
	if value, ok := stringField(fields, "effort"); ok {
		input.Effort = strings.ToLower(value)
	}
	if raw, ok := fields["max_loop_times"]; ok {
		switch value := raw.(type) {
		case int:
			input.MaxLoopTimes = value
		case float64:
			if value == float64(int(value)) {
				input.MaxLoopTimes = int(value)
			} else {
				input.MaxLoopTimes = -1
			}
		}
	}
}

func writeAgentSpec(path string, input AgentInput) error {
	if input.Description == "" {
		input.Description = input.Name
	}
	if input.MaxLoopTimes == 0 {
		input.MaxLoopTimes = 100
	}
	var b strings.Builder
	b.WriteString("---\nname: " + yamlScalar(input.Name) + "\n")
	b.WriteString("description: " + yamlScalar(input.Description) + "\n")
	if input.Model != "" {
		b.WriteString("model: " + yamlScalar(input.Model) + "\n")
	}
	if input.Effort != "" {
		b.WriteString("effort: " + yamlScalar(input.Effort) + "\n")
	}
	b.WriteString("max_loop_times: " + strconv.Itoa(input.MaxLoopTimes) + "\n---\n")
	b.WriteString(strings.TrimSpace(input.Instructions) + "\n")
	return atomicWrite(path, []byte(b.String()), 0o600)
}

func yamlScalar(value string) string {
	// JSON quoted strings are valid YAML scalars and keep multiline/colon/hash
	// values on one deterministic line.
	return strconv.Quote(value)
}

type mcpDocument struct {
	MCPServers map[string]fridaymcp.ServerConfig `json:"mcpServers,omitempty"`
	Servers    map[string]fridaymcp.ServerConfig `json:"servers,omitempty"`
}

func (s *FileStore) ListMCP() ([]fridaymcp.ServerConfig, error) {
	configs, err := fridaymcp.LoadConfigRoots(s.mcpRoots)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]fridaymcp.ServerConfig, 0, len(names))
	for _, name := range names {
		out = append(out, configs[name])
	}
	return out, nil
}

func (s *FileStore) GetMCP(name string) (fridaymcp.ServerConfig, error) {
	configs, err := fridaymcp.LoadConfigRoots(s.mcpRoots)
	if err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	config, ok := configs[name]
	if !ok {
		return fridaymcp.ServerConfig{}, fmt.Errorf("MCP server not found: %s", name)
	}
	return config, nil
}

func (s *FileStore) CreateMCP(name string, fields map[string]any) (fridaymcp.ServerConfig, error) {
	if !validName(name) {
		return fridaymcp.ServerConfig{}, fmt.Errorf("invalid MCP server name %q", name)
	}
	configs, err := fridaymcp.LoadConfigRoots(s.mcpRoots)
	if err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	if _, exists := configs[name]; exists {
		return fridaymcp.ServerConfig{}, fmt.Errorf("MCP server already exists: %s", name)
	}
	root, err := s.writableMCPRoot()
	if err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	path := filepath.Join(root.Path, name+".json")
	config := fridaymcp.ServerConfig{Name: name, Source: path, Project: root.Scope == workspace.ScopeProject}
	applyMCPFields(&config, fields)
	if err := config.Normalize(); err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	doc := mcpDocument{MCPServers: map[string]fridaymcp.ServerConfig{name: config}}
	if err := writeMCPDocument(path, doc); err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	return s.GetMCP(name)
}

func (s *FileStore) UpdateMCP(name string, fields map[string]any) (fridaymcp.ServerConfig, error) {
	current, err := s.GetMCP(name)
	if err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	root, err := s.writableMCPRoot()
	if err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	config := current
	applyMCPFields(&config, fields)
	path := current.Source
	if !pathWithin(root.Path, path) {
		path = filepath.Join(root.Path, name+".json")
		config.Source = path
		config.Project = root.Scope == workspace.ScopeProject
	}
	if err := config.Normalize(); err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	doc, err := readMCPDocument(path)
	if os.IsNotExist(err) {
		doc = mcpDocument{MCPServers: make(map[string]fridaymcp.ServerConfig)}
	} else if err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	if doc.MCPServers != nil {
		doc.MCPServers[name] = config
	} else {
		if doc.Servers == nil {
			doc.Servers = make(map[string]fridaymcp.ServerConfig)
		}
		doc.Servers[name] = config
	}
	if err := writeMCPDocument(path, doc); err != nil {
		return fridaymcp.ServerConfig{}, err
	}
	return s.GetMCP(name)
}

func (s *FileStore) DeleteMCP(name string) error {
	current, err := s.GetMCP(name)
	if err != nil {
		return err
	}
	root, err := s.writableMCPRoot()
	if err != nil {
		return err
	}
	if !pathWithin(root.Path, current.Source) {
		return fmt.Errorf("cannot delete inherited MCP server %q outside writable layer", name)
	}
	doc, err := readMCPDocument(current.Source)
	if err != nil {
		return err
	}
	delete(doc.MCPServers, name)
	delete(doc.Servers, name)
	if len(doc.MCPServers) == 0 && len(doc.Servers) == 0 {
		return os.Remove(current.Source)
	}
	return writeMCPDocument(current.Source, doc)
}

func (s *FileStore) writableMCPRoot() (workspace.ResourceRoot, error) {
	for i := len(s.mcpRoots) - 1; i >= 0; i-- {
		if !s.mcpRoots[i].Writable {
			continue
		}
		root := s.mcpRoots[i]
		if err := os.MkdirAll(root.Path, 0o755); err != nil {
			return workspace.ResourceRoot{}, err
		}
		return root, nil
	}
	return workspace.ResourceRoot{}, errors.New("no writable MCP directory configured")
}

func applyMCPFields(config *fridaymcp.ServerConfig, fields map[string]any) {
	data, _ := json.Marshal(fields)
	var patch fridaymcp.ServerConfig
	_ = json.Unmarshal(data, &patch)
	set := func(key string, apply func()) {
		if _, ok := fields[key]; ok {
			apply()
		}
	}
	set("type", func() { config.Type = patch.Type })
	set("command", func() { config.Command = patch.Command })
	set("args", func() { config.Args = patch.Args })
	set("env", func() { config.Env = patch.Env })
	set("cwd", func() { config.Cwd = patch.Cwd })
	set("url", func() { config.URL = patch.URL })
	set("headers", func() { config.Headers = patch.Headers })
	set("disabled", func() { config.Disabled = patch.Disabled })
	set("includeTools", func() { config.IncludeTools = patch.IncludeTools })
	set("excludeTools", func() { config.ExcludeTools = patch.ExcludeTools })
}

func readMCPDocument(path string) (mcpDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mcpDocument{}, err
	}
	var doc mcpDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return mcpDocument{}, err
	}
	return doc, nil
}

func writeMCPDocument(path string, doc mcpDocument) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".friday-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	ok = true
	return nil
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for i, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && (r == '-' || r == '_')) {
			return false
		}
	}
	return true
}

func pathWithin(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func rejectSymlinkPath(root, targetDir string) error {
	root, targetDir = filepath.Clean(root), filepath.Clean(targetDir)
	if !pathWithin(root, targetDir) {
		return fmt.Errorf("configuration path escapes writable root: %s", targetDir)
	}
	rel, err := filepath.Rel(root, targetDir)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing configuration path through symlink: %s", current)
		}
	}
	return nil
}

func stringField(values map[string]any, key string) (string, bool) {
	raw, ok := values[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	return strings.TrimSpace(value), ok
}

func RedactedMCP(config fridaymcp.ServerConfig) map[string]any {
	data, _ := json.Marshal(config)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	for _, key := range []string{"env", "headers"} {
		if values, ok := result[key].(map[string]any); ok {
			for name := range values {
				values[name] = "[redacted]"
			}
		}
	}
	if raw, ok := result["url"].(string); ok {
		if parsed, err := url.Parse(raw); err == nil {
			parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
			result["url"] = parsed.String()
		}
	}
	result["name"] = config.Name
	result["source"] = config.Source
	result["project"] = config.Project
	return result
}
