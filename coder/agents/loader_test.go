package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentSpec(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SpecFilename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderLoadsDefaultsAndForwardCompatibleFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeAgentSpec(t, root, "reviewer", `---
description: Reviews changes
max_loop_times: "12"
future_scalar: enabled
future_array: [one, two]
future_map:
  nested: true
---
Review the requested work carefully.
`)

	registry, err := NewLoader(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := registry.Get("REVIEWER")
	if !ok {
		t.Fatal("reviewer was not loaded")
	}
	if spec.Name != "reviewer" || spec.Description != "Reviews changes" || spec.MaxLoopTimes != 12 {
		t.Fatalf("unexpected spec: %#v", spec)
	}
	if spec.SystemPrompt != "Review the requested work carefully." {
		t.Fatalf("system prompt = %q", spec.SystemPrompt)
	}
}

func TestLoaderParsesModelAndEffort(t *testing.T) {
	root := t.TempDir()
	writeAgentSpec(t, root, "reviewer", `---
model: model-1
effort: HIGH
---
Review carefully.
`)
	registry, err := NewLoader(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := registry.Get("reviewer")
	if spec.Model != "model-1" || spec.Effort != "high" || spec.SourcePath == "" {
		t.Fatalf("agent policy = %+v", spec)
	}
}

func TestLoaderInvalidOptionalFieldsUseDefaults(t *testing.T) {
	root := t.TempDir()
	writeAgentSpec(t, root, "writer", `---
description: [not, text]
max_loop_times: nope
---
Write the result.
`)

	registry, err := NewLoader(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := registry.Get("writer")
	if spec.Description != "writer" || spec.MaxLoopTimes != 100 {
		t.Fatalf("optional defaults not applied: %#v", spec)
	}
}

func TestLoaderEmptyFrontmatterUsesDefaults(t *testing.T) {
	root := t.TempDir()
	writeAgentSpec(t, root, "writer", "---\n---\nWrite the result.\n")
	registry, err := NewLoader(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := registry.Get("writer")
	if spec.Description != "writer" || spec.MaxLoopTimes != 100 || spec.SystemPrompt != "Write the result." {
		t.Fatalf("unexpected defaults: %#v", spec)
	}
}

func TestLoaderLaterRootOverridesAndListIsSorted(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeAgentSpec(t, home, "writer", "Home prompt")
	writeAgentSpec(t, home, "alpha", "Alpha prompt")
	writeAgentSpec(t, project, "writer", "Project prompt")

	registry, err := NewLoader(home, project).Load()
	if err != nil {
		t.Fatal(err)
	}
	writer, _ := registry.Get("writer")
	if writer.SystemPrompt != "Project prompt" {
		t.Fatalf("override prompt = %q", writer.SystemPrompt)
	}
	list := registry.List()
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "writer" {
		t.Fatalf("list order = %#v", list)
	}
}

func TestLoaderRejectsBrokenSpecs(t *testing.T) {
	tests := map[string]struct {
		dir     string
		content string
		want    string
	}{
		"broken yaml":      {dir: "broken", content: "---\nname: [\n---\nprompt", want: "parse"},
		"open frontmatter": {dir: "open", content: "---\nname: open\nprompt", want: "not closed"},
		"empty prompt":     {dir: "empty", content: "---\ndescription: empty\n---\n", want: "system prompt body"},
		"unsafe directory": {dir: "BadName", content: "prompt", want: "invalid agent directory"},
		"mismatched name":  {dir: "one", content: "---\nname: two\n---\nprompt", want: "must match directory"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeAgentSpec(t, root, tt.dir, tt.content)
			_, err := NewLoader(root).Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoaderMissingRootIsEmpty(t *testing.T) {
	registry, err := NewLoader(filepath.Join(t.TempDir(), "missing")).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.List()) != 0 {
		t.Fatalf("unexpected specs: %#v", registry.List())
	}
}
