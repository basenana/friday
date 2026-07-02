//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/mcp"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	mcpc "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
)

// startMockMCPServer starts an in-process MCP StreamableHTTP server with the
// provided tools. Returns (endpointURL, shutdown).
func startMockMCPServer(t *testing.T, tool *mcpgo.Tool, handler mcpserver.ToolHandlerFunc) (string, func()) {
	t.Helper()
	mcpSrv := mcpserver.NewMCPServer("test-mcp", "1.0.0")
	mcpSrv.AddTool(*tool, handler)
	httpSrv := mcpserver.NewStreamableHTTPServer(mcpSrv)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpSrv.Start(addr)
	}()

	// Wait until reachable.
	url := "http://" + addr + "/mcp"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		<-done
	}
	return url, cleanup
}

// initMCPClient connects, starts, and initializes a fresh MCP client against
// url. Returns the ready client.
func initMCPClient(ctx context.Context, url string) (*mcpc.Client, error) {
	tr, err := mcptransport.NewStreamableHTTP(url)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	c := mcpc.NewClient(tr)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	if _, err := c.Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ClientInfo: mcpgo.Implementation{Name: "e2e-test", Version: "1.0.0"},
		},
	}); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	return c, nil
}

// TestMCP_ConnectAndListTools verifies the friday mcp.Server Connect +
// InitTools chain returns the expected tools from a mock server.
func TestMCP_ConnectAndListTools(t *testing.T) {
	echoTool := mcpgo.NewTool("echo_tool",
		mcpgo.WithDescription("Echoes back the input text"),
		mcpgo.WithString("text"),
	)
	url, stop := startMockMCPServer(t, &echoTool, func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		text, _ := args["text"].(string)
		return mcpgo.NewToolResultText("echo: " + text), nil
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv := &mcp.Server{
		Name:     "test",
		Describe: "test",
		SSE:      &mcp.MCPSse{Endpoint: url},
	}
	if err := srv.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Initialize the underlying client (Connect doesn't do this itself).
	if _, err := srv.Client().Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{ClientInfo: mcpgo.Implementation{Name: "e2e", Version: "1.0"}},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools_, err := srv.InitTools(ctx)
	if err != nil {
		t.Fatalf("InitTools: %v", err)
	}
	if len(tools_) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools_))
	}
	if tools_[0].Name != "echo_tool" {
		t.Errorf("expected echo_tool, got %q", tools_[0].Name)
	}
	if tools_[0].Description == "" {
		t.Errorf("expected non-empty Description")
	}
	if tools_[0].Handler == nil {
		t.Errorf("expected non-nil Handler")
	}
}

// TestMCP_ToolInvocation verifies the adapted handler can actually invoke the
// mock tool and return its result.
func TestMCP_ToolInvocation(t *testing.T) {
	echoTool := mcpgo.NewTool("echo_tool",
		mcpgo.WithDescription("Echoes back the input text"),
		mcpgo.WithString("text"),
	)
	url, stop := startMockMCPServer(t, &echoTool, func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		text, _ := args["text"].(string)
		return mcpgo.NewToolResultText("echo: " + text), nil
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv := &mcp.Server{
		Name: "test", Describe: "test",
		SSE: &mcp.MCPSse{Endpoint: url},
	}
	if err := srv.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := srv.Client().Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{ClientInfo: mcpgo.Implementation{Name: "e2e", Version: "1.0"}},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	toos_, err := srv.InitTools(ctx)
	if err != nil {
		t.Fatalf("InitTools: %v", err)
	}
	result, err := toos_[0].Handler(ctx, &tools.Request{Arguments: map[string]any{"text": "hello-mcp"}})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	got := tools.Res2Str(result)
	if !strings.Contains(got, "hello-mcp") {
		t.Errorf("expected result to contain 'hello-mcp', got %q", got)
	}
}

// TestMCP_ToolError verifies that a mock tool returning an MCP error is
// propagated by the adapted handler.
func TestMCP_ToolError(t *testing.T) {
	errTool := mcpgo.NewTool("error_tool",
		mcpgo.WithDescription("Always fails"),
	)
	url, stop := startMockMCPServer(t, &errTool, func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return nil, errors.New("simulated mcp error")
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv := &mcp.Server{
		Name: "test", Describe: "test",
		SSE: &mcp.MCPSse{Endpoint: url},
	}
	if err := srv.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := srv.Client().Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{ClientInfo: mcpgo.Implementation{Name: "e2e", Version: "1.0"}},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	toos_, err := srv.InitTools(ctx)
	if err != nil {
		t.Fatalf("InitTools: %v", err)
	}
	_, err = toos_[0].Handler(ctx, &tools.Request{Arguments: map[string]any{}})
	if err == nil {
		t.Error("expected error from error_tool, got nil")
	}
}

// TestMCP_ConnectFail verifies that Connect against an unreachable endpoint
// does not silently succeed in a way that hides the problem.
func TestMCP_ConnectFail(t *testing.T) {
	srv := &mcp.Server{
		Name: "test", Describe: "test",
		SSE: &mcp.MCPSse{Endpoint: "http://127.0.0.1:1/nope"},
	}
	// Connect only constructs the client; it always succeeds for any URL
	// because no network call happens yet. The error surfaces on actual use.
	if err := srv.Connect(); err != nil {
		t.Fatalf("Connect should not return error for unreachable URL: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Initialization must fail because the endpoint is unreachable.
	_, err := srv.Client().Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{ClientInfo: mcpgo.Implementation{Name: "e2e", Version: "1.0"}},
	})
	if err == nil {
		t.Error("expected Initialize to fail on unreachable endpoint, got nil")
	}
}

// keep imports
var _ = mcpc.NewClient
