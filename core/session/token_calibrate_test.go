package session

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestEstimateHistoryTokens_UsesBackfilledTokens(t *testing.T) {
	msg1 := types.Message{Role: types.RoleUser, Content: "hello", Tokens: 10}
	msg2 := types.Message{Role: types.RoleUser, Content: "world"}

	total := EstimateHistoryTokens([]types.Message{msg1, msg2})
	expectedFromMsg2 := msg2.EstimatedTokens()
	if total != 10+expectedFromMsg2 {
		t.Fatalf("expected total=%d, got %d", 10+expectedFromMsg2, total)
	}
}

func TestEstimatedTokens_UsesReasoningFields(t *testing.T) {
	msg := types.Message{
		Role:               types.RoleAssistant,
		Content:            "done",
		Reasoning:          "abc",
		ReasoningSignature: "sig",
		RedactedThinking:   "opaque",
	}

	// len("doneabcsigopaque") * 0.5 = 8
	if got := msg.EstimatedTokens(); got != 8 {
		t.Fatalf("expected EstimatedTokens=8, got %d", got)
	}
}

func TestTokens_UsesCheckpointWhenAvailable(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello"},
	))

	ctxState := sess.EnsureContextState()
	ctxState.TokenCheckpoint = TokenCheckpoint{
		Index:        1,
		PromptTokens: 500,
	}

	if got := sess.Tokens(); got != 500 {
		t.Fatalf("expected Tokens=500 (checkpoint only), got %d", got)
	}
}

func TestTokens_CheckpointWithNewMessages(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello"},
	))

	ctxState := sess.EnsureContextState()
	ctxState.TokenCheckpoint = TokenCheckpoint{
		Index:        1,
		PromptTokens: 500,
	}

	newMsg := types.Message{Role: types.RoleAssistant, Content: "world response here"}
	sess.AppendMessage(&newMsg)

	tokens := sess.Tokens()
	expectedNew := newMsg.EstimatedTokens()
	if tokens != 500+expectedNew {
		t.Fatalf("expected Tokens=%d (500 + %d), got %d", 500+expectedNew, expectedNew, tokens)
	}
}

func TestTokens_FallsBackToEstimationWhenNoCheckpoint(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello world"},
	))

	tokens := sess.Tokens()
	if tokens <= 0 {
		t.Fatalf("expected positive tokens from EstimateHistoryTokens, got %d", tokens)
	}

	history := sess.GetHistory()
	if tokens != EstimateHistoryTokens(history) {
		t.Fatalf("expected fallback to match EstimateHistoryTokens")
	}
}

func TestCompactHistory_ResetsTokenCheckpoint(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello"},
		types.Message{Role: types.RoleAssistant, Content: "hi there"},
		types.Message{Role: types.RoleUser, Content: "how are you"},
		types.Message{Role: types.RoleAssistant, Content: "I'm fine"},
	))

	ctxState := sess.EnsureContextState()
	ctxState.TokenCheckpoint = TokenCheckpoint{
		Index:        2,
		PromptTokens: 300,
	}

	if err := sess.CompactHistory(context.Background()); err != nil {
		t.Fatalf("CompactHistory error: %v", err)
	}

	ctxState = sess.EnsureContextState()
	if ctxState.TokenCheckpoint.PromptTokens != 0 || ctxState.TokenCheckpoint.Index != 0 {
		t.Fatalf("expected checkpoint reset after compaction, got PromptTokens=%d Index=%d",
			ctxState.TokenCheckpoint.PromptTokens, ctxState.TokenCheckpoint.Index)
	}
}
