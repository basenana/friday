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

func TestModelConfigRequiresModelName(t *testing.T) {
	unnamed := ModelConfig{
		Provider: "openai",
		BaseURL:  "https://example.com/v1",
		Key:      "secret",
		Input:    "text,image",
	}
	if unnamed.IsConfigured() {
		t.Fatalf("unnamed model unexpectedly configured: %#v", unnamed)
	}
	unnamed.Model = "named"
	if !unnamed.IsConfigured() {
		t.Fatalf("named model unexpectedly unconfigured: %#v", unnamed)
	}
}

func TestChatModelsDeduplicatesEndpointButKeepsSameNameAcrossServers(t *testing.T) {
	cfg := &Config{
		Model: &ModelConfig{Provider: "openai", BaseURL: "https://A.example/v1/", Model: "model-1"},
		Models: []ModelConfig{
			{Provider: "", BaseURL: "https://a.example/v1", Model: "model-1"},
			{Provider: "openai", BaseURL: "https://b.example/v1", Model: "model-1"},
			{Provider: "openai", BaseURL: "https://a.example/v1", Model: "model-2"},
		},
	}
	models := cfg.ChatModels()
	if len(models) != 3 {
		t.Fatalf("ChatModels = %#v, want three unique endpoints", models)
	}
	if got := cfg.ModelNames(); len(got) != 2 || got[0] != "model-1" || got[1] != "model-2" {
		t.Fatalf("ModelNames = %#v", got)
	}
}

func TestChatModelsAllowsNilPrimaryModel(t *testing.T) {
	cfg := &Config{
		Model: nil,
		Models: []ModelConfig{
			{Provider: "anthropic", Model: "claude-sonnet"},
			{Provider: "openai", Model: "gpt-4.1"},
		},
	}

	models := cfg.ChatModels()
	if len(models) != 2 || models[0].Model != "claude-sonnet" || models[1].Model != "gpt-4.1" {
		t.Fatalf("ChatModels() = %#v", models)
	}
	if got := cfg.PrimaryModel(); got.Model != "claude-sonnet" {
		t.Fatalf("PrimaryModel() = %#v", got)
	}
}

func TestLoadHonorsNullPrimaryModel(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{"model":null,"models":[{"provider":"anthropic","model":"claude-sonnet"}]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != nil {
		t.Fatalf("Model = %#v, want nil", cfg.Model)
	}
	models := cfg.ChatModels()
	if len(models) != 1 || models[0].Model != "claude-sonnet" {
		t.Fatalf("ChatModels() = %#v", models)
	}
}

func TestLoadTreatsMissingAndUnnamedOptionalModelsAsNull(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	t.Setenv("EMPTY_MODEL_NAME", "")
	tests := []struct {
		name     string
		filename string
		data     string
	}{
		{
			name:     "json fields absent",
			filename: "config.json",
			data:     `{"models":[{"provider":"anthropic","model":"fallback"}]}`,
		},
		{
			name:     "json objects have no names",
			filename: "config.json",
			data:     `{"model":{"provider":"openai","key":"secret"},"image_model":{"provider":"openai","input":"image"},"models":[{"provider":"anthropic","model":"fallback"}]}`,
		},
		{
			name:     "yaml fields absent",
			filename: "friday.yaml",
			data:     "models:\n  - provider: anthropic\n    model: fallback\n",
		},
		{
			name:     "yaml names expand to empty",
			filename: "friday.yaml",
			data:     "model:\n  provider: openai\n  model: $EMPTY_MODEL_NAME\nimage_model:\n  provider: openai\n  model: '   '\nmodels:\n  - provider: anthropic\n    model: fallback\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Model != nil {
				t.Fatalf("Model = %#v, want nil", cfg.Model)
			}
			if cfg.ImageModel != nil {
				t.Fatalf("ImageModel = %#v, want nil", cfg.ImageModel)
			}
			if got := cfg.PrimaryModel().Model; got != "fallback" {
				t.Fatalf("PrimaryModel().Model = %q, want fallback", got)
			}
		})
	}
}

func TestLoadNamedOptionalModelsKeepFieldDefaults(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"model":{"model":"chat"},"image_model":{"model":"vision"}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model == nil || cfg.Model.Model != "chat" || cfg.Model.ContextWindow == 0 || cfg.Model.MaxTokens == 0 {
		t.Fatalf("named Model did not retain defaults: %#v", cfg.Model)
	}
	if cfg.ImageModel == nil || cfg.ImageModel.Model != "vision" || cfg.ImageModel.ContextWindow == 0 || cfg.ImageModel.MaxTokens == 0 {
		t.Fatalf("named ImageModel did not retain defaults: %#v", cfg.ImageModel)
	}
}

func TestPreferModelStablePartitionsAllMatchingEndpoints(t *testing.T) {
	cfg := &Config{
		Model: &ModelConfig{Provider: "openai", BaseURL: "https://a.example", Model: "one"},
		Models: []ModelConfig{
			{Provider: "openai", BaseURL: "https://a.example", Model: "two"},
			{Provider: "openai", BaseURL: "https://b.example", Model: "one"},
			{Provider: "openai", BaseURL: "https://a.example", Model: "three"},
		},
	}
	got := cfg.PreferModel("one")
	wantServers := []string{"https://a.example", "https://b.example", "https://a.example", "https://a.example"}
	wantNames := []string{"one", "one", "two", "three"}
	for i := range wantNames {
		if got[i].Model != wantNames[i] || got[i].BaseURL != wantServers[i] {
			t.Fatalf("PreferModel[%d] = %+v", i, got[i])
		}
	}
}

func TestTUIAlternateScreenValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "friday.yaml")
	if err := os.WriteFile(path, []byte("tui:\n  alternate_screen: sideways\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid alternate-screen mode to fail")
	}
	cfg := DefaultConfig()
	if cfg.TUI.AlternateScreen != "auto" {
		t.Fatalf("default alternate screen = %q", cfg.TUI.AlternateScreen)
	}
}

func TestPlanReasoningEffortDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Collaboration.Plan.ReasoningEffort != "medium" {
		t.Fatalf("default plan effort = %q", cfg.Collaboration.Plan.ReasoningEffort)
	}
	cfg.Collaboration.Plan.ReasoningEffort = ""
	if err := cfg.validate(); err != nil || cfg.Collaboration.Plan.ReasoningEffort != "medium" {
		t.Fatalf("empty plan effort default: effort=%q err=%v", cfg.Collaboration.Plan.ReasoningEffort, err)
	}
	cfg.Collaboration.Plan.ReasoningEffort = "impossible"
	if err := cfg.validate(); err == nil {
		t.Fatal("invalid plan effort was accepted")
	}
}

func TestConfigValidateIncludesSandboxConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Sandbox.Filesystem.Deny = []string{"[unterminated"}
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() accepted invalid sandbox configuration")
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
				Model: &ModelConfig{
					Provider: "openai",
					Key:      "main-key",
					Model:    "gpt-4.1",
				},
				ImageModel: &ModelConfig{
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
				Model: &ModelConfig{
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
			name: "ignore unnamed image model and fallback to multimodal primary",
			cfg: &Config{
				Model: &ModelConfig{
					Provider: "anthropic",
					Key:      "main-key",
					Model:    "claude-sonnet",
					Input:    "text,image",
				},
				ImageModel: &ModelConfig{
					Provider: "openai",
					Key:      "image-key",
					Input:    "image",
				},
			},
			wantModel:    "claude-sonnet",
			wantProvider: "anthropic",
			wantKey:      "main-key",
		},
		{
			name: "allow per-call model override",
			cfg: &Config{
				Model: &ModelConfig{
					Provider: "openai",
					Key:      "main-key",
					Model:    "gpt-4.1",
				},
				ImageModel: &ModelConfig{
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
				Model: &ModelConfig{
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

func TestResolveImageModelInheritsConfiguredPrimaryModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Model = &ModelConfig{
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
	cfg.ImageModel = &ModelConfig{
		Model: "claude-vision",
	}

	got, err := cfg.ResolveImageModel("")
	if err != nil {
		t.Fatalf("ResolveImageModel() error = %v", err)
	}
	if got.Provider != "openai" {
		t.Fatalf("ResolveImageModel() provider = %q, want %q", got.Provider, "openai")
	}
	if got.Key != "default-key" {
		t.Fatalf("ResolveImageModel() key = %q, want %q", got.Key, "default-key")
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
		ImageModel: &ModelConfig{
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

func TestImageModelAllowsNilAndDefaultConfigProvidesInitTemplate(t *testing.T) {
	cfg := &Config{
		Model: &ModelConfig{Provider: "openai", Model: "text-only"},
	}
	if _, err := cfg.ResolveImageModel(""); err == nil {
		t.Fatal("nil image model unexpectedly resolved for a text-only primary model")
	}

	defaults := DefaultConfig()
	if defaults.ImageModel == nil {
		t.Fatal("DefaultConfig image model template is nil")
	}
	if defaults.ImageModel.IsConfigured() {
		t.Fatalf("default image model template unexpectedly active: %#v", defaults.ImageModel)
	}
	if defaults.ImageModel.ContextWindow == 0 || defaults.ImageModel.MaxTokens == 0 || defaults.ImageModel.QPM == 0 {
		t.Fatalf("default image model template lacks tuning defaults: %#v", defaults.ImageModel)
	}
}

func TestLoadHonorsNullImageModel(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"image_model":null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImageModel != nil {
		t.Fatalf("ImageModel = %#v, want nil", cfg.ImageModel)
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
