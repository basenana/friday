package config

import "github.com/basenana/friday/sandbox"

type Config struct {
	Model         *ModelConfig        `yaml:"model" json:"model"`
	Models        []ModelConfig       `yaml:"models" json:"models"`
	ImageModel    *ModelConfig        `yaml:"image_model" json:"image_model"`
	DataDir       string              `yaml:"data_dir" json:"data_dir"`
	Workspace     string              `yaml:"workspace" json:"workspace"`
	Memory        MemoryConfig        `yaml:"memory" json:"memory"`
	Session       SessionConfig       `yaml:"session" json:"session"`
	Log           LogConfig           `yaml:"log" json:"log"`
	Sandbox       *sandbox.Config     `yaml:"sandbox" json:"sandbox"`
	TUI           TUIConfig           `yaml:"tui" json:"tui"`
	Editor        EditorConfig        `yaml:"editor" json:"editor"`
	Worktree      WorktreeConfig      `yaml:"worktree" json:"worktree"`
	Collaboration CollaborationConfig `yaml:"collaboration" json:"collaboration"`

	// Runtime-only workspace layering metadata. These fields are populated by
	// LoadForDir and intentionally stay out of serialized configuration files.
	workspaceFallbacks []string
	agentPaths         []string
	projectScope       bool
	configPath         string
}

type CollaborationConfig struct {
	Plan PlanModeConfig `yaml:"plan" json:"plan"`
}

type PlanModeConfig struct {
	ReasoningEffort string `yaml:"reasoning_effort" json:"reasoning_effort"`
}

type TUIConfig struct {
	// AlternateScreen controls terminal scrollback behavior: auto uses the
	// alternate screen except in known-incompatible multiplexers such as
	// Zellij; always/never force the choice.
	AlternateScreen string `yaml:"alternate_screen" json:"alternate_screen"`
}

type WorktreeConfig struct {
	Directory    string `yaml:"directory" json:"directory"`
	BranchPrefix string `yaml:"branch_prefix" json:"branch_prefix"`
}

type EditorConfig struct {
	Command         string `yaml:"command" json:"command"`
	RemoteAuthority string `yaml:"remote_authority" json:"remote_authority"`
}

type LogConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

type ModelConfig struct {
	Provider        string  `yaml:"provider" json:"provider"` // "openai", "openai-response", or "anthropic"
	BaseURL         string  `yaml:"base_url" json:"base_url"`
	Key             string  `yaml:"key" json:"key"`
	Input           string  `yaml:"input" json:"input"` // "text" "image"
	Model           string  `yaml:"model" json:"model"`
	ContextWindow   int64   `yaml:"context_window" json:"context_window"`
	MaxTokens       int     `yaml:"max_tokens" json:"max_tokens"`
	Temperature     float64 `yaml:"temperature" json:"temperature"`
	QPM             int64   `yaml:"qpm" json:"qpm"`
	Proxy           string  `yaml:"proxy" json:"proxy"`
	ReasoningEffort string  `yaml:"reasoning_effort" json:"reasoning_effort"` // thinking-mode effort: default/none/low/medium/high/xhigh/max
	ReasoningSplit  bool    `yaml:"reasoning_split" json:"reasoning_split"`
}

type MemoryConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	Days    int  `yaml:"days" json:"days"`
}

type SessionConfig struct {
	DefaultAgent string `yaml:"default_agent" json:"default_agent"`
}

func DefaultConfig() *Config {
	return &Config{
		Model:      DefaultModelConfig(),
		ImageModel: DefaultImageModelConfig(),
		DataDir:    "~/.friday",
		Workspace:  "~/.friday/workspace",
		Memory: MemoryConfig{
			Enabled: true,
		},
		Session: SessionConfig{
			DefaultAgent: "react",
		},
		TUI:           TUIConfig{AlternateScreen: "auto"},
		Editor:        EditorConfig{Command: "code"},
		Worktree:      WorktreeConfig{Directory: ".friday/worktrees", BranchPrefix: "friday/"},
		Collaboration: CollaborationConfig{Plan: PlanModeConfig{ReasoningEffort: "medium"}},
		Sandbox:       sandbox.DefaultConfig(),
	}
}

// DefaultModelConfig returns the usable chat model written by friday init.
func DefaultModelConfig() *ModelConfig {
	return &ModelConfig{
		Provider:      "openai",
		Model:         "gpt-4o",
		ContextWindow: 128000,
		MaxTokens:     4096,
		Temperature:   0.7,
		QPM:           60,
	}
}

// DefaultImageModelConfig returns an inactive, editable image model template.
// Only numeric tuning defaults are populated so it is not considered
// configured until the user supplies a model name. Keeping the pointer non-nil
// makes friday init emit the fields instead of an unhelpful JSON null.
func DefaultImageModelConfig() *ModelConfig {
	return &ModelConfig{
		ContextWindow: 128000,
		MaxTokens:     4096,
		Temperature:   0.7,
		QPM:           60,
	}
}

func (c *Config) applyRuntimeDefaults() {
	if c.Sandbox == nil {
		c.Sandbox = sandbox.DefaultConfig()
	}
	c.Sandbox.ApplyRuntimeDefaults()
}
