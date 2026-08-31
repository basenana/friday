package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/actor/events"
	coretools "github.com/basenana/friday/core/tools"
)

// makeUpdateCardTool builds the update_card tool bound to actor a.
//
// update_card publishes a JSON Patch (RFC 6902) against a previously
// emitted card. It is non-blocking.
func makeUpdateCardTool(a *Actor) *coretools.Tool {
	handler := func(ctx context.Context, req *coretools.Request) (*coretools.Result, error) {
		cardID, _ := req.Arguments["card_id"].(string)
		if cardID == "" {
			return coretools.NewToolResultError("update_card requires a 'card_id' field"), nil
		}
		patchRaw, ok := req.Arguments["patch"]
		if !ok {
			return coretools.NewToolResultError("update_card requires a 'patch' field"), nil
		}
		// Patch is a JSON array of RFC 6902 ops; normalize to []any.
		raw, err := json.Marshal(patchRaw)
		if err != nil {
			return coretools.NewToolResultError("invalid patch: " + err.Error()), nil
		}
		var patch []any
		if err := json.Unmarshal(raw, &patch); err != nil {
			return coretools.NewToolResultError("patch must be an array: " + err.Error()), nil
		}
		if err := validateCardPatch(patch); err != nil {
			return coretools.NewToolResultError(err.Error()), nil
		}

		a.EmitCustom(events.CustomCardUpdated, cardID, events.CardUpdatedBody{
			CardID: cardID,
			Patch:  patch,
		})
		return coretools.NewToolResultText(`{"ok":true}`), nil
	}

	return coretools.NewTool("update_card",
		coretools.WithDescription(`Update a previously emitted card by JSON Patch (RFC 6902).
'card_id' is the id returned by emit_card. Patch operations may only be add,
remove, replace, or test, and may target /title or /component (including their
descendants). A patch is limited to 64 operations and 64KB. Unsupported fields
and operations are rejected rather than ignored.`),
		coretools.WithString("card_id",
			coretools.Required(),
			coretools.Description("The id returned by emit_card."),
		),
		coretools.WithArray("patch",
			coretools.Required(),
			coretools.MinItems(1),
			coretools.Items(map[string]interface{}{"type": "object"}),
			coretools.Description("An RFC 6902 JSON Patch array."),
		),
		coretools.WithToolHandler(handler),
	)
}

// validateCardPatch validates only the patch envelope. The actor has no card
// state, so it deliberately does not apply or re-normalize component updates.
func validateCardPatch(patch []any) error {
	if len(patch) == 0 {
		return fmt.Errorf("update_card patch requires at least one operation")
	}
	if len(patch) > 64 {
		return fmt.Errorf("update_card patch may contain at most 64 operations")
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("invalid patch: %w", err)
	}
	if len(raw) > 64*1024 {
		return fmt.Errorf("update_card patch exceeds 64KB")
	}
	for index, value := range patch {
		op, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("update_card patch operation %d must be an object", index)
		}
		operation, _ := op["op"].(string)
		switch operation {
		case "add", "remove", "replace", "test":
		default:
			return fmt.Errorf("update_card patch operation %d has unsupported op %q", index, operation)
		}
		path, _ := op["path"].(string)
		if !allowedCardPatchPath(path) {
			return fmt.Errorf("update_card patch operation %d has unsupported path %q", index, path)
		}
		if !validJSONPointer(path) {
			return fmt.Errorf("update_card patch operation %d has invalid JSON pointer", index)
		}
		requiresValue := operation == "add" || operation == "replace" || operation == "test"
		_, hasValue := op["value"]
		if requiresValue != hasValue {
			return fmt.Errorf("update_card patch operation %d has invalid value field", index)
		}
		for key := range op {
			if key != "op" && key != "path" && key != "value" {
				return fmt.Errorf("update_card patch operation %d has unsupported field %q", index, key)
			}
		}
	}
	return nil
}

func validJSONPointer(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			continue
		}
		if index+1 == len(value) || (value[index+1] != '0' && value[index+1] != '1') {
			return false
		}
		index++
	}
	return true
}

func allowedCardPatchPath(value string) bool {
	return value == "/title" || value == "/component" ||
		strings.HasPrefix(value, "/title/") || strings.HasPrefix(value, "/component/")
}
