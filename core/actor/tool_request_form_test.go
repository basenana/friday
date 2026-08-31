package actor

import (
	"context"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/cards"
	"github.com/basenana/friday/core/actor/events"
)

func TestNormalizeFormOptions(t *testing.T) {
	s := cards.FormSchema{
		Title:       "Pick one",
		Description: "desc",
		Fields: []cards.Field{
			{Name: "organism", Type: cards.FieldSelect, Options: []cards.Option{
				{Label: "Human", Description: "homo sapiens"},
				{Label: "Mouse", Value: ""},
				{Label: "Fly", Value: "dmel"},
			}},
			{Name: "tags", Type: cards.FieldMultiSelect, Options: []cards.Option{
				{Label: "Alpha"},
				{Label: "Beta", Value: "beta"},
			}},
			{Name: "note", Type: cards.FieldText},
		},
	}

	got := normalizeFormOptions(s)

	if got.Fields[0].Options[0].Value != "Human" {
		t.Fatalf("missing value should fall back to label, got %v", got.Fields[0].Options[0].Value)
	}
	if got.Fields[0].Options[1].Value != "Mouse" {
		t.Fatalf("blank value should fall back to label, got %v", got.Fields[0].Options[1].Value)
	}
	if got.Fields[0].Options[2].Value != "dmel" {
		t.Fatalf("explicit value must be preserved, got %v", got.Fields[0].Options[2].Value)
	}
	if got.Fields[0].Options[0].Description != "homo sapiens" {
		t.Fatalf("option description must be preserved, got %v", got.Fields[0].Options[0].Description)
	}
	if got.Fields[1].Options[0].Value != "Alpha" {
		t.Fatalf("multiselect missing value should fall back to label, got %v", got.Fields[1].Options[0].Value)
	}
	if got.Fields[1].Options[1].Value != "beta" {
		t.Fatalf("multiselect explicit value must be preserved, got %v", got.Fields[1].Options[1].Value)
	}
	if got.Fields[2].Type != cards.FieldText {
		t.Fatalf("non-choice fields must be untouched")
	}
}

func TestFormSchemaToMapPreservesDescriptionAndOptionValues(t *testing.T) {
	s := cards.FormSchema{
		Title:       "T",
		Description: "D",
		Fields: []cards.Field{
			{Name: "organism", Type: cards.FieldSelect, Options: []cards.Option{
				{Label: "Human", Value: "hsapiens", Description: "homo sapiens"},
			}},
		},
	}

	m, err := formSchemaToMap(s)
	if err != nil {
		t.Fatalf("formSchemaToMap: %v", err)
	}
	if m["description"] != "D" {
		t.Fatalf("schema description lost: %v", m)
	}

	fields, ok := m["fields"].([]any)
	if !ok || len(fields) == 0 {
		t.Fatalf("fields missing from marshaled schema: %v", m)
	}
	field, ok := fields[0].(map[string]any)
	if !ok {
		t.Fatalf("field not a map: %v", fields[0])
	}
	options, ok := field["options"].([]any)
	if !ok || len(options) == 0 {
		t.Fatalf("options missing from marshaled field: %v", field)
	}
	option, ok := options[0].(map[string]any)
	if !ok {
		t.Fatalf("option not a map: %v", options[0])
	}
	if option["value"] != "hsapiens" {
		t.Fatalf("option value lost: %v", option)
	}
	if option["description"] != "homo sapiens" {
		t.Fatalf("option description lost: %v", option)
	}
}

// TestActor_RequestForm_OptionValueFallback verifies the emitted
// form.requested schema fills missing option values with the label, so
// the frontend never renders multiple identical (undefined) choices.
func TestActor_RequestForm_OptionValueFallback(t *testing.T) {
	mock := newMockAgent(
		chatScript{
			toolCall: &scriptedToolCall{
				name: "request_form",
				arguments: map[string]any{
					"schema": map[string]any{
						"title":       "Pick one",
						"description": "choose an organism",
						"fields": []map[string]any{
							{"name": "organism", "type": "select",
								"options": []map[string]any{
									{"label": "Human", "description": "homo sapiens"},
									{"label": "Mouse"},
								}},
						},
					},
				},
			},
		},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "ask"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, schema := waitForFormRequestedSchema(t, sub)
	if schema["description"] != "choose an organism" {
		t.Fatalf("schema description lost: %v", schema)
	}

	fields := schema["fields"].([]any)
	options := fields[0].(map[string]any)["options"].([]any)
	first := options[0].(map[string]any)
	second := options[1].(map[string]any)
	if first["value"] != "Human" {
		t.Fatalf("first option value not normalized: %v", first)
	}
	if second["value"] != "Mouse" {
		t.Fatalf("second option value not normalized: %v", second)
	}
	if first["description"] != "homo sapiens" {
		t.Fatalf("option description lost: %v", first)
	}
}

func waitForFormRequestedSchema(t *testing.T, sub *Subscription) (string, map[string]any) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatal("subscription closed before form.requested")
			}
			if e.Type == events.KindCustom && e.Name == events.CustomFormRequested {
				var body events.FormRequestedBody
				if err := events.DecodePayload(e, &body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				return body.FormID, body.Schema
			}
		case <-deadline:
			t.Fatal("timed out waiting for form.requested")
		}
	}
}
