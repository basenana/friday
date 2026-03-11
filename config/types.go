package config

type Config struct {
	Model     ModelConfig   `yaml:"model"`
	DataDir   string        `yaml:"data_dir"`
	Workspace string        `yaml:"workspace"`
	Memory    MemoryConfig  `yaml:"memory"`
	Session   SessionConfig `yaml:"session"`
}

type ModelConfig struct {
	Provider    string  `yaml:"provider" json:"provider"` // "openai" or "anthropic"
	BaseURL     string  `yaml:"base_url" json:"base_url"`
	Key         string  `yaml:"key" json:"key"`
	Model       string  `yaml:"model" json:"model"`
	MaxTokens   int     `yaml:"max_tokens" json:"max_tokens"`
	Temperature float64 `yaml:"temperature" json:"temperature"`
	QPM         int64   `yaml:"qpm" json:"qpm"`
	Proxy       string  `yaml:"proxy" json:"proxy"`
}

type MemoryConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
}

type SessionConfig struct {
	DefaultAgent string `yaml:"default_agent"`
}

func DefaultConfig() *Config {
	return &Config{
		Model: ModelConfig{
			Provider:    "openai",
			BaseURL:     "",
			Key:         "",
			Model:       "gpt-4o",
			MaxTokens:   4096,
			Temperature: 0.7,
			QPM:         60,
		},
		DataDir:   "~/.friday",
		Workspace: "~/.friday/workspace",
		Memory: MemoryConfig{
			Enabled: true,
			Days:    2,
		},
		Session: SessionConfig{
			DefaultAgent: "react",
		},
	}
}
