package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workspaceConfigStub struct {
	workspace string
	fallbacks []string
	project   bool
	memory    string
}

func (c workspaceConfigStub) WorkspacePath() string            { return c.workspace }
func (c workspaceConfigStub) WorkspaceFallbackPaths() []string { return c.fallbacks }
func (c workspaceConfigStub) ProjectScoped() bool              { return c.project }
func (c workspaceConfigStub) MemoryPath() string               { return c.memory }

func TestNewWorkspace(t *testing.T) {
	ws := NewWorkspace("/tmp/workspace", "/tmp/memory")
	if ws.BasePath() != "/tmp/workspace" {
		t.Errorf("expected basePath /tmp/workspace, got %s", ws.BasePath())
	}
	if ws.MemoryPath() != "/tmp/memory" {
		t.Errorf("expected memoryPath /tmp/memory, got %s", ws.MemoryPath())
	}
}

func TestLayeredWorkspaceProjectOverridesAndFallsBack(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	memory := t.TempDir()

	for name, content := range map[string]string{
		"AGENTS.md": "global agents",
		"SOUL.md":   "global soul",
		"MEMORY.md": "global memory",
	} {
		if err := os.WriteFile(filepath.Join(global, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "SOUL.md"), []byte("project soul"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "MEMORY.md"), []byte("project memory"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := NewWorkspace(project, memory, global)
	if got, err := ws.Read("AGENTS.md"); err != nil || got != "global agents" {
		t.Fatalf("Read inherited AGENTS = %q, %v", got, err)
	}
	if got, err := ws.LoadFile("SOUL.md"); err != nil || got != "project soul" {
		t.Fatalf("LoadFile project SOUL = %q, %v", got, err)
	}
	loaded, err := ws.Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.SystemPrompts, "\n") != "global agents\nproject soul" {
		t.Fatalf("layered system prompts = %v", loaded.SystemPrompts)
	}
	if len(loaded.MemoryHistory) != 1 || !strings.Contains(loaded.MemoryHistory[0].Content, "project memory") || strings.Contains(loaded.MemoryHistory[0].Content, "global memory") {
		t.Fatalf("layered memory history = %#v", loaded.MemoryHistory)
	}
	if err := ws.Write("AGENTS.md", "project agents"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ws.Read("AGENTS.md"); got != "project agents" {
		t.Fatalf("project write did not override global file: %q", got)
	}
	if err := ws.Delete("AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ws.Read("AGENTS.md"); got != "global agents" {
		t.Fatalf("deleting project override did not reveal global file: %q", got)
	}

	wantSkills := []string{filepath.Join(global, "skills"), filepath.Join(project, "skills")}
	gotSkills := ws.SkillsPaths()
	if len(gotSkills) != len(wantSkills) || gotSkills[0] != wantSkills[0] || gotSkills[1] != wantSkills[1] {
		t.Fatalf("SkillsPaths() = %v, want %v", gotSkills, wantSkills)
	}
	wantMCP := []string{filepath.Join(global, "mcp"), filepath.Join(project, "mcp")}
	gotMCP := ws.MCPPaths()
	if len(gotMCP) != len(wantMCP) || gotMCP[0] != wantMCP[0] || gotMCP[1] != wantMCP[1] {
		t.Fatalf("MCPPaths() = %v, want %v", gotMCP, wantMCP)
	}
}

func TestNewFromConfigBuildsScopedLowToHighLayers(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	ws := NewFromConfig(workspaceConfigStub{
		workspace: project,
		fallbacks: []string{home},
		project:   true,
		memory:    t.TempDir(),
	})

	layers := ws.Layers()
	if len(layers) != 2 {
		t.Fatalf("Layers() = %#v", layers)
	}
	if layers[0].Root != home || layers[0].Scope != ScopeHome || layers[0].Writable {
		t.Fatalf("HOME layer = %#v", layers[0])
	}
	if layers[1].Root != project || layers[1].Scope != ScopeProject || !layers[1].Writable {
		t.Fatalf("project layer = %#v", layers[1])
	}
	roots := ws.ResourceRoots("skills")
	if roots[0].Path != filepath.Join(home, "skills") || roots[1].Path != filepath.Join(project, "skills") {
		t.Fatalf("ResourceRoots(skills) = %#v", roots)
	}
}

func TestNewFromConfigSharedHomeWorkspaceIsReadOnly(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("home agents"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := NewFromConfig(workspaceConfigStub{
		workspace: home,
		fallbacks: []string{home},
		project:   true,
		memory:    t.TempDir(),
	})

	layers := ws.Layers()
	if len(layers) != 1 || layers[0].Scope != ScopeHome || layers[0].Writable {
		t.Fatalf("shared layers = %#v", layers)
	}
	if got, err := ws.Read("AGENTS.md"); err != nil || got != "home agents" {
		t.Fatalf("Read(AGENTS.md) = %q, %v", got, err)
	}
	if err := ws.EnsureDir(""); err != nil {
		t.Fatalf("EnsureDir should accept an existing read-only root: %v", err)
	}
	if err := ws.Write("AGENTS.md", "project agents"); !errors.Is(err, ErrReadOnlyWorkspace) {
		t.Fatalf("Write() error = %v, want ErrReadOnlyWorkspace", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "AGENTS.md"))
	if err != nil || string(data) != "home agents" {
		t.Fatalf("HOME file changed: %q, %v", data, err)
	}
	if roots := ws.MCPRoots(); len(roots) != 1 || roots[0].Scope != ScopeHome {
		t.Fatalf("MCPRoots() = %#v", roots)
	}
	if ws.SkillsPath() != "" || ws.MCPPath() != "" {
		t.Fatalf("read-only workspace exposed a writable resource path")
	}
}

func TestNewFromConfigProjectSymlinkKeepsProjectScope(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	linkedWorkspace := filepath.Join(project, "workspace-link")
	if err := os.Symlink(home, linkedWorkspace); err != nil {
		t.Fatal(err)
	}
	ws := NewFromConfig(workspaceConfigStub{
		workspace: linkedWorkspace,
		fallbacks: []string{home},
		project:   true,
		memory:    t.TempDir(),
	})

	layers := ws.Layers()
	if len(layers) != 2 || layers[1].Scope != ScopeProject || layers[1].Root != linkedWorkspace {
		t.Fatalf("symlink layers = %#v", layers)
	}
}

func TestLayeredWorkspaceListMergesNames(t *testing.T) {
	project := t.TempDir()
	global := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "project.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "global.md"), []byte("g"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(project, t.TempDir(), global)
	got, err := ws.Ls("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"global.md", "project.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Ls() = %v, want %v", got, want)
	}
}

func TestWorkspaceInit(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")

	ws := NewWorkspace(workspacePath, memoryPath)

	// Test Init
	created, err := ws.InitWithParams(nil)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if len(created) != len(DefaultContents) {
		t.Errorf("expected %d created files, got %d", len(DefaultContents), len(created))
	}

	// Verify directories exist
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		t.Error("workspace directory not created")
	}
	if _, err := os.Stat(memoryPath); os.IsNotExist(err) {
		t.Error("memory directory not created")
	}

	// Verify files exist
	for filename := range DefaultContents {
		filePath := filepath.Join(workspacePath, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("file %s not created", filename)
		}
	}

	// Test Init again - should return empty created list
	createdAgain, err := ws.InitWithParams(nil)
	if err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
	if len(createdAgain) != 0 {
		t.Errorf("expected 0 created files on second init, got %d", len(createdAgain))
	}
}

func TestWorkspaceLoad(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")

	ws := NewWorkspace(workspacePath, memoryPath)

	// Init workspace
	_, err = ws.InitWithParams(nil)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Test Load
	content, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have 3 system prompt files (AGENTS, SOUL, IDENTITY)
	if len(content.SystemPrompts) != 3 {
		t.Errorf("expected 3 system prompts, got %d", len(content.SystemPrompts))
	}

	// MEMORY.md has FileRoleMemory, so it should not be in SystemPrompts
	if len(content.SystemPrompts) != 3 {
		t.Errorf("expected memory files to stay out of loaded content prompts, got %d system prompts", len(content.SystemPrompts))
	}

	// Fresh workspace: MEMORY.md only contains the empty template header,
	// so no memory history messages should be produced.
	if len(content.MemoryHistory) != 0 {
		t.Errorf("expected 0 memory history messages for fresh workspace, got %d", len(content.MemoryHistory))
	}
}

func TestWorkspaceLoadIncludesLongTermAndRecentMemory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")

	ws := NewWorkspace(workspacePath, memoryPath)
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workspacePath, "MEMORY.md"), []byte("# MEMORY.md\n\n- Prefers concise answers"), 0644); err != nil {
		t.Fatalf("failed to write MEMORY.md: %v", err)
	}
	today := time.Now().Format("2006-01-02")
	if err := os.WriteFile(filepath.Join(memoryPath, today+".md"), []byte("Investigated low mapping rate in sample T1."), 0644); err != nil {
		t.Fatalf("failed to write daily memory: %v", err)
	}

	content, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(content.MemoryHistory) != 2 {
		t.Fatalf("expected 2 memory history messages, got %d", len(content.MemoryHistory))
	}
	if !strings.Contains(content.MemoryHistory[0].Content, "[Long-Term Memory]") {
		t.Fatalf("expected long-term memory header, got %q", content.MemoryHistory[0].Content)
	}
	if !strings.Contains(content.MemoryHistory[1].Content, "[Recent Memory Context]") {
		t.Fatalf("expected recent memory header, got %q", content.MemoryHistory[1].Content)
	}
}

func TestWorkspaceLoadMemoryDaysOption(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "workspace-memory-days-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")
	ws := NewWorkspace(workspacePath, memoryPath)
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Write one daily log three days back and one for today.
	oldDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	for name, content := range map[string]string{
		oldDate + ".md": "Older than the default window.",
		today + ".md":   "Today's note.",
	} {
		if err := os.WriteFile(filepath.Join(memoryPath, name), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	// Default: only today + yesterday.
	defaultContent, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(defaultContent.MemoryHistory[len(defaultContent.MemoryHistory)-1].Content, "Today's note.") {
		t.Fatalf("expected today's log by default, got %#v", defaultContent.MemoryHistory)
	}
	if strings.Contains(defaultContent.MemoryHistory[len(defaultContent.MemoryHistory)-1].Content, "Older than the default window.") {
		t.Fatalf("3-day-old log should not load with the default 2-day window, got %#v", defaultContent.MemoryHistory)
	}

	// Configured retention: 14 days pulls in the 3-day-old log.
	content, err := ws.Load(WithMemoryDays(14))
	if err != nil {
		t.Fatalf("Load(WithMemoryDays(14)) failed: %v", err)
	}
	combined := content.MemoryHistory[len(content.MemoryHistory)-1].Content
	if !strings.Contains(combined, "Older than the default window.") || !strings.Contains(combined, "Today's note.") {
		t.Fatalf("expected both logs with 14-day window, got %#v", content.MemoryHistory)
	}

	// Invalid values fall back to the default.
	fallback, err := ws.Load(WithMemoryDays(0))
	if err != nil {
		t.Fatalf("Load(WithMemoryDays(0)) failed: %v", err)
	}
	if len(fallback.MemoryHistory) != len(defaultContent.MemoryHistory) {
		t.Fatalf("expected zero value to fall back to default, got %#v", fallback.MemoryHistory)
	}
}

func TestComposeSystemPrompt(t *testing.T) {
	tests := []struct {
		name        string
		content     *LoadedContent
		contains    string
		notContains string
	}{
		{
			name: "empty prompts filtered",
			content: &LoadedContent{
				SystemPrompts: []string{"", "prompt1", "   ", "prompt2"},
			},
			contains: "prompt1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComposeSystemPrompt(tt.content)
			if tt.contains != "" && !strings.Contains(result, tt.contains) {
				t.Errorf("expected result to contain %q, got %q", tt.contains, result)
			}
			if tt.notContains != "" && strings.Contains(result, tt.notContains) {
				t.Errorf("expected result not to contain %q, got %q", tt.notContains, result)
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     string
		params   *TemplateParams
		expected string
	}{
		{
			name:     "nil params uses defaults",
			tmpl:     "DataDir: {{if .Paths}}{{.Paths.DataDir}}{{else}}~/.friday{{end}}",
			params:   nil,
			expected: "DataDir: ~/.friday",
		},
		{
			name: "with paths renders correctly",
			tmpl: "DataDir: {{.Paths.DataDir}}",
			params: &TemplateParams{
				Paths: &Paths{
					DataDir: "/custom/data",
				},
			},
			expected: "DataDir: /custom/data",
		},
		{
			name:     "plain text unchanged",
			tmpl:     "Hello World",
			params:   nil,
			expected: "Hello World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RenderTemplate(tt.tmpl, tt.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
