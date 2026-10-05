package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
)

func TestCreateProviderClientSupportsOpenAIResponse(t *testing.T) {
	client, err := CreateProviderClientFromModel(config.ModelConfig{
		Provider:      "openai-response",
		BaseURL:       "https://example.com/v1",
		Key:           "test-key",
		Model:         "gpt-test",
		ContextWindow: 128000,
		MaxTokens:     4096,
		QPM:           60,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelProvider, ok := client.(providers.ModelNameProvider)
	if !ok || modelProvider.ModelName() != "gpt-test" {
		t.Fatalf("unexpected Responses client: %#v", client)
	}
}

func TestCreateModelPoolCarriesRuntimeMetadata(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Model.Provider = "openai"
	cfg.Model.BaseURL = "https://example.test/v1"
	cfg.Model.Model = "gpt-test"
	cfg.Model.ReasoningEffort = providers.ReasoningEffortHigh

	pool, err := CreateModelPool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := pool.NewClient(fallback.NewSessionPolicy(providers.ClientPolicy{}), providers.ClientPolicy{})
	info, ok := providers.RuntimeInfo(client)
	if !ok || info.Model != "gpt-test" || info.EndpointKey == "" || info.Effort != providers.ReasoningEffortHigh || info.Actual {
		t.Fatalf("runtime metadata = %+v, ok=%v", info, ok)
	}
}

func TestCreateProviderClientSupportsOpenAIResponsesAlias(t *testing.T) {
	if _, err := CreateProviderClientFromModel(config.ModelConfig{
		Provider: "openai-responses",
		BaseURL:  "https://example.com/v1",
		Model:    "gpt-test",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDecisionProviderRequiresConfiguredModel(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {}, {DecisionModel: &config.DecisionModelConfig{}}} {
		if _, err := CreateDecisionProvider(cfg); err == nil || !strings.Contains(err.Error(), "decision model is not configured") {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestCreateDecisionProviderRejectsUnknownProvider(t *testing.T) {
	_, err := CreateDecisionProviderFromModel(config.DecisionModelConfig{Provider: "other", Model: "judge"})
	if err == nil || !strings.Contains(err.Error(), "unknown decision provider: other") {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateDecisionProviderEvaluatesThroughOpenRouterAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/systemone" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer decision-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"decision-1","model":"jev-test","provider":"TypeSafe","answers":{"safe":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":2,"cost":0.001}}`))
	}))
	defer server.Close()

	provider, err := CreateDecisionProvider(&config.Config{DecisionModel: &config.DecisionModelConfig{
		Provider: "openrouter",
		BaseURL:  server.URL + "/api/v1/",
		Key:      "decision-key",
		Model:    "jev-test",
		QPM:      60000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Evaluate(context.Background(), providers.DecisionRequest{
		State: "artifact",
		Questions: map[string]providers.DecisionQuestion{
			"safe": providers.NoulQuestion{Instructions: "Is it safe?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "decision-1" || response.Answers["safe"].(providers.NoulAnswer).Noul != 0.9 || response.Usage.Cost != 0.001 {
		t.Fatalf("response = %+v", response)
	}
}

func TestCreateModelPoolIgnoresDecisionModel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DecisionModel = &config.DecisionModelConfig{Provider: "unsupported", Model: "judge"}
	if _, err := CreateModelPool(cfg); err != nil {
		t.Fatal(err)
	}
}
