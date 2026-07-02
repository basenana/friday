package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/basenana/friday/config"
)

// E2EConfig is the top-level configuration for e2e tests.
// It is loaded from a local YAML file that is NOT checked into git.
type E2EConfig struct {
	Models  map[string]config.ModelConfig `yaml:"models"`
	Sandbox SandboxToggle                 `yaml:"sandbox"`
	Retry   RetryConfig                   `yaml:"retry"`
	Timeout TimeoutConfig                 `yaml:"timeout"`
}

// SandboxToggle controls whether OS-level sandboxing (bwrap/seatbelt) is
// applied during tool execution. Disabling it is useful when the sandbox
// binaries are unavailable (e.g. CI containers without bwrap).
type SandboxToggle struct {
	Enabled bool `yaml:"enabled"`
}

// RetryConfig controls the retry behaviour for non-deterministic LLM
// assertions.
type RetryConfig struct {
	MaxAttempts int    `yaml:"max_attempts"`
	Backoff     string `yaml:"backoff"`
}

// TimeoutConfig controls per-test and per-suite deadlines.
type TimeoutConfig struct {
	PerTest  string `yaml:"per_test"`
	PerSuite string `yaml:"per_suite"`
}

// BackoffDuration returns the parsed backoff duration with a sane default.
func (c *E2EConfig) BackoffDuration() time.Duration {
	if c == nil || c.Retry.Backoff == "" {
		return 5 * time.Second
	}
	d, err := time.ParseDuration(c.Retry.Backoff)
	if err != nil {
		return 5 * time.Second
	}
	return d
}

// MaxAttempts returns the configured retry count (default 3).
func (c *E2EConfig) MaxAttempts() int {
	if c == nil || c.Retry.MaxAttempts <= 0 {
		return 3
	}
	return c.Retry.MaxAttempts
}

// TestTimeout returns the per-test deadline (default 120s).
func (c *E2EConfig) TestTimeout() time.Duration {
	if c == nil || c.Timeout.PerTest == "" {
		return 120 * time.Second
	}
	d, err := time.ParseDuration(c.Timeout.PerTest)
	if err != nil {
		return 120 * time.Second
	}
	return d
}

// SuiteTimeout returns the overall suite deadline (default 30m).
func (c *E2EConfig) SuiteTimeout() time.Duration {
	if c == nil || c.Timeout.PerSuite == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(c.Timeout.PerSuite)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}

// LoadE2EConfig reads and parses the e2e config from the given path.
// String fields in ModelConfig are expanded with os.ExpandEnv so that
// secrets can be referenced via ${ENV_VAR} without hard-coding them.
func LoadE2EConfig(path string) (*E2EConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read e2e config %s: %w", path, err)
	}

	// Expand env vars before YAML parsing so ${VAR} placeholders in both
	// keys and URLs are resolved.
	expanded := os.ExpandEnv(string(raw))

	var cfg E2EConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse e2e config: %w", err)
	}

	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("e2e config has no models defined")
	}

	// Normalise model configs: trim whitespace and expand any remaining
	// env references in fields that yaml ExpandEnv might have missed
	// (e.g. values quoted in a way that prevents expansion).
	for name, m := range cfg.Models {
		m.Provider = strings.TrimSpace(m.Provider)
		m.BaseURL = strings.TrimSpace(m.BaseURL)
		m.Key = strings.TrimSpace(m.Key)
		m.Model = strings.TrimSpace(m.Model)
		cfg.Models[name] = m
	}

	return &cfg, nil
}

// FindE2EConfig locates the e2e config file by searching a set of
// candidate paths. It returns the first match or ("", nil) if none found.
func FindE2EConfig() string {
	// 1. Explicit override.
	if p := os.Getenv("E2E_CONFIG"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// 2. Conventional local location (gitignored).
	// Walk up from the current dir to find a `.local/e2e.yaml` so the test
	// works regardless of which package dir is the cwd.
	candidates := []string{
		"e2e/testdata/e2e.yaml",
	}
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			c := filePath(dir, ".local", "e2e.yaml")
			if _, err := os.Stat(c); err == nil {
				return c
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filePath(home, ".friday", "e2e.yaml"),
		)
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}

func filePath(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += "/" + p
	}
	return out
}
