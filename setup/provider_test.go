package setup

import (
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
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

func TestCreateProviderClientSupportsOpenAIResponsesAlias(t *testing.T) {
	if _, err := CreateProviderClientFromModel(config.ModelConfig{
		Provider: "openai-responses",
		BaseURL:  "https://example.com/v1",
		Model:    "gpt-test",
	}); err != nil {
		t.Fatal(err)
	}
}
