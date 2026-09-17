package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ModelIdentity uniquely identifies one configured provider endpoint. Model
// names are deliberately not unique: several servers may expose the same
// model and all of them remain eligible fallback candidates.
type ModelIdentity struct {
	Provider string
	Server   string
	Model    string
}

// CanonicalProvider normalizes aliases used by provider construction.
func CanonicalProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openai":
		return "openai"
	case "openai-response", "openai-responses":
		return "openai-responses"
	case "anthropic":
		return "anthropic"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

// EffectiveBaseURL returns the endpoint used when base_url is omitted.
func (m ModelConfig) EffectiveBaseURL() string {
	if base := strings.TrimSpace(m.BaseURL); base != "" {
		return base
	}
	switch CanonicalProvider(m.Provider) {
	case "anthropic":
		return "https://api.anthropic.com"
	default:
		return "https://api.openai.com/v1"
	}
}

// Identity returns the normalized provider/server/model endpoint key.
func (m ModelConfig) Identity() ModelIdentity {
	return ModelIdentity{
		Provider: CanonicalProvider(m.Provider),
		Server:   canonicalServer(m.EffectiveBaseURL()),
		Model:    strings.TrimSpace(m.Model),
	}
}

func canonicalServer(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

// ChatModels returns the complete ordered model catalog: model is the
// configured first choice and models supplies the remaining candidates.
// Exact provider/server/model duplicates are removed with first entry wins.
func (c *Config) ChatModels() []ModelConfig {
	if c == nil {
		return nil
	}
	candidates := make([]ModelConfig, 0, len(c.Models)+1)
	if c.Model != nil {
		candidates = append(candidates, *c.Model)
	}
	candidates = append(candidates, c.Models...)

	models := make([]ModelConfig, 0, len(candidates))
	seen := make(map[ModelIdentity]struct{}, len(candidates))
	for _, model := range candidates {
		if !model.IsConfigured() {
			continue
		}
		model.Model = strings.TrimSpace(model.Model)
		if model.Model == "" {
			continue
		}
		model.Provider = CanonicalProvider(model.Provider)
		model.BaseURL = strings.TrimSpace(model.BaseURL)
		identity := model.Identity()
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		models = append(models, model)
	}
	return models
}

// ModelNames returns configured model names once each, in catalog order.
func (c *Config) ModelNames() []string {
	var names []string
	for _, model := range c.ChatModels() {
		if !slices.Contains(names, model.Model) {
			names = append(names, model.Model)
		}
	}
	return names
}

// HasModelName reports whether name identifies at least one configured
// endpoint. Model IDs are case-sensitive.
func (c *Config) HasModelName(name string) bool {
	return slices.Contains(c.ModelNames(), strings.TrimSpace(name))
}

// PreferModel returns a stable partition of the catalog with every endpoint
// exposing name first. An empty or unknown name preserves catalog order.
func (c *Config) PreferModel(name string) []ModelConfig {
	models := c.ChatModels()
	name = strings.TrimSpace(name)
	if name == "" || !c.HasModelName(name) {
		return models
	}
	preferred := make([]ModelConfig, 0, len(models))
	rest := make([]ModelConfig, 0, len(models))
	for _, model := range models {
		if model.Model == name {
			preferred = append(preferred, model)
		} else {
			rest = append(rest, model)
		}
	}
	return append(preferred, rest...)
}

// PrimaryModel returns the first effective chat model.
func (c *Config) PrimaryModel() ModelConfig {
	models := c.ChatModels()
	if len(models) == 0 {
		return ModelConfig{}
	}
	return models[0]
}

// normalizeOptionalModels gives an absent model name the same meaning as a
// null model object. It runs after environment expansion so a name sourced
// from an unset environment variable is inactive too.
func (c *Config) normalizeOptionalModels() {
	if c == nil {
		return
	}
	if c.Model != nil && strings.TrimSpace(c.Model.Model) == "" {
		c.Model = nil
	}
	if c.ImageModel != nil && strings.TrimSpace(c.ImageModel.Model) == "" {
		c.ImageModel = nil
	}
}

// IsConfigured returns true only when the required model name is set.
// Connection and tuning fields alone do not identify a usable model.
func (m ModelConfig) IsConfigured() bool {
	return strings.TrimSpace(m.Model) != ""
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
	if c == nil {
		return ModelConfig{}, fmt.Errorf("no image-capable model configured: set image_model or add image to the primary model input")
	}
	var selected ModelConfig
	base := c.PrimaryModel()

	switch {
	case c.ImageModel != nil && c.ImageModel.IsConfigured():
		selected = base
		selected.overlay(*c.ImageModel)
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
