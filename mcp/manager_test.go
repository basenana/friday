package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/workspace"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

type managerWorkspaceConfig struct {
	root     string
	memory   string
	project  bool
	fallback []string
}

func (c managerWorkspaceConfig) WorkspacePath() string            { return c.root }
func (c managerWorkspaceConfig) WorkspaceFallbackPaths() []string { return c.fallback }
func (c managerWorkspaceConfig) ProjectScoped() bool              { return c.project }
func (c managerWorkspaceConfig) MemoryPath() string               { return c.memory }

func TestManagerStreamableHTTPToolsAndCache(t *testing.T) {
	t.Setenv("FRIDAY_TEST_MCP_TOKEN", "test-token")
	remote := mcpserver.NewMCPServer("test", "1")
	echo := mcpgo.NewTool("echo", mcpgo.WithDescription("echo text"), mcpgo.WithString("text"))
	remote.AddTool(echo, func(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]any)
		return mcpgo.NewToolResultText(fmt.Sprint(args["text"])), nil
	})
	transportHandler := mcpserver.NewStreamableHTTPServer(remote)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		transportHandler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	root := t.TempDir()
	configs := filepath.Join(root, "configs")
	if err := os.MkdirAll(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMCPConfig(t, filepath.Join(configs, "server.json"), fmt.Sprintf(`{"mcpServers":{"remote":{"url":%q,"headers":{"Authorization":"Bearer $FRIDAY_TEST_MCP_TOKEN"}}}}`, httpServer.URL+"/mcp"))
	manager, err := NewManager(ManagerConfig{ConfigRoots: homeConfigRoots(configs), CacheRoot: filepath.Join(root, "caches"), TrustPath: filepath.Join(root, "trust.json"), HandshakeTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	manager.Warmup(context.Background())
	status, err := manager.Inspect("remote")
	if err != nil || status.State != StateReady || status.Tools != 1 {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
	available := manager.Tools()
	if len(available) != 1 || available[0].Name != "mcp__remote__echo" {
		t.Fatalf("tools = %#v", available)
	}
	result, err := available[0].Handler(context.Background(), &tools.Request{Arguments: map[string]any{"text": "hello"}})
	if err != nil || !strings.Contains(tools.Res2Str(result), "hello") {
		t.Fatalf("call result=%#v err=%v", result, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	cached, err := NewManager(ManagerConfig{ConfigRoots: homeConfigRoots(configs), CacheRoot: filepath.Join(root, "caches"), TrustPath: filepath.Join(root, "trust.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()
	cachedStatus, _ := cached.Inspect("remote")
	if cachedStatus.State != StateCached || len(cached.Tools()) != 1 {
		t.Fatalf("cached status=%#v tools=%d", cachedStatus, len(cached.Tools()))
	}
}

func TestManagerProjectTrustIsDigestBound(t *testing.T) {
	root := t.TempDir()
	configs := filepath.Join(root, "mcp")
	if err := os.MkdirAll(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configs, "server.json")
	writeMCPConfig(t, configPath, `{"mcpServers":{"local":{"command":"definitely-not-started-before-trust"}}}`)
	options := ManagerConfig{ConfigRoots: []workspace.ResourceRoot{{Path: configs, Scope: workspace.ScopeProject}}, ProjectRoot: root, CacheRoot: filepath.Join(root, "cache"), TrustPath: filepath.Join(root, "trust.json"), HandshakeTimeout: 10 * time.Millisecond}
	manager, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := manager.Inspect("local")
	if status.State != StateBlocked || status.Trusted {
		t.Fatalf("initial status = %#v", status)
	}
	if err := manager.Trust(context.Background(), "local"); err == nil {
		t.Fatal("expected the deliberately missing command to fail after trust was saved")
	}
	_ = manager.Close()
	trusted, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = trusted.Inspect("local")
	if status.State == StateBlocked || !status.Trusted {
		t.Fatalf("trusted status = %#v", status)
	}
	_ = trusted.Close()
	writeMCPConfig(t, configPath, `{"mcpServers":{"local":{"command":"changed-command"}}}`)
	changed, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	status, _ = changed.Inspect("local")
	if status.State != StateBlocked || status.Trusted {
		t.Fatalf("changed status = %#v", status)
	}
}

func TestManagerSharedHomeWorkspaceRemainsGloballyTrusted(t *testing.T) {
	root := t.TempDir()
	configs := filepath.Join(root, "mcp")
	if err := os.MkdirAll(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMCPConfig(t, filepath.Join(configs, "server.json"), `{"mcpServers":{"global":{"command":"not-started-during-inspect"}}}`)
	ws := workspace.NewFromConfig(managerWorkspaceConfig{
		root:     root,
		memory:   t.TempDir(),
		project:  true,
		fallback: []string{root},
	})
	manager, err := NewManager(ManagerConfig{
		ConfigRoots: ws.MCPRoots(),
		ProjectRoot: t.TempDir(),
		CacheRoot:   filepath.Join(t.TempDir(), "cache"),
		TrustPath:   filepath.Join(t.TempDir(), "trust.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, err := manager.Inspect("global")
	if err != nil {
		t.Fatal(err)
	}
	if status.Project || !status.Trusted || status.State == StateBlocked {
		t.Fatalf("shared HOME status = %#v", status)
	}
}

func TestManagerStdioTools(t *testing.T) {
	root := t.TempDir()
	configs := filepath.Join(root, "configs")
	if err := os.MkdirAll(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"mcpServers":{"stdio":{"command":%q,"args":["-test.run=^TestMCPStdioHelperProcess$"],"env":{"FRIDAY_MCP_STDIO_HELPER":"1"}}}}`, os.Args[0])
	writeMCPConfig(t, filepath.Join(configs, "stdio.json"), config)
	manager, err := NewManager(ManagerConfig{ConfigRoots: homeConfigRoots(configs), CacheRoot: filepath.Join(root, "cache"), TrustPath: filepath.Join(root, "trust.json"), HandshakeTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Warmup(context.Background())
	status, _ := manager.Inspect("stdio")
	if status.State != StateReady || status.Tools != 1 {
		t.Fatalf("stdio status = %#v", status)
	}
	result, err := manager.Tools()[0].Handler(context.Background(), &tools.Request{Arguments: map[string]any{"text": "over-stdio"}})
	if err != nil || !strings.Contains(tools.Res2Str(result), "over-stdio") {
		t.Fatalf("stdio result=%#v err=%v", result, err)
	}
}

func TestManagerLegacySSETools(t *testing.T) {
	remote := mcpserver.NewMCPServer("sse-test", "1")
	echo := mcpgo.NewTool("echo", mcpgo.WithDescription("echo text"), mcpgo.WithString("text"))
	remote.AddTool(echo, func(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]any)
		return mcpgo.NewToolResultText(fmt.Sprint(args["text"])), nil
	})
	httpServer := mcpserver.NewTestServer(remote)
	defer httpServer.Close()
	root := t.TempDir()
	configs := filepath.Join(root, "configs")
	if err := os.MkdirAll(configs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMCPConfig(t, filepath.Join(configs, "sse.json"), fmt.Sprintf(`{"mcpServers":{"legacy":{"type":"sse","url":%q}}}`, httpServer.URL+"/sse"))
	manager, err := NewManager(ManagerConfig{ConfigRoots: homeConfigRoots(configs), CacheRoot: filepath.Join(root, "cache"), TrustPath: filepath.Join(root, "trust.json"), HandshakeTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.Warmup(context.Background())
	status, _ := manager.Inspect("legacy")
	if status.State != StateReady || status.Tools != 1 {
		t.Fatalf("SSE status = %#v", status)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("FRIDAY_MCP_STDIO_HELPER") != "1" {
		return
	}
	remote := mcpserver.NewMCPServer("stdio-test", "1")
	echo := mcpgo.NewTool("echo", mcpgo.WithDescription("echo text"), mcpgo.WithString("text"))
	remote.AddTool(echo, func(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]any)
		return mcpgo.NewToolResultText(fmt.Sprint(args["text"])), nil
	})
	if err := mcpserver.ServeStdio(remote); err != nil {
		t.Fatal(err)
	}
}

func writeMCPConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func homeConfigRoots(paths ...string) []workspace.ResourceRoot {
	roots := make([]workspace.ResourceRoot, 0, len(paths))
	for _, path := range paths {
		roots = append(roots, workspace.ResourceRoot{Path: path, Scope: workspace.ScopeHome})
	}
	return roots
}
