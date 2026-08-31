package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const maxConfigFileSize = 1 << 20 // 1 MiB

// Config is the top-level configuration for sandbox
type Config struct {
	Permissions PermissionsConfig `json:"permissions" yaml:"permissions"`
	Sandbox     SandboxConfig     `json:"sandbox" yaml:"sandbox"`
}

// PermissionsConfig defines allow/deny rules for commands
type PermissionsConfig struct {
	Allow []string `json:"allow" yaml:"allow"`
	Deny  []string `json:"deny" yaml:"deny"`
}

// SandboxConfig defines sandbox isolation settings
type SandboxConfig struct {
	Enabled    bool             `json:"enabled" yaml:"enabled"`
	Filesystem FilesystemConfig `json:"filesystem" yaml:"filesystem"`
	Network    NetworkConfig    `json:"network" yaml:"network"`
	Defaults   DefaultsConfig   `json:"defaults" yaml:"defaults"`
}

// FilesystemConfig defines filesystem access control
type FilesystemConfig struct {
	// ReadOnly paths are mounted as read-only
	ReadOnly []string `json:"readonly" yaml:"readonly"`
	// Deny paths are completely blocked
	Deny []string `json:"deny" yaml:"deny"`
	// Write paths are allowed for writing
	Write []string `json:"write" yaml:"write"`
	// Protected paths cannot be written even if in Write paths
	Protected []string `json:"protected" yaml:"protected"`
}

// NetworkConfig defines network access control
type NetworkConfig struct {
	Isolation bool     `json:"isolation" yaml:"isolation"`
	Allow     []string `json:"allow" yaml:"allow"`
}

// DefaultsConfig defines default execution parameters
type DefaultsConfig struct {
	Timeout string `json:"timeout" yaml:"timeout"` // e.g. "5m"
}

// LoadConfig loads sandbox configuration from file
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}

	// Open the file and stat the opened descriptor so the permission and
	// size checks apply to the file that is actually read (closing the
	// Stat/ReadFile TOCTOU window).
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if err := validateConfigFilePerm(path, info); err != nil {
		return nil, err
	}
	if info.Size() > maxConfigFileSize {
		return nil, fmt.Errorf("config file too large: %s exceeds %d bytes", path, maxConfigFileSize)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxConfigFileSize {
		return nil, fmt.Errorf("config file too large: %s exceeds %d bytes", path, maxConfigFileSize)
	}

	if strings.HasSuffix(strings.ToLower(path), ".json") {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks the config for invalid values
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.Sandbox.Defaults.Timeout); c.Sandbox.Defaults.Timeout != "" && err != nil {
		return fmt.Errorf("invalid sandbox.defaults.timeout: %w", err)
	}
	for _, entry := range c.Sandbox.Network.Allow {
		if err := validateNetworkAllowEntry(entry); err != nil {
			return fmt.Errorf("invalid sandbox.network.allow: %w", err)
		}
	}
	return nil
}

func validateConfigFilePerm(path string, info os.FileInfo) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config path is not a regular file: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("config file permissions too open: %s (%s)", path, info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if dirInfo.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("config directory is world-writable: %s", filepath.Dir(path))
	}
	return nil
}
