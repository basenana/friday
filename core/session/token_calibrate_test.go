package session

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

func TestCalibrateAndBackfill_SkipsWhenPromptTokensZero(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello world"},
	))
	req := providers.NewRequest("", sess.GetHistory()...)

	CalibrateAndBackfill(sess, req, 0)

	if sess.tokenCalibration.CalibrationSamples != 0 {
		t.Fatalf("expected no calibration samples, got %d", sess.tokenCalibration.CalibrationSamples)
	}
}

func TestCalibrateAndBackfill_SkipsWhenHistoryEmpty(t *testing.T) {
	sess := New("test", nil)
	req := providers.NewRequest("")

	CalibrateAndBackfill(sess, req, 500)

	if sess.tokenCalibration.CalibrationSamples != 0 {
		t.Fatalf("expected no calibration samples, got %d", sess.tokenCalibration.CalibrationSamples)
	}
}

func TestCalibrateAndBackfill_BackfillsMatchedMessageTokens(t *testing.T) {
	msg1 := types.Message{Role: types.RoleUser, Content: "hello world"}
	msg2 := types.Message{Role: types.RoleUser, Content: "how are you doing today"}
	sess := New("test", nil, WithHistory(msg1, msg2))
	req := providers.NewRequest("", sess.GetHistory()...)

	CalibrateAndBackfill(sess, req, 100)

	history := sess.GetHistory()
	if history[0].Tokens == 0 || history[1].Tokens == 0 {
		t.Fatalf("expected Tokens to be backfilled, got %d and %d", history[0].Tokens, history[1].Tokens)
	}

	var totalTokens int64
	for _, msg := range history {
		totalTokens += msg.Tokens
	}
	if totalTokens != 100 {
		t.Fatalf("expected total backfilled tokens=100, got %d", totalTokens)
	}
}

func TestCalibrateAndBackfill_DoesNotDoubleCountExistingTokens(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "first turn", Tokens: 40},
		types.Message{Role: types.RoleAssistant, Content: "first answer", Tokens: 20},
		types.Message{Role: types.RoleUser, Content: "second turn"},
	))
	req := providers.NewRequest("", sess.GetHistory()...)

	CalibrateAndBackfill(sess, req, 90)

	history := sess.GetHistory()
	if history[0].Tokens != 40 || history[1].Tokens != 20 {
		t.Fatalf("expected existing token counts to remain unchanged, got %d and %d", history[0].Tokens, history[1].Tokens)
	}
	if history[2].Tokens != 30 {
		t.Fatalf("expected remaining prompt tokens to be assigned to the new message, got %d", history[2].Tokens)
	}
}

func TestCalibrateAndBackfill_SubtractsRequestOverhead(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello world"},
	))
	req := providers.NewRequest("", sess.GetHistory()...)
	req.SetSystemPrompt("system prompt with instructions")
	req.SetToolDefines([]providers.ToolDefine{
		providers.NewToolDefine(
			"read_file",
			"Read a file from the workspace",
			map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		),
	})

	overhead := EstimateRequestOverhead(req)
	if overhead <= 0 {
		t.Fatal("expected request overhead estimate to be positive")
	}

	CalibrateAndBackfill(sess, req, 200)

	history := sess.GetHistory()
	if history[0].Tokens <= 0 {
		t.Fatalf("expected backfilled user tokens, got %d", history[0].Tokens)
	}
	if history[0].Tokens >= 200 {
		t.Fatalf("expected overhead to stay out of history tokens, got %d", history[0].Tokens)
	}
}

func TestCalibrateAndBackfill_UpdatesCalibrationFactor(t *testing.T) {
	msg := types.Message{Role: types.RoleUser, Content: "hello world"}
	sess := New("test", nil, WithHistory(msg))
	req := providers.NewRequest("", sess.GetHistory()...)

	CalibrateAndBackfill(sess, req, 100)

	if sess.tokenCalibration.CalibrationSamples != 1 {
		t.Fatalf("expected 1 calibration sample, got %d", sess.tokenCalibration.CalibrationSamples)
	}
	if sess.tokenCalibration.LastActualPromptTokens != 100 {
		t.Fatalf("expected LastActualPromptTokens=100, got %d", sess.tokenCalibration.LastActualPromptTokens)
	}
	if sess.tokenCalibration.CalibrationFactor <= 0 {
		t.Fatalf("expected positive CalibrationFactor, got %f", sess.tokenCalibration.CalibrationFactor)
	}
}

func TestCalibrateAndBackfill_SlidingAverageCapsAtMax(t *testing.T) {
	msg := types.Message{Role: types.RoleUser, Content: "hello"}
	sess := New("test", nil, WithHistory(msg))
	req := providers.NewRequest("", sess.GetHistory()...)

	for i := 0; i < MaxCalibrationSamples+5; i++ {
		CalibrateAndBackfill(sess, req, 50)
	}

	if sess.tokenCalibration.CalibrationSamples != MaxCalibrationSamples {
		t.Fatalf("expected CalibrationSamples capped at %d, got %d", MaxCalibrationSamples, sess.tokenCalibration.CalibrationSamples)
	}
}

func TestCalibratedTokenCount_UsesBackfilledTokensAndFactor(t *testing.T) {
	msg1 := types.Message{Role: types.RoleUser, Content: "hello", Tokens: 10}
	msg2 := types.Message{Role: types.RoleUser, Content: "world"}

	total := CalibratedTokenCount([]types.Message{msg1, msg2}, 1.5)
	expectedFromMsg2 := int64(float64(msg2.EstimatedTokens()) * 1.5)
	if total != 10+expectedFromMsg2 {
		t.Fatalf("expected total=%d, got %d", 10+expectedFromMsg2, total)
	}
}

func TestCountTokens_UsesSessionCalibrationFactor(t *testing.T) {
	sess := New("test", nil)
	sess.tokenCalibration.CalibrationFactor = 2

	msg := types.Message{Role: types.RoleUser, Content: "hello"}
	if got, want := sess.CountTokens([]types.Message{msg}), int64(msg.EstimatedTokens())*2; got != want {
		t.Fatalf("expected CountTokens=%d, got %d", want, got)
	}
}

func TestTokens_UsesCheckpointWhenAvailable(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello"},
	))

	// Simulate a checkpoint from an LLM response
	ctxState := sess.EnsureContextState()
	ctxState.TokenCheckpoint = TokenCheckpoint{
		Index:        1, // len(History) was 1 when checkpoint was recorded
		PromptTokens: 500,
	}

	// No new messages → should return exact checkpoint value
	if got := sess.Tokens(); got != 500 {
		t.Fatalf("expected Tokens=500 (checkpoint only), got %d", got)
	}
}

func TestTokens_CheckpointWithNewMessages(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello"},
	))

	// Set checkpoint at index 1
	ctxState := sess.EnsureContextState()
	ctxState.TokenCheckpoint = TokenCheckpoint{
		Index:        1,
		PromptTokens: 500,
	}

	// Add new messages after checkpoint
	newMsg := types.Message{Role: types.RoleAssistant, Content: "world response here"}
	sess.AppendMessage(&newMsg)

	tokens := sess.Tokens()
	expectedNew := newMsg.EstimatedTokens()
	if tokens != 500+expectedNew {
		t.Fatalf("expected Tokens=%d (500 + %d), got %d", 500+expectedNew, expectedNew, tokens)
	}
}

func TestTokens_FallsBackToCalibratedWhenNoCheckpoint(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello world"},
	))

	// No checkpoint set → should use CalibratedTokenCount
	tokens := sess.Tokens()
	if tokens <= 0 {
		t.Fatalf("expected positive tokens from CalibratedTokenCount, got %d", tokens)
	}

	// Should match CalibratedTokenCount exactly
	factor := sess.CalibrationFactor()
	history := sess.GetHistory()
	if tokens != CalibratedTokenCount(history, factor) {
		t.Fatalf("expected fallback to match CalibratedTokenCount")
	}
}

func TestCompactHistory_ResetsTokenCheckpoint(t *testing.T) {
	sess := New("test", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: "hello"},
		types.Message{Role: types.RoleAssistant, Content: "hi there"},
		types.Message{Role: types.RoleUser, Content: "how are you"},
		types.Message{Role: types.RoleAssistant, Content: "I'm fine"},
	))

	// Set a checkpoint
	ctxState := sess.EnsureContextState()
	ctxState.TokenCheckpoint = TokenCheckpoint{
		Index:        2,
		PromptTokens: 300,
	}

	// Compact without LLM (truncation fallback)
	if err := sess.CompactHistory(context.Background()); err != nil {
		t.Fatalf("CompactHistory error: %v", err)
	}

	// Checkpoint should be reset
	ctxState = sess.EnsureContextState()
	if ctxState.TokenCheckpoint.PromptTokens != 0 || ctxState.TokenCheckpoint.Index != 0 {
		t.Fatalf("expected checkpoint reset after compaction, got PromptTokens=%d Index=%d",
			ctxState.TokenCheckpoint.PromptTokens, ctxState.TokenCheckpoint.Index)
	}
}
