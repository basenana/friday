package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/sandbox"
	"gopkg.in/yaml.v3"
)

func Load(configPath string) (*Config, error) {
	if configPath == "" {
		homeFriday, err := homeFridayDir()
		if err != nil {
			return nil, err
		}
		configPath, err = findConfig(homeFriday)
		if err != nil {
			return nil, err
		}
		if configPath == "" {
			cfg := DefaultConfig()
			cfg.applyRuntimeDefaults()
			return cfg, nil
		}
	} else {
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return nil, fmt.Errorf("resolve config path: %w", err)
		}
		configPath = absPath
	}
	return loadFile(configPath, true, false)
}

// LoadForDir discovers and loads the effective CLI configuration. An explicit
// path wins, followed by a config in cwd/.friday, then the HOME config. Only
// the current directory is inspected; parent directories are not searched.
// When a project config is active, its workspace falls back to the HOME
// workspace on a per-file basis.
func LoadForDir(explicitPath, cwd string) (*Config, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}

	homeFriday, err := homeFridayDir()
	if err != nil {
		return nil, err
	}
	homeConfigPath, err := findConfig(homeFriday)
	if err != nil {
		return nil, err
	}

	configPath := explicitPath
	if configPath != "" {
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return nil, fmt.Errorf("resolve config path: %w", err)
		}
		if _, statErr := os.Stat(configPath); statErr != nil {
			return nil, statErr
		}
	} else {
		configPath, err = findConfig(filepath.Join(absCWD, ".friday"))
		if err != nil {
			return nil, err
		}
		if configPath == "" {
			configPath = homeConfigPath
		}
	}

	activeDir := homeFriday
	if configPath != "" {
		activeDir = filepath.Clean(filepath.Dir(configPath))
	}
	projectScoped := activeDir != filepath.Clean(homeFriday)

	var cfg *Config
	if configPath == "" {
		cfg = DefaultConfig()
		cfg.applyRuntimeDefaults()
	} else {
		cfg, err = loadFile(configPath, false, projectScoped)
		if err != nil {
			return nil, err
		}
	}

	if err := applyProjectSandboxAllow(cfg, absCWD); err != nil {
		return nil, err
	}

	homeFriday = filepath.Clean(homeFriday)
	if activeDir == homeFriday {
		return cfg, nil
	}

	homeCfg := DefaultConfig()
	if homeConfigPath != "" {
		homeCfg, err = loadFile(homeConfigPath, false, false)
		if err != nil {
			return nil, fmt.Errorf("load HOME config for workspace fallback: %w", err)
		}
	}
	cfg.projectScope = true
	cfg.workspaceFallbacks = []string{homeCfg.WorkspacePath()}
	cfg.agentPaths = append(homeCfg.AgentPaths(), filepath.Join(activeDir, "agents"))
	return cfg, nil
}

// applyProjectSandboxAllow merges the per-project sandbox command grants from
// <DataDir>/projects/<ProjectID(cwd)>/sandbox.json into the sandbox allow
// list. The file lives on the HOME side (outside the agent's default
// filesystem write roots) so a sandboxed agent cannot edit it to escalate its
// own permissions. Deny rules are never affected; a structurally invalid
// file fails loudly instead of being ignored.
func applyProjectSandboxAllow(cfg *Config, cwd string) error {
	if cfg == nil || cfg.Sandbox == nil || cfg.Sandbox.IsolationDisabled() {
		return nil
	}
	allowPath, err := sandbox.ProjectAllowPath(cfg.DataDirPath(), cwd)
	if err != nil {
		// No usable project root (for example a vanished cwd); skip silently.
		return nil
	}
	allow, err := sandbox.LoadProjectAllow(allowPath)
	if err != nil {
		return fmt.Errorf("load project sandbox allowlist %s: %w", allowPath, err)
	}
	if len(allow) == 0 {
		return nil
	}
	merged := append([]string{}, cfg.Sandbox.Permissions.Allow...)
	for _, entry := range allow {
		if !containsString(merged, entry) {
			merged = append(merged, entry)
		}
	}
	cfg.Sandbox.Permissions.Allow = merged
	return nil
}

func containsString(list []string, target string) bool {
	for _, entry := range list {
		if entry == target {
			return true
		}
	}
	return false
}

func loadFile(configPath string, missingOK, projectDefaults bool) (*Config, error) {
	cfg := DefaultConfig()
	// Model defaults are useful when a model object is present (for example,
	// they fill context_window and max_tokens), but the default model name must
	// not make an omitted model field look configured. Clearing the names before
	// decoding also makes an object without a model name equivalent to null.
	if cfg.Model != nil {
		cfg.Model.Model = ""
	}
	if cfg.ImageModel != nil {
		cfg.ImageModel.Model = ""
	}
	if projectDefaults {
		cfg.Workspace = "workspace"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if missingOK && os.IsNotExist(err) {
			cfg.applyRuntimeDefaults()
			return cfg, nil
		}
		return nil, err
	}

	// Support both JSON and YAML
	if strings.HasSuffix(configPath, ".json") {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	cfg.expandEnv()
	cfg.normalizeOptionalModels()
	cfg.resolveRelativePaths(filepath.Dir(configPath))
	cfg.applyRuntimeDefaults()
	cfg.configPath = configPath

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func findConfig(fridayDir string) (string, error) {
	for _, name := range []string{"config.json", "friday.yaml"} {
		path := filepath.Join(fridayDir, name)
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("config path is a directory: %s", path)
			}
			return path, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

func homeFridayDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.Getenv("HOME")
	}
	if strings.TrimSpace(home) == "" {
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return "", fmt.Errorf("resolve home directory: HOME is empty")
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(absHome, ".friday"), nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Collaboration.Plan.ReasoningEffort) == "" {
		c.Collaboration.Plan.ReasoningEffort = providers.ReasoningEffortMedium
	}
	switch strings.ToLower(strings.TrimSpace(c.TUI.AlternateScreen)) {
	case "", "auto", "always", "never":
	default:
		return fmt.Errorf("invalid tui.alternate_screen %q: must be auto, always, or never", c.TUI.AlternateScreen)
	}
	if c.Model != nil {
		if e := c.Model.ReasoningEffort; e != "" && !providers.IsValidReasoningEffort(e) {
			return fmt.Errorf("invalid model.reasoning_effort %q: must be one of default, none, low, medium, high, xhigh, max", e)
		}
	}
	for _, m := range c.Models {
		if e := m.ReasoningEffort; e != "" && !providers.IsValidReasoningEffort(e) {
			return fmt.Errorf("invalid models entry %q: reasoning_effort %q must be one of default, none, low, medium, high, xhigh, max", m.Model, e)
		}
	}
	if e := c.Collaboration.Plan.ReasoningEffort; !providers.IsValidReasoningEffort(e) {
		return fmt.Errorf("invalid collaboration.plan.reasoning_effort %q: must be one of default, none, low, medium, high, xhigh, max", e)
	}
	if c.Sandbox != nil {
		if err := c.Sandbox.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) expandEnv() {
	expandModelEnv(c.Model)
	for i := range c.Models {
		expandModelEnv(&c.Models[i])
	}
	c.DataDir = expandEnvStr(c.DataDir)
	c.Workspace = expandEnvStr(c.Workspace)
	expandModelEnv(c.ImageModel)
}

func (c *Config) resolveRelativePaths(baseDir string) {
	c.DataDir = resolvePathFrom(baseDir, c.DataDir)
	if c.Workspace != "" {
		c.Workspace = resolvePathFrom(baseDir, c.Workspace)
	}
}

func resolvePathFrom(baseDir, path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		return filepath.Clean(filepath.Join(home, path[2:]))
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func expandModelEnv(m *ModelConfig) {
	if m == nil {
		return
	}
	m.Key = expandEnvStr(m.Key)
	m.BaseURL = expandEnvStr(m.BaseURL)
	m.Input = expandEnvStr(m.Input)
	m.Model = expandEnvStr(m.Model)
	m.Proxy = expandEnvStr(m.Proxy)
}

func expandEnvStr(s string) string {
	if s == "" {
		return ""
	}
	return os.Expand(s, func(v string) string {
		return os.Getenv(v)
	})
}

func (c *Config) ResolvePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func (c *Config) DataDirPath() string {
	return c.ResolvePath(c.DataDir)
}

func (c *Config) WorkspacePath() string {
	if c.Workspace == "" {
		return filepath.Join(c.DataDirPath(), "workspace")
	}
	return c.ResolvePath(c.Workspace)
}

// WorkspaceFallbackPaths returns lower-priority workspace directories used
// when a file is absent from the active workspace.
func (c *Config) WorkspaceFallbackPaths() []string {
	return append([]string(nil), c.workspaceFallbacks...)
}

// AgentPaths returns agent definition directories from lowest to highest
// priority. HOME definitions are loaded first and project definitions replace
// agents with the same normalized name.
func (c *Config) AgentPaths() []string {
	if len(c.agentPaths) > 0 {
		return append([]string(nil), c.agentPaths...)
	}
	return []string{filepath.Join(c.DataDirPath(), "agents")}
}

// ProjectScoped reports whether a non-HOME configuration is active.
func (c *Config) ProjectScoped() bool { return c.projectScope }

// ConfigPath returns the loaded configuration path, or an empty string when
// built-in defaults are active.
func (c *Config) ConfigPath() string { return c.configPath }

func (c *Config) SessionsPath() string {
	return filepath.Join(c.DataDirPath(), "sessions")
}

func (c *Config) ProjectsPath() string {
	return filepath.Join(c.DataDirPath(), "projects")
}

func (c *Config) MemoryPath() string {
	return filepath.Join(c.DataDirPath(), "memory")
}

func (c *Config) StatePath() string {
	return filepath.Join(c.DataDirPath(), "states")
}

// CachesPath is the root for reusable namespaced caches.
func (c *Config) CachesPath() string {
	return filepath.Join(c.DataDirPath(), "caches")
}

func LogPath() string {
	return filepath.Join("/tmp", fmt.Sprintf("friday-%s.log", time.Now().Format(time.DateOnly)))
}

func WriteDefaultConfig(path string) (bool, error) {
	return WriteConfig(path, DefaultConfig())
}

// WriteConfig writes cfg only when path does not already exist.
func WriteConfig(path string, cfg *Config) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if cfg == nil {
		return false, fmt.Errorf("config is required")
	}
	var data []byte
	var err error

	if strings.HasSuffix(path, ".json") {
		data, err = json.MarshalIndent(cfg, "", "  ")
	} else {
		data, err = yaml.Marshal(cfg)
	}
	if err != nil {
		return false, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return false, err
	}
	return true, nil
}
