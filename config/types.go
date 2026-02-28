package config

type Config struct {
	API      APIConfig      `yaml:"api"`
	DataDir  string         `yaml:"data_dir"`
	Workspace string        `yaml:"workspace"`
	Memory   MemoryConfig  `yaml:"memory"`
	Session  SessionConfig `yaml:"session"`
}

type APIConfig struct {
	Provider  string `yaml:"provider"`
	BaseURL   string `yaml:"base_url"`
	Key       string `yaml:"key"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
}

type MemoryConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
}

type SessionConfig struct {
	CompactThreshold int64  `yaml:"compact_threshold"`
	DefaultAgent     string `yaml:"default_agent"`
}

func DefaultConfig() *Config {
	return &Config{
		API: APIConfig{
			Provider:  "openai",
			BaseURL:   "",
			Key:       "",
			Model:     "gpt-4o",
			MaxTokens: 4096,
		},
		DataDir:   "~/.friday",
		Workspace: "~/.friday/workspace",
		Memory: MemoryConfig{
			Enabled: true,
			Days:    2,
		},
		Session: SessionConfig{
			CompactThreshold: 3000,
			DefaultAgent:     "react",
		},
	}
}
