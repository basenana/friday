package setup

import (
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
