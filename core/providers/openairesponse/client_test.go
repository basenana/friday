package openairesponse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

func TestResponseNewParamsUsesResponsesWireFormat(t *testing.T) {
	temp := 0.4
	cli := &client{model: Model{
		Name:            "gpt-test",
		Temperature:     &temp,
		MaxTokens:       2048,
		ReasoningEffort: "high",
	}}
	req := providers.NewRequest("system prompt",
		types.Message{
			Role:    types.RoleUser,
			Content: "inspect this",
			Image: &types.ImageContent{
				Type: types.ImageTypeURL,
				URL:  "https://example.com/image.png",
			},
		},
		types.Message{
			Role:               types.RoleAssistant,
			ReasoningSignature: `{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"opaque","status":"completed"}`,
			ToolCalls: []types.ToolCall{{
				ID: "call_1", Name: "lookup", Arguments: `{"query":"friday"}`,
			}},
		},
		types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call_1", Content: "result"}},
	)
	req.SetPromptCacheKey("session-1")
	req.SetToolDefines([]providers.ToolDefine{
		providers.NewToolDefine("zeta", "z", map[string]any{"type": "object"}),
		providers.NewToolDefine("alpha", "a", map[string]any{"type": "object"}),
	})

	raw, err := json.Marshal(cli.responseNewParams(req))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}

	if body["model"] != "gpt-test" || body["instructions"] != "system prompt" {
		t.Fatalf("unexpected top-level Responses fields: %s", raw)
	}
	if body["store"] != false || body["max_output_tokens"] != float64(2048) {
		t.Fatalf("expected stateless response and max_output_tokens: %s", raw)
	}
	if body["prompt_cache_key"] != "session-1" {
		t.Fatalf("prompt cache key missing: %s", raw)
	}
	if _, exists := body["messages"]; exists {
		t.Fatalf("chat-completions messages leaked into Responses request: %s", raw)
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("expected user, reasoning, function_call, and function_call_output items: %s", raw)
	}
	assertItemType(t, input[1], "reasoning")
	assertItemType(t, input[2], "function_call")
	assertItemType(t, input[3], "function_call_output")
	tools := body["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "alpha" || tools[1].(map[string]any)["name"] != "zeta" {
		t.Fatalf("tools were not sorted: %#v", tools)
	}
}

func assertItemType(t *testing.T, raw any, want string) {
	t.Helper()
	item, ok := raw.(map[string]any)
	if !ok || item["type"] != want {
		t.Fatalf("item = %#v, want type %q", raw, want)
	}
}

func TestCompletionTranslatesResponsesSSE(t *testing.T) {
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		requestBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\",\"item_id\":\"rs_1\",\"output_index\":0,\"summary_index\":0,\"sequence_number\":1}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"item_id\":\"msg_1\",\"output_index\":1,\"content_index\":0,\"sequence_number\":2}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":2,\"sequence_number\":3,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"{\\\"query\\\":\\\"friday\\\"}\",\"status\":\"completed\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"sequence_number\":4,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"opaque\",\"status\":\"completed\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":12,\"output_tokens\":5,\"total_tokens\":17,\"input_tokens_details\":{\"cached_tokens\":7},\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cli := New(server.URL+"/", "test-key", Model{Name: "gpt-test", QPM: 60})
	resp := cli.Completion(context.Background(), providers.NewPromptRequest("hello"))

	var deltas []providers.Delta
	for delta := range resp.Message() {
		deltas = append(deltas, delta)
	}
	for err := range resp.Error() {
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(deltas) != 4 {
		t.Fatalf("deltas = %#v, want reasoning, content, tool call, and reasoning signature", deltas)
	}
	if deltas[0].Reasoning != "thinking" || deltas[1].Content != "hello" {
		t.Fatalf("unexpected text deltas: %#v", deltas)
	}
	if len(deltas[2].ToolUse) != 1 || deltas[2].ToolUse[0].ID != "call_1" || deltas[2].ToolUse[0].Name != "lookup" {
		t.Fatalf("unexpected tool delta: %#v", deltas[2])
	}
	if deltas[3].ReasoningSignature == "" {
		t.Fatalf("reasoning item was not preserved: %#v", deltas[3])
	}
	tokens := resp.Tokens()
	if tokens.PromptTokens != 12 || tokens.CompletionTokens != 5 || tokens.CachedPromptTokens != 7 || tokens.TotalTokens != 17 {
		t.Fatalf("unexpected usage: %#v", tokens)
	}

	var sent map[string]any
	if err := json.Unmarshal(requestBody, &sent); err != nil {
		t.Fatal(err)
	}
	if _, ok := sent["input"]; !ok {
		t.Fatalf("request did not use Responses input: %s", requestBody)
	}
}

func TestCompletionNonStreamingWaitsForStreamingResponse(t *testing.T) {
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cli := New(server.URL+"/", "test-key", Model{Name: "gpt-test", QPM: 60})
	got, err := cli.CompletionNonStreaming(context.Background(), providers.NewPromptRequest("hello"))
	if err != nil || got != "hello world" {
		t.Fatalf("CompletionNonStreaming() = %q, %v", got, err)
	}

	var sent map[string]any
	if err := json.Unmarshal(requestBody, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["stream"] != true {
		t.Fatalf("CompletionNonStreaming request did not enable streaming: %s", requestBody)
	}
}

func TestStructuredPredictWaitsForStreamingResponse(t *testing.T) {
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"answer\\\":\\\"ok\\\"}\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cli := New(server.URL+"/", "test-key", Model{Name: "gpt-test", QPM: 60})
	var got struct {
		Answer string `json:"answer"`
	}
	if err := cli.StructuredPredict(context.Background(), providers.NewPromptRequest("answer with ok"), &got); err != nil {
		t.Fatal(err)
	}
	if got.Answer != "ok" {
		t.Fatalf("StructuredPredict() = %#v", got)
	}

	var sent map[string]any
	if err := json.Unmarshal(requestBody, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["stream"] != true {
		t.Fatalf("StructuredPredict request did not enable streaming: %s", requestBody)
	}
}
