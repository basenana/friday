package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		configPath = filepath.Join(home, ".friday", "friday.yaml")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	cfg.expandEnv()

	return cfg, nil
}

func (c *Config) expandEnv() {
	c.API.Key = expandEnvStr(c.API.Key)
	c.API.BaseURL = expandEnvStr(c.API.BaseURL)
	c.DataDir = expandEnvStr(c.DataDir)
	c.Workspace = expandEnvStr(c.Workspace)
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
	return c.ResolvePath(c.Workspace)
}

func (c *Config) SessionsPath() string {
	return filepath.Join(c.DataDirPath(), "sessions")
}

func (c *Config) MemoryPath() string {
	return filepath.Join(c.DataDirPath(), "memory")
}
