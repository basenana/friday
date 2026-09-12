package actor

import (
	"testing"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
)

func TestTranslatorPreservesCompactStatistics(t *testing.T) {
	data := map[string]string{
		"method": "summary", "trigger": "manual", "status": "ok",
		"tokens_before": "12000", "tokens_after": "4000", "saved_tokens": "8000", "duration_ms": "250",
	}
	out := NewTranslator("run-compact").FromCoreEvent(types.Event{Type: types.EventCompactFinish, Data: data})
	if len(out) != 1 || out[0].Type != events.KindCustom || out[0].Name != events.CustomCompactFinish {
		t.Fatalf("unexpected translated events: %#v", out)
	}
	var payload events.CustomData
	if err := events.DecodePayload(out[0], &payload); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	for key, want := range data {
		if got := payload.Body[key]; got != want {
			t.Fatalf("payload[%q] = %v, want %q", key, got, want)
		}
	}
}
