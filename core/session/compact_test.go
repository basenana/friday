package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

func TestExtractFileRefsFromHistory(t *testing.T) {
	now := time.Now()
	history := []types.Message{
		{Role: types.RoleUser, Content: "Please refactor core/session/compact.go.", Time: now},
		{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`}}, Time: now},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "1", Content: "file content"}, Time: now},
	}

	refs := extractFileRefsFromHistory(history)
	if len(refs) == 0 {
		t.Fatalf("expected file refs to be extracted")
	}
	if refs[0].Path != "core/session/compact.go" {
		t.Fatalf("expected compact.go path, got %q", refs[0].Path)
	}
}

func TestExtractFileRefsDedupes(t *testing.T) {
	text := "edit core/session/compact.go and then core/session/compact.go again"
	refs := ExtractFileRefs(text, "test", time.Now())
	if len(refs) != 1 {
		t.Fatalf("expected 1 deduplicated ref, got %d", len(refs))
	}
}

func TestBuildCompactSummaryHistoryStartsWithSummary(t *testing.T) {
	summary := "This is a compact summary of the conversation."

	// Create 10 messages; tail=6 (compactTailMessages)
	history := make([]types.Message, 10)
	for i := range history {
		history[i] = types.Message{Role: types.RoleUser, Content: fmt.Sprintf("message-%d", i)}
	}

	compacted := BuildCompactSummaryHistory(history, summary)

	// Compact summary message should be first
	if len(compacted) < 1 || !strings.Contains(compacted[0].Content, summary) {
		t.Fatalf("expected compact summary message as first element, got %q", compacted[0].Content)
	}
	if !strings.HasPrefix(compacted[0].Content, "Several lengthy dialogues") {
		t.Fatalf("expected summary to start with summaryPrefix, got %q", compacted[0].Content)
	}

	// Tail messages should be the last 6 (indices 4-9)
	if len(compacted) != 7 {
		t.Fatalf("expected 7 elements after compaction, got %d", len(compacted))
	}
	tailStart := compacted[1]
	if tailStart.Content != "message-4" {
		t.Errorf("expected tail to start with message-4, got %q", tailStart.Content)
	}
}

func TestSummarizeToCompactSummaryUsesLLM(t *testing.T) {
	llm := &fakeCompactClient{
		completionChunks: []string{"This is the LLM-generated summary."},
	}

	history := []types.Message{
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there"},
	}

	summary, err := SummarizeToCompactSummary(context.Background(), llm, history)
	if err != nil {
		t.Fatalf("SummarizeToCompactSummary() error = %v", err)
	}
	if summary != "This is the LLM-generated summary." {
		t.Fatalf("expected LLM summary, got %q", summary)
	}
}

func TestSummarizeToCompactSummaryReturnsEmptyOnLLMEmpty(t *testing.T) {
	llm := &fakeCompactClient{
		completionChunks: []string{""}, // Empty response
	}

	history := []types.Message{
		{Role: types.RoleUser, Content: "Please test the fallback."},
		{Role: types.RoleAssistant, Content: "Working on it."},
	}

	summary, err := SummarizeToCompactSummary(context.Background(), llm, history)
	if err != nil {
		t.Fatalf("SummarizeToCompactSummary() error = %v", err)
	}
	if summary != "" {
		t.Fatalf("expected empty summary when LLM returns empty, got %q", summary)
	}
}

func TestSummarizeToCompactSummaryNilLLM(t *testing.T) {
	history := []types.Message{
		{Role: types.RoleUser, Content: "Test nil LLM fallback."},
	}

	summary, err := SummarizeToCompactSummary(context.Background(), nil, history)
	if err != nil {
		t.Fatalf("SummarizeToCompactSummary() error = %v", err)
	}
	if summary != "" {
		t.Fatalf("expected empty summary when LLM is nil, got %q", summary)
	}
}

// Mock types

type fakeCompactClient struct {
	completionChunks []string
	completionErr    error
	completionCalls  int
}

func (f *fakeCompactClient) Completion(ctx context.Context, _ providers.Request) providers.Response {
	f.completionCalls++
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		for _, chunk := range f.completionChunks {
			select {
			case <-ctx.Done():
				resp.Err <- ctx.Err()
				return
			case resp.Stream <- providers.Delta{Content: chunk}:
			}
		}
		if f.completionErr != nil {
			resp.Err <- f.completionErr
		}
	}()
	return resp
}

func (f *fakeCompactClient) CompletionNonStreaming(_ context.Context, _ providers.Request) (string, error) {
	return "", errors.New("unexpected CompletionNonStreaming call")
}

func (f *fakeCompactClient) StructuredPredict(_ context.Context, _ providers.Request, _ any) error {
	return errors.New("structured predict not supported")
}
