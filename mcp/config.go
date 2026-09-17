package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/basenana/friday/workspace"
)

type TransportType string

const (
	TransportStdio          TransportType = "stdio"
	TransportStreamableHTTP TransportType = "streamable-http"
	TransportSSE            TransportType = "sse"
)

// ServerConfig accepts the de-facto configuration shape used by Claude,
// VS Code, Cursor and other MCP clients.
type ServerConfig struct {
	Type         TransportType     `json:"type,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	URL          string            `json:"url,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Disabled     bool              `json:"disabled,omitempty"`
	IncludeTools []string          `json:"includeTools,omitempty"`
	ExcludeTools []string          `json:"excludeTools,omitempty"`

	Source  string `json:"-"`
	Project bool   `json:"-"`
	Digest  string `json:"-"`
	Name    string `json:"-"`
}

type configDocument struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
	Servers    map[string]ServerConfig `json:"servers"`
}

// LoadConfigRoots loads JSON files from low to high priority. Later files and
// directories replace servers with the same name. Each server retains the
// scope of the workspace layer that supplied its effective definition.
func LoadConfigRoots(roots []workspace.ResourceRoot) (map[string]ServerConfig, error) {
	result := make(map[string]ServerConfig)
	for _, root := range roots {
		dir := root.Path
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read MCP config directory %s: %w", dir, err)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, filename := range names {
			path := filepath.Join(dir, filename)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read MCP config %s: %w", path, err)
			}
			var doc configDocument
			if err := json.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("decode MCP config %s: %w", path, err)
			}
			if doc.MCPServers == nil && doc.Servers == nil {
				return nil, fmt.Errorf("MCP config %s must contain mcpServers or servers", path)
			}
			merged := make(map[string]ServerConfig, len(doc.MCPServers)+len(doc.Servers))
			for name, config := range doc.Servers {
				merged[name] = config
			}
			for name, config := range doc.MCPServers {
				merged[name] = config
			}
			serverNames := make([]string, 0, len(merged))
			for name := range merged {
				serverNames = append(serverNames, name)
			}
			sort.Strings(serverNames)
			for _, name := range serverNames {
				config := merged[name]
				config.Name = name
				config.Source = path
				config.Project = root.Scope == workspace.ScopeProject
				if err := config.normalize(); err != nil {
					return nil, fmt.Errorf("MCP server %q in %s: %w", name, path, err)
				}
				result[name] = config
			}
		}
	}
	return result, nil
}

// ConfigFilesStamp returns a stable signature of the JSON configuration files
// in roots. It intentionally uses metadata rather than file contents so callers
// can cheaply decide whether a full parse is necessary.
func ConfigFilesStamp(roots []workspace.ResourceRoot) (string, error) {
	records := make([]string, 0)
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read MCP config directory %s: %w", root.Path, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				continue
			}
			path := filepath.Join(root.Path, entry.Name())
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("stat MCP config %s: %w", path, err)
			}
			records = append(records, fmt.Sprintf("%s\x00%d\x00%d", filepath.Clean(path), info.ModTime().UnixNano(), info.Size()))
		}
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func (c *ServerConfig) normalize() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Command = strings.TrimSpace(c.Command)
	c.URL = strings.TrimSpace(c.URL)
	typ := strings.ToLower(strings.TrimSpace(string(c.Type)))
	switch typ {
	case "", "http", "streamablehttp", "streamable_http", "streamable-http":
		if typ == "" && c.Command != "" {
			c.Type = TransportStdio
		} else {
			c.Type = TransportStreamableHTTP
		}
	case "stdio", "cmd", "command":
		c.Type = TransportStdio
	case "sse":
		c.Type = TransportSSE
	default:
		return fmt.Errorf("unsupported transport type %q", typ)
	}
	if c.Type == TransportStdio && c.Command == "" {
		return fmt.Errorf("stdio transport requires command")
	}
	if c.Type != TransportStdio && c.URL == "" {
		return fmt.Errorf("%s transport requires url", c.Type)
	}
	if c.Cwd != "" && !filepath.IsAbs(c.Cwd) && !strings.HasPrefix(c.Cwd, "$") {
		c.Cwd = filepath.Clean(filepath.Join(filepath.Dir(c.Source), c.Cwd))
	}
	canonical := struct {
		Type         TransportType     `json:"type"`
		Command      string            `json:"command,omitempty"`
		Args         []string          `json:"args,omitempty"`
		Env          map[string]string `json:"env,omitempty"`
		Cwd          string            `json:"cwd,omitempty"`
		URL          string            `json:"url,omitempty"`
		Headers      map[string]string `json:"headers,omitempty"`
		Disabled     bool              `json:"disabled,omitempty"`
		IncludeTools []string          `json:"includeTools,omitempty"`
		ExcludeTools []string          `json:"excludeTools,omitempty"`
	}{c.Type, c.Command, c.Args, c.Env, c.Cwd, c.URL, c.Headers, c.Disabled, c.IncludeTools, c.ExcludeTools}
	data, _ := json.Marshal(canonical)
	digest := sha256.Sum256(data)
	c.Digest = hex.EncodeToString(digest[:])
	return nil
}

// Normalize validates a programmatically constructed server definition and
// computes its canonical digest. Name, Source, and Project should be populated
// by the caller before invoking it.
func (c *ServerConfig) Normalize() error { return c.normalize() }

func expand(value string) string { return os.ExpandEnv(value) }

func (c ServerConfig) expandedEnv() []string {
	keys := make([]string, 0, len(c.Env))
	for key := range c.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+expand(c.Env[key]))
	}
	return result
}

func (c ServerConfig) allowedTool(name string) bool {
	included := len(c.IncludeTools) == 0
	for _, candidate := range c.IncludeTools {
		if candidate == name {
			included = true
			break
		}
	}
	for _, candidate := range c.ExcludeTools {
		if candidate == name {
			return false
		}
	}
	return included
}
