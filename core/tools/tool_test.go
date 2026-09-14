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

func TestValidateArgumentsReturnsAllIssuesInStableOrder(t *testing.T) {
	tool := NewTool("batch",
		WithString("name", Required(), MinLength(2), Description("Batch name.")),
		WithNumber("ratio", MultipleOf(0.5), Description("Batch ratio.")),
		WithObject("labels", AdditionalProperties(map[string]any{"type": "string"}),
			PropertyNames(map[string]any{"type": "string", "pattern": `^[a-z]+$`}), Description("Labels.")),
	)
	args := map[string]any{
		"extra":  true,
		"labels": map[string]any{"Bad-Key": 4.0, "good": false},
		"ratio":  0.3,
	}
	first := tool.ValidateArguments(args)
	second := tool.ValidateArguments(args)
	if first != second {
		t.Fatalf("validation order changed:\nfirst: %s\nsecond: %s", first, second)
	}
	for _, want := range []string{
		"arguments.name is required",
		"arguments.extra is not supported",
		"arguments.labels.Bad-Key must be a string",
		`arguments.labels property name "Bad-Key" must match pattern`,
		"arguments.labels.good must be a string",
		"arguments.ratio must be a multiple of 0.5",
		"Suggestion:",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("validation message missing %q:\n%s", want, first)
		}
	}
}

func TestNewToolResultActionableError(t *testing.T) {
	result := NewToolResultActionableError("path does not exist", "use fs_list to find the correct path")
	if !result.IsError {
		t.Fatal("result must be an error")
	}
	text := result.Content[0].(TextContent).Text
	if !strings.Contains(text, "path does not exist") || !strings.Contains(text, "Suggestion: use fs_list") {
		t.Fatalf("result text = %q", text)
	}
}

func TestNewToolResultErrorAddsFallbackSuggestion(t *testing.T) {
	result := NewToolResultError("remote operation failed")
	text := result.Content[0].(TextContent).Text
	if !strings.Contains(text, "remote operation failed") || !strings.Contains(text, "Suggestion:") {
		t.Fatalf("result text is not actionable: %q", text)
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
