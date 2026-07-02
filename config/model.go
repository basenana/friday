package config

import (
	"fmt"
	"strings"
)

// ChatModels returns the configured primary/fallback model list for text chat.
// Completely empty placeholder entries are ignored.
func (c *Config) ChatModels() []ModelConfig {
	if len(c.Models) == 0 {
		return []ModelConfig{c.Model}
	}

	models := make([]ModelConfig, 0, len(c.Models))
	for _, model := range c.Models {
		if model.IsConfigured() {
			models = append(models, model)
		}
	}
	if len(models) > 0 {
		return models
	}
	return []ModelConfig{c.Model}
}

// PrimaryModel returns the first effective chat model.
func (c *Config) PrimaryModel() ModelConfig {
	models := c.ChatModels()
	if len(models) == 0 {
		return ModelConfig{}
	}
	return models[0]
}

// AgentModel returns the effective ModelConfig for the named agent.
// The per-agent entry (if configured) overlays the primary model, allowing
// partial overrides (e.g. only setting the model name to a cheaper variant).
// Unconfigured or missing agents fall back to PrimaryModel.
func (c *Config) AgentModel(name string) ModelConfig {
	if m, ok := c.Agents[name]; ok && m.IsConfigured() {
		base := c.PrimaryModel()
		base.overlay(m)
		return base
	}
	return c.PrimaryModel()
}

// IsConfigured returns true when any meaningful model field is set.
func (m ModelConfig) IsConfigured() bool {
	return strings.TrimSpace(m.Provider) != "" ||
		strings.TrimSpace(m.BaseURL) != "" ||
		strings.TrimSpace(m.Key) != "" ||
		strings.TrimSpace(m.Input) != "" ||
		strings.TrimSpace(m.Model) != ""
}

// HasInput reports whether the configured input types contain kind.
func (m ModelConfig) HasInput(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return false
	}

	for _, part := range strings.FieldsFunc(strings.ToLower(m.Input), func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if strings.TrimSpace(part) == kind {
			return true
		}
	}
	return false
}

// ResolveImageModel returns the effective model config for image understanding.
// image_model takes precedence when configured; otherwise a multimodal primary model is used.
func (c *Config) ResolveImageModel(modelOverride string) (ModelConfig, error) {
	var selected ModelConfig
	base := c.PrimaryModel()

	switch {
	case c.ImageModel.IsConfigured():
		selected = base
		selected.overlay(c.ImageModel)
	case base.HasInput("image"):
		selected = base
	default:
		return ModelConfig{}, fmt.Errorf("no image-capable model configured: set image_model or add image to the primary model input")
	}

	if modelOverride != "" {
		selected.Model = modelOverride
	}
	if strings.TrimSpace(selected.Model) == "" {
		return ModelConfig{}, fmt.Errorf("image model name is empty")
	}
	if strings.TrimSpace(selected.Provider) == "" {
		selected.Provider = "openai"
	}
	return selected, nil
}

func (m *ModelConfig) overlay(src ModelConfig) {
	if strings.TrimSpace(src.Provider) != "" {
		m.Provider = src.Provider
	}
	if strings.TrimSpace(src.BaseURL) != "" {
		m.BaseURL = src.BaseURL
	}
	if strings.TrimSpace(src.Key) != "" {
		m.Key = src.Key
	}
	if strings.TrimSpace(src.Input) != "" {
		m.Input = src.Input
	}
	if strings.TrimSpace(src.Model) != "" {
		m.Model = src.Model
	}
	if src.ContextWindow != 0 {
		m.ContextWindow = src.ContextWindow
	}
	if src.MaxTokens != 0 {
		m.MaxTokens = src.MaxTokens
	}
	if src.Temperature != 0 {
		m.Temperature = src.Temperature
	}
	if src.QPM != 0 {
		m.QPM = src.QPM
	}
	if strings.TrimSpace(src.Proxy) != "" {
		m.Proxy = src.Proxy
	}
}
