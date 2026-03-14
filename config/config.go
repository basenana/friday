package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		// Try JSON first, then YAML
		jsonPath := filepath.Join(home, ".friday", "config.json")
		yamlPath := filepath.Join(home, ".friday", "friday.yaml")

		if _, err := os.Stat(jsonPath); err == nil {
			configPath = jsonPath
		} else if _, err := os.Stat(yamlPath); err == nil {
			configPath = yamlPath
		} else {
			return cfg, nil // use default
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
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

	return cfg, nil
}

func (c *Config) expandEnv() {
	c.Model.Key = expandEnvStr(c.Model.Key)
	c.Model.BaseURL = expandEnvStr(c.Model.BaseURL)
	c.DataDir = expandEnvStr(c.DataDir)
	c.Workspace = expandEnvStr(c.Workspace)
	c.Model.Model = expandEnvStr(c.Model.Model)
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

func LogPath() string {
	return filepath.Join("/tmp", fmt.Sprintf("friday-%s.log", time.Now().Format(time.DateOnly)))
}

func WriteDefaultConfig(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}

	cfg := DefaultConfig()
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
