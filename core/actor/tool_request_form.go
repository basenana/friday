package actor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/basenana/friday/core/actor/cards"
	"github.com/basenana/friday/core/actor/events"
	coretools "github.com/basenana/friday/core/tools"
)

// makeRequestFormTool builds the request_form tool bound to actor a.
//
// request_form emits an A2UI Form schema and blocks until the user
// submits or cancels. The tool returns the submitted values to the
// agent. While waiting, the actor does not consume new inbox messages
// (the actor loop is blocked inside Chat → tool handler).
func makeRequestFormTool(a *Actor) *coretools.Tool {
	handler := func(ctx context.Context, req *coretools.Request) (*coretools.Result, error) {
		schemaRaw, ok := req.Arguments["schema"]
		if !ok {
			return coretools.NewToolResultError("request_form requires a 'schema' field"), nil
		}
		schemaMap, err := toStringMap(schemaRaw)
		if err != nil {
			return coretools.NewToolResultError("request_form 'schema' must be an object: " + err.Error()), nil
		}

		// Validate structurally by routing the schema through FormSchema.
		schema, err := decodeFormSchema(schemaMap)
		if err != nil {
			return coretools.NewToolResultError("invalid form schema: " + err.Error()), nil
		}
		if err := cards.Default.ValidateForm(schema); err != nil {
			return coretools.NewToolResultError(err.Error()), nil
		}

		// Normalize select/multiselect options so every choice carries a
		// non-empty value. Models frequently omit `value` (only emitting
		// `label` + `description`), which collapses every option to the
		// same undefined value in the frontend. Fall back to the label.
		schema = normalizeFormOptions(schema)
		schemaMap, err = formSchemaToMap(schema)
		if err != nil {
			return coretools.NewToolResultError("invalid form schema: " + err.Error()), nil
		}

		formID := globalIDGenerator.Next("form")
		waiter := a.prepareFormWait(formID)
		// Announce the form.
		a.EmitCustom(events.CustomFormRequested, formID, events.FormRequestedBody{
			FormID: formID,
			Schema: schemaMap,
		})

		// Block until user responds.
		outcome, err := a.waitForRegisteredForm(ctx, formID, waiter)
		if err != nil {
			return coretools.NewToolResultError("form wait failed: " + err.Error()), nil
		}
		if outcome.Cancelled {
			a.EmitCustom(events.CustomFormCancelled, formID, events.FormCancelledBody{FormID: formID})
			return coretools.NewToolResultText(`{"cancelled":true}`), nil
		}
		a.EmitCustom(events.CustomFormSubmitted, formID, events.FormSubmittedBody{
			FormID: formID,
			Values: outcome.Values,
		})

		body := map[string]any{"values": outcome.Values}
		raw, _ := json.Marshal(body)
		return coretools.NewToolResultText(string(raw)), nil
	}

	return coretools.NewTool("request_form",
		coretools.WithDescription(`Emit an A2UI form and BLOCK until the user submits or cancels.
The 'schema' is a JSON object with 'title' and 'fields' (each field has name/label/type
and optional description/options/required/default/min/max). Supported field types: text,
textarea, number, boolean, select, multiselect, date, list, object, file. For select and
multiselect fields each option should carry 'label' and 'value' (plus optional
'description'); when 'value' is omitted it defaults to the option's label. Use this for
structured parameter collection.`),
		coretools.WithObject("schema",
			coretools.Required(),
			coretools.MinProperties(1),
			coretools.Description("The declarative A2UI form schema."),
		),
		coretools.WithToolHandler(handler),
	)
}

// normalizeFormOptions ensures every select/multiselect option has a
// non-empty value, falling back to the option label when the value is
// missing or blank.
func normalizeFormOptions(s cards.FormSchema) cards.FormSchema {
	for i := range s.Fields {
		f := &s.Fields[i]
		if f.Type != cards.FieldSelect && f.Type != cards.FieldMultiSelect {
			continue
		}
		for j := range f.Options {
			if optionValueMissing(f.Options[j].Value) {
				f.Options[j].Value = f.Options[j].Label
			}
		}
	}
	return s
}

func optionValueMissing(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

// formSchemaToMap serializes a cards.FormSchema back to the
// map[string]any shape carried inside form.requested.
func formSchemaToMap(s cards.FormSchema) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return out, nil
}

// decodeFormSchema converts a generic map into a cards.FormSchema.
func decodeFormSchema(m map[string]any) (cards.FormSchema, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return cards.FormSchema{}, fmt.Errorf("marshal schema: %w", err)
	}
	var s cards.FormSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return cards.FormSchema{}, fmt.Errorf("unmarshal schema: %w", err)
	}
	return s, nil
}
