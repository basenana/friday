package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/core/tools"
)

func TestLoadSkillToolReturnsAbsoluteDirectoryPath(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "writer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: writer\ndescription: Draft release notes\nallowed_tools: fs_read\n---\nFollow references/style.md."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	workdir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(workdir, root)
	if err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(relativeRoot)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	tool := newLoadSkillTool(NewRegistry(loader))
	result, err := tool.Handler(context.Background(), &tools.Request{Arguments: map[string]any{"name": "writer"}})
	if err != nil {
		t.Fatalf("load_skill handler error: %v", err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("load_skill result = %#v", result)
	}
	text, ok := result.Content[0].(tools.TextContent)
	if !ok {
		t.Fatalf("load_skill content type = %T", result.Content[0])
	}
	var payload struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		DirPath      string `json:"dir_path"`
		Instructions string `json:"instructions"`
		AllowedTools string `json:"allowed_tools"`
	}
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		t.Fatalf("decode load_skill result: %v", err)
	}
	if payload.Name != "writer" || payload.Description != "Draft release notes" {
		t.Fatalf("load_skill metadata = %#v", payload)
	}
	if !filepath.IsAbs(payload.DirPath) || payload.DirPath != filepath.Clean(skillDir) {
		t.Fatalf("dir_path = %q, want absolute %q", payload.DirPath, skillDir)
	}
	if payload.Instructions != "Follow references/style.md." || payload.AllowedTools != "fs_read" {
		t.Fatalf("load_skill instructions or allowed_tools missing: %#v", payload)
	}
}
