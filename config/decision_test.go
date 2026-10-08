package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultDecisionModelConfigIsInactiveTemplate(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DecisionModel == nil {
		t.Fatal("decision model template is nil")
	}
	if cfg.DecisionModel.IsConfigured() {
		t.Fatalf("decision model template unexpectedly configured: %#v", cfg.DecisionModel)
	}
	if cfg.DecisionModel.EffectiveProvider() != "jev" || cfg.DecisionModel.EffectiveBaseURL() != "https://openrouter.ai/api/v1" || cfg.DecisionModel.QPM != 20 {
		t.Fatalf("decision model defaults = %#v", cfg.DecisionModel)
	}
}

func TestLoadNormalizesUnconfiguredDecisionModelToNil(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	tests := []struct {
		name     string
		filename string
		data     string
	}{
		{name: "json omitted", filename: "config.json", data: `{}`},
		{name: "json null", filename: "config.json", data: `{"decision_model":null}`},
		{name: "json empty model", filename: "config.json", data: `{"decision_model":{"model":""}}`},
		{name: "yaml omitted", filename: "friday.yaml", data: `{}`},
		{name: "yaml null", filename: "friday.yaml", data: "decision_model: null\n"},
		{name: "expanded empty", filename: "friday.yaml", data: "decision_model:\n  model: $FRIDAY_MISSING_DECISION_MODEL\n"},
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
			if cfg.DecisionModel != nil {
				t.Fatalf("DecisionModel = %#v, want nil", cfg.DecisionModel)
			}
		})
	}
}

func TestLoadDecisionModelInheritsDefaultsAndExpandsEnvironment(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	t.Setenv("FRIDAY_DECISION_KEY", "decision-key")
	t.Setenv("FRIDAY_DECISION_BASE", "https://decision.example/v1")
	t.Setenv("FRIDAY_DECISION_MODEL", "typesafe/jev-test")
	t.Setenv("FRIDAY_DECISION_PROXY", "http://proxy.example:8080")
	path := filepath.Join(t.TempDir(), "friday.yaml")
	data := `decision_model:
  key: $FRIDAY_DECISION_KEY
  base_url: $FRIDAY_DECISION_BASE
  model: $FRIDAY_DECISION_MODEL
  proxy: $FRIDAY_DECISION_PROXY
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := DecisionModelConfig{
		Provider: "jev",
		BaseURL:  "https://decision.example/v1",
		Key:      "decision-key",
		Model:    "typesafe/jev-test",
		QPM:      20,
		Proxy:    "http://proxy.example:8080",
	}
	if cfg.DecisionModel == nil || !reflect.DeepEqual(*cfg.DecisionModel, want) {
		t.Fatalf("DecisionModel = %#v, want %#v", cfg.DecisionModel, want)
	}
}

func TestDecisionModelStaysOutOfChatCatalog(t *testing.T) {
	cfg := &Config{
		Model:         &ModelConfig{Model: "chat-model"},
		DecisionModel: &DecisionModelConfig{Model: "decision-model"},
	}
	if got := cfg.ModelNames(); !reflect.DeepEqual(got, []string{"chat-model"}) {
		t.Fatalf("ModelNames() = %#v", got)
	}
}
