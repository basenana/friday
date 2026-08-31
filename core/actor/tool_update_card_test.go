package actor

import (
	"strings"
	"testing"
)

func TestValidateCardPatch(t *testing.T) {
	tests := []struct {
		name    string
		patch   []any
		wantErr string
	}{
		{"add", []any{map[string]any{"op": "add", "path": "/component/rows/-", "value": "row"}}, ""},
		{"remove", []any{map[string]any{"op": "remove", "path": "/component/rows/0"}}, ""},
		{"replace", []any{map[string]any{"op": "replace", "path": "/title", "value": "Updated"}}, ""},
		{"test", []any{map[string]any{"op": "test", "path": "/component/status", "value": "ready"}}, ""},
		{"replace explicit null", []any{map[string]any{"op": "replace", "path": "/component/status", "value": nil}}, ""},
		{"move", []any{map[string]any{"op": "move", "path": "/title", "from": "/component/title"}}, "op"},
		{"copy", []any{map[string]any{"op": "copy", "path": "/title", "from": "/component/title"}}, "op"},
		{"root kind", []any{map[string]any{"op": "replace", "path": "/kind", "value": "table"}}, "path"},
		{"root card id", []any{map[string]any{"op": "replace", "path": "/card_id", "value": "card-1"}}, "path"},
		{"other root", []any{map[string]any{"op": "replace", "path": "/files", "value": []any{}}}, "path"},
		{"empty patch", []any{}, "at least one"},
		{"add missing value", []any{map[string]any{"op": "add", "path": "/title"}}, "value"},
		{"replace missing value", []any{map[string]any{"op": "replace", "path": "/title"}}, "value"},
		{"test missing value", []any{map[string]any{"op": "test", "path": "/title"}}, "value"},
		{"remove value", []any{map[string]any{"op": "remove", "path": "/title", "value": nil}}, "field"},
		{"extra field", []any{map[string]any{"op": "replace", "path": "/title", "value": "Updated", "extra": true}}, "field"},
		{"from field", []any{map[string]any{"op": "replace", "path": "/title", "value": "Updated", "from": "/component"}}, "field"},
		{"invalid pointer escape", []any{map[string]any{"op": "replace", "path": "/component/~2value", "value": "Updated"}}, "pointer"},
		{"trailing pointer escape", []any{map[string]any{"op": "replace", "path": "/component/~", "value": "Updated"}}, "pointer"},
		{"too many operations", repeatedPatch(65), "64 operations"},
		{"too large", []any{map[string]any{"op": "replace", "path": "/title", "value": strings.Repeat("x", 64*1024)}}, "64KB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCardPatch(tt.patch)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCardPatch() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateCardPatch() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func repeatedPatch(count int) []any {
	patch := make([]any, count)
	for i := range patch {
		patch[i] = map[string]any{"op": "replace", "path": "/title", "value": "Updated"}
	}
	return patch
}
