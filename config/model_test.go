package config

import "testing"

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
