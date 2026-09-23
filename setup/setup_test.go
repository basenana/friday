package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/coder/configtools"
	"github.com/basenana/friday/coder/worktreectx"
	"github.com/basenana/friday/config"
	coreagents "github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/workspace"
)

type recordingProviderClient struct {
	mu       sync.Mutex
	requests []providers.Request
}

type projectResourceProbeClient struct {
	paths   map[string]string
	mu      sync.Mutex
	results map[string]*tools.Result
}

type projectAccessProbe struct {
	name      string
	tool      string
	arguments map[string]interface{}
}

func (c *projectResourceProbeClient) Completion(ctx context.Context, req providers.Request) providers.Response {
	definitions := make(map[string]*tools.Tool)
	for _, definition := range req.ToolDefines() {
		tool, ok := definition.(*tools.Tool)
		if ok {
			definitions[tool.Name] = tool
		}
	}
	probes := []projectAccessProbe{
		{name: "read_codebase", tool: "fs_read", arguments: map[string]interface{}{"path": c.paths["codebase"]}},
		{name: "write_metadata", tool: "fs_write", arguments: map[string]interface{}{"path": c.paths["metadata"], "content": "changed"}},
		{name: "write_checkout", tool: "fs_write", arguments: map[string]interface{}{"path": c.paths["checkout"], "content": "owned"}},
		{name: "read_sibling", tool: "fs_read", arguments: map[string]interface{}{"path": c.paths["sibling"]}},
		{name: "write_sibling", tool: "fs_write", arguments: map[string]interface{}{"path": c.paths["sibling"], "content": "shared"}},
	}
	if path := c.paths["git_config"]; path != "" {
		probes = append(probes, projectAccessProbe{name: "write_git_config", tool: "fs_write", arguments: map[string]interface{}{"path": path, "content": "blocked"}})
	}
	if workdir := c.paths["shell_workdir"]; workdir != "" {
		probes = append(probes,
			projectAccessProbe{name: "shell_sibling", tool: "bash", arguments: map[string]interface{}{"command": "touch shell-owned.txt", "workdir": workdir}},
			projectAccessProbe{name: "background_sibling", tool: "background_task", arguments: map[string]interface{}{"command": "touch background-owned.txt", "workdir": workdir}},
		)
	}
	if path := c.paths["denied"]; path != "" {
		probes = append(probes, projectAccessProbe{name: "read_denied", tool: "fs_read", arguments: map[string]interface{}{"path": path}})
	}
	for _, probe := range probes {
		tool := definitions[probe.tool]
		if tool == nil || tool.Handler == nil {
			continue
		}
		result, err := tool.Handler(ctx, &tools.Request{Arguments: probe.arguments})
		if err != nil {
			result = tools.NewToolResultError(err.Error())
		}
		c.mu.Lock()
		c.results[probe.name] = result
		c.mu.Unlock()
	}
	resp := providers.NewCommonResponse()
	resp.Stream <- providers.Delta{Content: "done"}
	close(resp.Stream)
	close(resp.Err)
	return resp
}

func (c *projectResourceProbeClient) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "done", nil
}

func (c *projectResourceProbeClient) StructuredPredict(context.Context, providers.Request, any) error {
	return nil
}

func (c *projectResourceProbeClient) result(name string) *tools.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.results[name]
}

func (c *recordingProviderClient) Completion(_ context.Context, req providers.Request) providers.Response {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	resp := providers.NewCommonResponse()
	resp.Stream <- providers.Delta{Content: "done"}
	close(resp.Stream)
	close(resp.Err)
	return resp
}

func (c *recordingProviderClient) CompletionNonStreaming(_ context.Context, _ providers.Request) (string, error) {
	return "done", nil
}

func (c *recordingProviderClient) StructuredPredict(_ context.Context, _ providers.Request, _ any) error {
	return nil
}

func (c *recordingProviderClient) snapshot() []providers.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]providers.Request(nil), c.requests...)
}

func TestAppendImageRefsToMessage(t *testing.T) {
	message := appendImageRefsToMessage("What is in this image?", []string{"/tmp/example.png"})

	if !strings.Contains(message, "/tmp/example.png") {
		t.Fatalf("expected image reference to be included, got %q", message)
	}
	if !strings.Contains(message, "use the image tool") {
		t.Fatalf("expected image tool hint to be included, got %q", message)
	}
}

func TestWorkspaceLoadProvidesSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "setup-system-prompt-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ws := workspace.NewWorkspace(filepath.Join(tmpDir, "workspace"), filepath.Join(tmpDir, "memory"))
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("init workspace failed: %v", err)
	}
	loaded, err := ws.Load()
	if err != nil {
		t.Fatalf("load workspace failed: %v", err)
	}

	agentsPrompt, err := ws.Read("AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md failed: %v", err)
	}

	systemPrompt := workspace.ComposeSystemPrompt(loaded)

	if !strings.Contains(systemPrompt, strings.TrimSpace(agentsPrompt)) {
		t.Fatalf("expected composed system prompt to include AGENTS.md, got %q", systemPrompt)
	}
}

func TestProjectPromptLayersPreserveUserMessage(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "AGENTS.md"), []byte("distinct project rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false
	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatal(err)
	}
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &recordingProviderClient{}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "recording"}})
	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), WithWorkdir(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()

	const userMessage = "USER-BYTES-unchanged\nsecond line"
	if _, err := api.ReadAllContent(context.Background(), agentCtx.Agent.Chat(context.Background(), &api.Request{
		Session: agentCtx.Session, UserMessage: userMessage,
	})); err != nil {
		t.Fatal(err)
	}
	requests := client.snapshot()
	if len(requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(requests))
	}
	if got := strings.Count(requests[0].SystemPrompt(), "Project coding baseline:"); got != 1 {
		t.Fatalf("coding contract count = %d", got)
	}
	history := requests[0].History()
	if len(history) < 2 || history[0].Role != types.RoleAgent || history[len(history)-1].Role != types.RoleUser || history[len(history)-1].Content != userMessage {
		t.Fatalf("provider history = %#v", history)
	}
	canonicalRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	workspaceIndex := strings.Index(history[0].Content, `"`+canonicalRoot+`"`)
	instructionsIndex := strings.Index(history[0].Content, "distinct project rules")
	if workspaceIndex < 0 || instructionsIndex <= workspaceIndex {
		t.Fatalf("project context ordering = %q", history[0].Content)
	}
	for _, message := range agentCtx.Session.GetHistory() {
		if message.Role == types.RoleUser && message.Content != userMessage {
			t.Fatalf("unexpected user history: %#v", agentCtx.Session.GetHistory())
		}
		if message.Role == types.RoleAgent && (strings.Contains(message.Content, "# workspace") || strings.Contains(message.Content, canonicalRoot)) {
			t.Fatalf("bootstrap persisted in durable history: %#v", agentCtx.Session.GetHistory())
		}
	}
}

func TestNewAgentProjectResourceAccessIsReadOnlyAndProjectCodeScoped(t *testing.T) {
	base := t.TempDir()
	checkout := filepath.Join(base, "checkout")
	sibling := filepath.Join(base, "sibling")
	resources := filepath.Join(base, "data", "projects", "project-id")
	codebase := filepath.Join(resources, "codebase", "INDEX.md")
	metadata := filepath.Join(resources, "project.json")
	for _, dir := range []string{checkout, sibling, filepath.Dir(codebase)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		codebase: "shared index", metadata: "metadata", filepath.Join(sibling, "secret.txt"): "sibling",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	sharedReadOnly := append([]string(nil), cfg.Sandbox.Sandbox.Filesystem.ReadOnly...)
	sharedWrite := append([]string(nil), cfg.Sandbox.Sandbox.Filesystem.Write...)
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &projectResourceProbeClient{
		paths: map[string]string{
			"codebase": codebase, "metadata": metadata,
			"checkout": filepath.Join(checkout, "owned.txt"),
			"sibling":  filepath.Join(sibling, "secret.txt"),
		},
		results: make(map[string]*tools.Result),
	}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "probe"}})
	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), WithTemporary(true), WithWorkdir(checkout), WithProjectResources(resources), WithProjectCodeRoot(base))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()
	if _, err := api.ReadAllContent(context.Background(), agentCtx.Chat(context.Background(), "probe access")); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"read_codebase", "write_checkout", "read_sibling", "write_sibling"} {
		if result := client.result(name); result == nil || result.IsError {
			t.Fatalf("%s result = %#v, want success", name, result)
		}
	}
	for _, name := range []string{"write_metadata"} {
		if result := client.result(name); result == nil || !result.IsError {
			t.Fatalf("%s result = %#v, want policy denial", name, result)
		}
	}
	if got, err := os.ReadFile(metadata); err != nil || string(got) != "metadata" {
		t.Fatalf("project metadata changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "owned.txt")); err != nil || string(got) != "owned" {
		t.Fatalf("checkout write = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(sibling, "secret.txt")); err != nil || string(got) != "shared" {
		t.Fatalf("sibling write = %q, %v", got, err)
	}
	if len(cfg.Sandbox.Sandbox.Filesystem.ReadOnly) != len(sharedReadOnly) {
		t.Fatalf("shared sandbox config mutated: %#v", cfg.Sandbox.Sandbox.Filesystem.ReadOnly)
	}
	if len(cfg.Sandbox.Sandbox.Filesystem.Write) != len(sharedWrite) {
		t.Fatalf("shared sandbox write config mutated: %#v", cfg.Sandbox.Sandbox.Filesystem.Write)
	}
}

func TestNewAgentInstallsWorktreeContextOnlyWhenConfigured(t *testing.T) {
	base := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &recordingProviderClient{}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "recording"}})
	info := worktreectx.Context{
		ProjectName: "project", ProjectRoot: base, WorktreeName: "task", Branch: "friday/task",
		WorktreeRoot: base, SessionID: "session-task",
	}
	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), WithTemporary(true), WithWorkdir(base), WithWorktreeContext(info))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()
	req := providers.NewRequest("")
	if err := agentCtx.Session.RunHooks(context.Background(), types.SessionHookBeforeModel, coresession.HookPayload{ModelRequest: req}); err != nil {
		t.Fatal(err)
	}
	if len(req.History()) == 0 || !strings.Contains(req.History()[0].Content, "Worktree session: session-task") {
		t.Fatalf("worktree context missing: %#v", req.History())
	}
}

func TestDiskAgentReusesPrimaryClientPromptAndRunTaskRegistration(t *testing.T) {
	base := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false
	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := ws.Load()
	if err != nil {
		t.Fatal(err)
	}
	workspacePrompt := workspace.ComposeSystemPrompt(loaded)

	agentRegistry := coderagents.NewRegistry()
	agentRegistry.Register(&coderagents.AgentSpec{
		Name: "reviewer", Description: "Review work", SystemPrompt: "AGENT ONLY PROMPT", MaxLoopTimes: 7,
	})
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &recordingProviderClient{}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "recording"}})
	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), WithAgentRegistry(agentRegistry))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()

	content, err := api.ReadAllContent(context.Background(), agentCtx.Agent.Chat(context.Background(), &api.Request{
		Session: agentCtx.Session, UserMessage: "review this", Metadata: map[string]string{coderagents.RouteMetadataKey: "reviewer"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if content != "done" {
		t.Fatalf("content = %q", content)
	}
	requests := client.snapshot()
	if len(requests) != 1 {
		t.Fatalf("primary client calls = %d, want 1", len(requests))
	}
	systemPrompt := requests[0].SystemPrompt()
	wantStablePrefix := coderagents.ComposeSystemPrompt(
		coderagents.ComposeSystemPrompt(coreagents.DEFAULT_SYSTEM_PROMPT, workspacePrompt),
		"AGENT ONLY PROMPT",
	)
	if !strings.HasPrefix(strings.TrimSpace(systemPrompt), wantStablePrefix) {
		t.Fatalf("system prompt does not start with workspace + agent prompt:\n%s", systemPrompt)
	}
	if got := strings.Count(systemPrompt, "Project coding baseline:"); got != 1 {
		t.Fatalf("disk expert coding contract count = %d", got)
	}
	foundRunTask := false
	for _, tool := range requests[0].ToolDefines() {
		if tool.GetName() != "run_task" {
			continue
		}
		foundRunTask = true
		tasks := tool.GetParameters()["properties"].(map[string]interface{})["tasks"].(map[string]interface{})
		items := tasks["items"].(map[string]interface{})
		properties := items["properties"].(map[string]interface{})
		enum := properties["agent_name"].(map[string]interface{})["enum"].([]string)
		if len(enum) != 1 || enum[0] != "reviewer" {
			t.Fatalf("run_task agent enum = %#v", enum)
		}
	}
	if !foundRunTask {
		t.Fatal("run_task tool was not registered")
	}
}

func TestConfigToolsAreExplicitlyEnabled(t *testing.T) {
	base := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &recordingProviderClient{}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "recording"}})
	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), WithTemporary(true), WithConfigTools(true))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()
	req := &api.Request{}
	if err := agentCtx.Session.RunHooks(context.Background(), types.SessionHookBeforeAgent, coresession.HookPayload{AgentRequest: req}); err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool)
	for _, tool := range req.Tools {
		found[tool.Name] = true
	}
	for _, name := range []string{configtools.AgentToolName, configtools.MCPToolName} {
		if !found[name] {
			t.Fatalf("missing explicitly enabled tool %q in %#v", name, found)
		}
	}
}

func TestNewAgentRestoresRuntimeIntoProvidedClientPolicy(t *testing.T) {
	base := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false
	cfg.Models = append(cfg.Models, config.ModelConfig{Provider: "openai", Model: "alternate"})

	store := file.NewFileSessionStore(cfg.SessionsPath())
	sess, err := store.Create("restored", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	if err := mgr.SetModel("restored", sessions.ModelSelection{Model: "alternate"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetEffort("restored", "high"); err != nil {
		t.Fatal(err)
	}

	pool, err := CreateModelPool(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy := fallback.NewSessionPolicy(providers.ClientPolicy{})
	client := pool.NewClient(policy, providers.ClientPolicy{})
	agentCtx, err := NewAgent(mgr, cfg, WithSessionID("restored"), WithProviderClient(client))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()
	if agentCtx.Policy != policy {
		t.Fatal("AgentContext did not reuse the provided client policy")
	}
	if got := policy.Snapshot(); got != (providers.ClientPolicy{PreferredModel: "alternate", Effort: "high"}) {
		t.Fatalf("restored policy = %+v", got)
	}
}

func TestNewAgentMemoryInjectedPerRequestNotPersisted(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "setup-memory-hook-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(tmpDir, "data")
	cfg.Workspace = filepath.Join(tmpDir, "workspace")
	cfg.Model.Provider = "openai"
	cfg.Model.Model = "test-model"

	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("init workspace failed: %v", err)
	}
	if err := ws.Write("MEMORY.md", "remember persistent workspace facts"); err != nil {
		t.Fatalf("write MEMORY.md failed: %v", err)
	}
	if err := os.MkdirAll(cfg.MemoryPath(), 0755); err != nil {
		t.Fatalf("create memory dir failed: %v", err)
	}
	memLogPath := filepath.Join(cfg.MemoryPath(), time.Now().Format("2006-01-02")+".md")
	if err := os.WriteFile(memLogPath, []byte("daily memory note"), 0644); err != nil {
		t.Fatalf("write memory log failed: %v", err)
	}

	sessionStore := file.NewFileSessionStore(cfg.SessionsPath())
	sessionMgr := sessions.NewManager(sessionStore, filepath.Join(cfg.DataDirPath(), "current"), "")

	agentCtx, err := NewAgent(sessionMgr, cfg)
	if err != nil {
		t.Fatalf("NewAgent failed: %v", err)
	}

	// Memory must not be baked into the persisted session history.
	if history := agentCtx.Session.GetHistory(); len(history) != 0 {
		t.Fatalf("expected empty session history, got %#v", history)
	}

	// Instead it is injected at request-composition time on every model call.
	req := providers.NewRequest("system")
	if err := agentCtx.Session.RunHooks(context.Background(), types.SessionHookBeforeModel,
		coresession.HookPayload{ModelRequest: req}); err != nil {
		t.Fatalf("RunHooks failed: %v", err)
	}
	injected := req.History()
	if len(injected) != 3 {
		t.Fatalf("expected project context plus 2 memory messages in the model request, got %#v", injected)
	}
	if injected[0].Role != types.RoleAgent || !strings.Contains(injected[0].Content, "# workspace") {
		t.Fatalf("expected leading project context, got %#v", injected[0])
	}
	if !strings.Contains(injected[1].Content, "[Long-Term Memory]") {
		t.Fatalf("expected long-term memory message, got %q", injected[1].Content)
	}
	if !strings.Contains(injected[2].Content, "[Recent Memory Context]") {
		t.Fatalf("expected recent memory message, got %q", injected[2].Content)
	}
	if history := agentCtx.Session.GetHistory(); len(history) != 0 {
		t.Fatalf("session history must stay untouched, got %#v", history)
	}
}

type hookToolAppender struct{}

func (h *hookToolAppender) BeforeAgent(ctx context.Context, sess *coresession.Session, req coresession.AgentRequest) error {
	req.AppendTools(tools.NewTool("managed_tool", tools.WithDescription("managed test tool")))
	return nil
}

func TestGetOrCreateManagedSessionInstallsHooks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "setup-managed-session-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := file.NewFileSessionStore(filepath.Join(tmpDir, "sessions"))
	sessionMgr := sessions.NewManager(store, filepath.Join(tmpDir, "current"), "")

	sess, _, err := getOrCreateManagedSession(sessionMgr, "managed-1", []coresession.Hook{&hookToolAppender{}})
	if err != nil {
		t.Fatalf("getOrCreateManagedSession failed: %v", err)
	}

	req := &api.Request{}
	if err := sess.RunHooks(context.Background(), types.SessionHookBeforeAgent, coresession.HookPayload{AgentRequest: req}); err != nil {
		t.Fatalf("RunHooks failed: %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "managed_tool" {
		t.Fatalf("expected managed hook tool to be installed, got %#v", req.Tools)
	}

	currentID, err := sessionMgr.GetCurrentID()
	if err != nil {
		t.Fatalf("GetCurrentID failed: %v", err)
	}
	if currentID != "" {
		t.Fatalf("managed session should not become current, got %q", currentID)
	}
}
