package tools

import (
	"strings"
	"testing"
)

func TestValidateArguments(t *testing.T) {
	tool := NewTool("demo_search",
		WithString("pattern", Required(), MinLength(2), Description("Text pattern to search for.")),
		WithArray("paths", Required(), MinItems(1), UniqueItems(true), Items(map[string]any{"type": "string", "minLength": 1}), Description("Paths to search.")),
	)

	valid := map[string]any{"pattern": "go", "paths": []any{"core", "sandbox"}}
	if message := tool.ValidateArguments(valid); message != "" {
		t.Fatalf("valid arguments rejected: %s", message)
	}
	for name, arguments := range map[string]map[string]any{
		"missing":   {"pattern": "go"},
		"short":     {"pattern": "g", "paths": []any{"core"}},
		"duplicate": {"pattern": "go", "paths": []any{"core", "core"}},
		"unknown":   {"pattern": "go", "paths": []any{"core"}, "legacy": true},
	} {
		t.Run(name, func(t *testing.T) {
			if message := tool.ValidateArguments(arguments); message == "" || !strings.Contains(message, "retry") {
				t.Fatalf("expected actionable validation error, got %q", message)
			}
		})
	}
}

func TestValidateDefinitionRequiresDescriptionsExamplesAndShallowSchemas(t *testing.T) {
	valid := NewTool("batch",
		WithDescription("Run a batch."),
		WithArray("tasks", Required(), Description("Tasks to run."), Items(map[string]any{"type": "string"})),
		WithExample(map[string]any{"tasks": []any{"audit tools"}}),
	)
	if errors := valid.ValidateDefinition(2); len(errors) != 0 {
		t.Fatalf("valid definition errors: %v", errors)
	}

	invalid := NewTool("nested",
		WithArray("items", Items(map[string]any{"type": "array", "items": map[string]any{"type": "object"}})),
	)
	errors := invalid.ValidateDefinition(2)
	if len(errors) < 3 {
		t.Fatalf("expected description, example, and depth errors, got %v", errors)
	}
}

func TestExamplesAreIncludedInDescription(t *testing.T) {
	tool := NewTool("demo", WithDescription("Do the thing."), WithExample(map[string]any{"name": "value"}))
	if description := tool.GetDescription(); !strings.Contains(description, "Example:\n{\"name\":\"value\"}") {
		t.Fatalf("description = %q", description)
	}
}

func TestMarkdownTitle(t *testing.T) {
	if title := MarkdownTitle("text\n# Tool redesign\nbody", "fallback"); title != "Tool redesign" {
		t.Fatalf("title = %q", title)
	}
	if title := MarkdownTitle("#not-a-heading", "fallback"); title != "fallback" {
		t.Fatalf("fallback title = %q", title)
	}
}
