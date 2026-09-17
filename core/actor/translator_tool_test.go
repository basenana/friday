package actor

import (
	"testing"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
)

func TestTranslatorPreservesStructuredToolTimeout(t *testing.T) {
	out := NewTranslator("run-timeout").FromCoreEvent(types.Event{
		Type: types.EventToolFinish,
		Data: map[string]string{
			"id": "call-timeout", "tool": "bash", "success": "false", "output": "timed out",
			"status": "timed_out", "error_code": "tool_timeout", "timeout_kind": "declared",
			"timeout_ms": "250", "elapsed_ms": "253",
		},
	})
	if len(out) != 2 || out[0].Type != events.KindToolCallResult {
		t.Fatalf("translated events = %#v", out)
	}
	var result events.ToolCallResultData
	if err := events.DecodePayload(out[0], &result); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if result.ToolCallID != "call-timeout" || result.Success || result.Status != "timed_out" ||
		result.ErrorCode != "tool_timeout" || result.TimeoutKind != "declared" ||
		result.TimeoutMs != 250 || result.ElapsedMs != 253 {
		t.Fatalf("tool result = %+v", result)
	}
}
