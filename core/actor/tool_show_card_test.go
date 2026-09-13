package actor

import (
	"testing"

	"github.com/basenana/friday/core/session"
)

func TestActorBuiltInToolDefinitionsAreShallowAndDocumented(t *testing.T) {
	a := New(nil, session.New("session", nil))
	for _, tool := range a.Tools() {
		if errors := tool.ValidateDefinition(2); len(errors) != 0 {
			t.Errorf("%s definition: %v", tool.Name, errors)
		}
	}
}

func TestRequestUserInputRejectsLegacyShape(t *testing.T) {
	tool := makeRequestUserInputTool(New(nil, session.New("session", nil)))
	message := tool.ValidateArguments(map[string]any{"questions": []any{}})
	if message == "" {
		t.Fatal("legacy questions argument was accepted")
	}
}
