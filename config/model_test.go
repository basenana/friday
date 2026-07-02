package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelConfigHasInput(t *testing.T) {
	cfg := ModelConfig{Input: "text, image ; audio"}

	if !cfg.HasInput("image") {
		t.Fatalf("expected image input to be detected")
	}
	if cfg.HasInput("video") {
		t.Fatalf("did not expect video input to be detected")
	}
}

func TestResolveImageModel(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		overrideModel string
		wantModel     string
		wantProvider  string
		wantKey       string
		wantErr       bool
	}{
		{
			name: "prefer image model and inherit connection settings",
			cfg: &Config{
				Model: ModelConfig{
					Provider: "openai",
					Key:      "main-key",
					Model:    "gpt-4.1",
				},
				ImageModel: ModelConfig{
					Model: "gpt-4.1-mini-vision",
				},
			},
			wantModel:    "gpt-4.1-mini-vision",
			wantProvider: "openai",
			wantKey:      "main-key",
		},
		{
			name: "fallback to multimodal primary model",
			cfg: &Config{
				Model: ModelConfig{
					Provider: "anthropic",
					Key:      "main-key",
					Model:    "claude-sonnet",
					Input:    "text,image",
				},
			},
			wantModel:    "claude-sonnet",
			wantProvider: "anthropic",
			wantKey:      "main-key",
		},
		{
			name: "allow per-call model override",
			cfg: &Config{
				Model: ModelConfig{
					Provider: "openai",
					Key:      "main-key",
					Model:    "gpt-4.1",
				},
				ImageModel: ModelConfig{
					Model: "gpt-4.1-mini-vision",
				},
			},
			overrideModel: "gpt-4.1-nano-vision",
			wantModel:     "gpt-4.1-nano-vision",
			wantProvider:  "openai",
			wantKey:       "main-key",
		},
		{
			name: "error when no image-capable model exists",
			cfg: &Config{
				Model: ModelConfig{
					Provider: "openai",
					Model:    "gpt-4.1",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.ResolveImageModel(tt.overrideModel)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveImageModel() error = %v", err)
			}
			if got.Model != tt.wantModel {
				t.Fatalf("ResolveImageModel() model = %q, want %q", got.Model, tt.wantModel)
			}
			if got.Provider != tt.wantProvider {
				t.Fatalf("ResolveImageModel() provider = %q, want %q", got.Provider, tt.wantProvider)
			}
			if got.Key != tt.wantKey {
				t.Fatalf("ResolveImageModel() key = %q, want %q", got.Key, tt.wantKey)
			}
		})
	}
}

func TestResolveImageModelPrefersPrimaryModelFromModelsList(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Model = ModelConfig{
		Provider: "openai",
		Key:      "default-key",
		Model:    "default-model",
	}
	cfg.Models = []ModelConfig{
		{
			Provider: "anthropic",
			Key:      "models-key",
			Model:    "claude-sonnet",
			Input:    "text,image",
		},
	}
	cfg.ImageModel = ModelConfig{
		Model: "claude-vision",
	}

	got, err := cfg.ResolveImageModel("")
	if err != nil {
		t.Fatalf("ResolveImageModel() error = %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("ResolveImageModel() provider = %q, want %q", got.Provider, "anthropic")
	}
	if got.Key != "models-key" {
		t.Fatalf("ResolveImageModel() key = %q, want %q", got.Key, "models-key")
	}
	if got.Model != "claude-vision" {
		t.Fatalf("ResolveImageModel() model = %q, want %q", got.Model, "claude-vision")
	}
}

func TestExpandEnvIncludesImageModel(t *testing.T) {
	t.Setenv("FRIDAY_IMAGE_KEY", "img-key")
	t.Setenv("FRIDAY_IMAGE_BASE", "https://example.com")
	t.Setenv("FRIDAY_IMAGE_MODEL", "vision-model")

	cfg := &Config{
		ImageModel: ModelConfig{
			Key:     "$FRIDAY_IMAGE_KEY",
			BaseURL: "$FRIDAY_IMAGE_BASE",
			Model:   "$FRIDAY_IMAGE_MODEL",
		},
	}

	cfg.expandEnv()

	if cfg.ImageModel.Key != "img-key" {
		t.Fatalf("image_model.key not expanded, got %q", cfg.ImageModel.Key)
	}
	if cfg.ImageModel.BaseURL != "https://example.com" {
		t.Fatalf("image_model.base_url not expanded, got %q", cfg.ImageModel.BaseURL)
	}
	if cfg.ImageModel.Model != "vision-model" {
		t.Fatalf("image_model.model not expanded, got %q", cfg.ImageModel.Model)
	}
}

func TestLoadExpandsEnvInModelsList(t *testing.T) {
	t.Setenv("FRIDAY_MODELS_KEY", "models-key")
	t.Setenv("FRIDAY_MODELS_BASE", "https://models.example.com")
	t.Setenv("FRIDAY_MODELS_NAME", "models-name")
	t.Setenv("FRIDAY_MODELS_PROXY", "http://proxy.example.com:8080")

	dir := t.TempDir()
	path := filepath.Join(dir, "friday.yaml")
	data := []byte(`
models:
  - provider: openai
    key: $FRIDAY_MODELS_KEY
    base_url: $FRIDAY_MODELS_BASE
    model: $FRIDAY_MODELS_NAME
    input: text,image
    proxy: $FRIDAY_MODELS_PROXY
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(cfg.Models))
	}
	if cfg.Models[0].Key != "models-key" {
		t.Fatalf("models[0].key = %q, want %q", cfg.Models[0].Key, "models-key")
	}
	if cfg.Models[0].BaseURL != "https://models.example.com" {
		t.Fatalf("models[0].base_url = %q, want %q", cfg.Models[0].BaseURL, "https://models.example.com")
	}
	if cfg.Models[0].Model != "models-name" {
		t.Fatalf("models[0].model = %q, want %q", cfg.Models[0].Model, "models-name")
	}
	if cfg.Models[0].Proxy != "http://proxy.example.com:8080" {
		t.Fatalf("models[0].proxy = %q, want %q", cfg.Models[0].Proxy, "http://proxy.example.com:8080")
	}
}
