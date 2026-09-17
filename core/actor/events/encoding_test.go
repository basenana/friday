package events

import (
	"reflect"
	"testing"
)

func TestDecodeInputAcceptedIgnoresHistoricalDeliveryField(t *testing.T) {
	event := NewEvent(KindCustom, "turn-1").
		WithName(CustomInputAccepted).
		WithPayload(map[string]any{
			"turn_id":      "turn-1",
			"text":         "model prompt",
			"display_text": "visible prompt",
			"delivery":     "steer",
			"sources":      []string{"user.local", "loop"},
		})

	var body InputAcceptedBody
	if err := DecodePayload(event, &body); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if body.TurnID != "turn-1" || body.Text != "model prompt" || body.DisplayText != "visible prompt" {
		t.Fatalf("decoded body = %+v", body)
	}
	if want := []string{"user.local", "loop"}; !reflect.DeepEqual(body.Sources, want) {
		t.Fatalf("sources = %v, want %v", body.Sources, want)
	}
}
