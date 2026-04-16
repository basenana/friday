package common

import (
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestApplyTokenFallbackWithOverhead(t *testing.T) {
	messages := []types.Message{
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
	}

	var msgTokens int64
	for _, msg := range messages {
		msgTokens += msg.FuzzyTokens()
	}

	tests := []struct {
		name             string
		promptTokens     int64
		completionTokens int64
		accumulated      string
		overhead         int64
		wantPromptGT     int64 // expected: prompt > this value
		wantPromptLT     int64 // expected: prompt < this value (optional)
		wantCompletionGT int64
	}{
		{
			name:             "zero API tokens uses messages plus overhead",
			promptTokens:     0,
			completionTokens: 0,
			accumulated:      "Sure!",
			overhead:         500,
			wantPromptGT:     msgTokens + 500 - 1, // strictly greater than msgTokens + 500 - 1
			wantCompletionGT: 0,
		},
		{
			name:             "non-zero API tokens ignores overhead",
			promptTokens:     1000,
			completionTokens: 50,
			accumulated:      "Sure!",
			overhead:         500,
			wantPromptGT:     999,
			wantPromptLT:     1001,
			wantCompletionGT: 49,
		},
		{
			name:             "zero overhead still counts messages",
			promptTokens:     0,
			completionTokens: 0,
			accumulated:      "test",
			overhead:         0,
			wantPromptGT:     msgTokens - 1,
			wantCompletionGT: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, completion, total := ApplyTokenFallback(
				tt.promptTokens, tt.completionTokens, tt.accumulated, messages, tt.overhead,
			)
			if prompt <= tt.wantPromptGT {
				t.Errorf("prompt tokens = %d, want > %d", prompt, tt.wantPromptGT)
			}
			if tt.wantPromptLT > 0 && prompt >= tt.wantPromptLT {
				t.Errorf("prompt tokens = %d, want < %d", prompt, tt.wantPromptLT)
			}
			if completion <= tt.wantCompletionGT {
				t.Errorf("completion tokens = %d, want > %d", completion, tt.wantCompletionGT)
			}
			if total != prompt+completion {
				t.Errorf("total = %d, want %d (prompt+completion)", total, prompt+completion)
			}
		})
	}
}

func TestApplyTokenFallbackOverheadOnlyCountedWhenAPIReturnsZero(t *testing.T) {
	messages := []types.Message{
		{Role: types.RoleUser, Content: "Hello world"},
	}

	overhead := int64(500)

	// When API returns prompt tokens, overhead should be ignored
	prompt1, _, _ := ApplyTokenFallback(800, 10, "ok", messages, overhead)
	if prompt1 != 800 {
		t.Errorf("with API tokens, expected prompt=800, got %d", prompt1)
	}

	// When API returns zero, overhead should be included
	prompt2, _, _ := ApplyTokenFallback(0, 10, "ok", messages, overhead)
	msgTokens := messages[0].FuzzyTokens()
	if prompt2 <= msgTokens {
		t.Errorf("without API tokens, expected prompt > %d (msg only), got %d", msgTokens, prompt2)
	}
	if prompt2 != msgTokens+overhead {
		t.Errorf("expected prompt = %d (msg+overhead), got %d", msgTokens+overhead, prompt2)
	}
}
