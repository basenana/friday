package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/tools"
)

type pollWaitRunCall struct {
	command string
	opts    ExecOptions
}

type pollWaitRunResponse struct {
	result *Result
	err    error
}

type pollWaitStubRunner struct {
	responses []pollWaitRunResponse
	defaults  pollWaitRunResponse
	calls     []pollWaitRunCall
}

func (s *pollWaitStubRunner) Run(ctx context.Context, command string, opts ExecOptions) (*Result, error) {
	s.calls = append(s.calls, pollWaitRunCall{
		command: command,
		opts:    opts,
	})

	if len(s.responses) > 0 {
		resp := s.responses[0]
		s.responses = s.responses[1:]
		return resp.result, resp.err
	}

	return s.defaults.result, s.defaults.err
}

func TestNewPollWaitTool_HasExpectedSchema(t *testing.T) {
	exec := NewExecutor(DefaultConfig())
	tool := NewPollWaitTool(exec, "/tmp/project")

	if tool.Name != "poll_wait" {
		t.Fatalf("tool.Name = %q, want poll_wait", tool.Name)
	}

	schema := tool.JsonSchema()
	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "command" {
		t.Fatalf("required = %#v, want [command]", required)
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties type = %T, want map[string]interface{}", schema["properties"])
	}

	for _, name := range []string{"command", "interval", "attempt_timeout", "max_timeout", "workdir"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("expected property %q in schema, got %#v", name, properties)
		}
	}
}

func TestPollWaitToolHandler_UsesDefaultsAndReturnsSuccess(t *testing.T) {
	baseWorkdir := t.TempDir()
	runner := &pollWaitStubRunner{
		defaults: pollWaitRunResponse{
			result: &Result{ExitCode: 0, Stdout: "ready\n"},
		},
	}
	handler := newPollWaitToolHandler(runner, baseWorkdir, 2*time.Second, 10*time.Second)

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command": "echo ready",
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, text = %q", pollWaitResultText(t, result))
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	if runner.calls[0].command != "echo ready" {
		t.Fatalf("command = %q, want echo ready", runner.calls[0].command)
	}
	if runner.calls[0].opts.Timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want %v", runner.calls[0].opts.Timeout, 2*time.Second)
	}
	if runner.calls[0].opts.Workdir != baseWorkdir {
		t.Fatalf("workdir = %q, want %q", runner.calls[0].opts.Workdir, baseWorkdir)
	}
	if !strings.Contains(pollWaitResultText(t, result), "ready") {
		t.Fatalf("result text = %q, want to contain ready", pollWaitResultText(t, result))
	}
}

func TestPollWaitToolHandler_RetriesRecoverableFailuresUntilSuccess(t *testing.T) {
	baseWorkdir := t.TempDir()
	runner := &pollWaitStubRunner{
		responses: []pollWaitRunResponse{
			{result: &Result{ExitCode: 1, Stderr: "not ready"}},
			{result: &Result{ExitCode: 124, TimedOut: true, Stderr: "Command timed out"}},
			{result: &Result{ExitCode: 0, Stdout: "ready\n"}},
		},
	}
	handler := newPollWaitToolHandler(runner, baseWorkdir, 2*time.Second, 10*time.Second)

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command":         "cat ready.txt",
			"interval":        "1ms",
			"attempt_timeout": "25ms",
			"max_timeout":     "250ms",
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, text = %q", pollWaitResultText(t, result))
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(runner.calls))
	}
	if !strings.Contains(pollWaitResultText(t, result), "ready") {
		t.Fatalf("result text = %q, want to contain ready", pollWaitResultText(t, result))
	}
}

func TestPollWaitToolHandler_PermissionDeniedFailsImmediately(t *testing.T) {
	baseWorkdir := t.TempDir()
	runner := &pollWaitStubRunner{
		defaults: pollWaitRunResponse{
			result: &Result{ExitCode: 1, Stderr: "Permission denied: command 'sudo' matched deny rule: sudo"},
			err:    ErrPermissionDenied,
		},
	}
	handler := newPollWaitToolHandler(runner, baseWorkdir, 2*time.Second, 10*time.Second)

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command":     "sudo ls",
			"interval":    "1ms",
			"max_timeout": "250ms",
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, text = %q", pollWaitResultText(t, result))
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	if !strings.Contains(pollWaitResultText(t, result), "Permission denied") {
		t.Fatalf("result text = %q, want permission denied", pollWaitResultText(t, result))
	}
}

func TestPollWaitToolHandler_InvalidWorkdirFailsImmediately(t *testing.T) {
	runner := &pollWaitStubRunner{}
	handler := newPollWaitToolHandler(runner, t.TempDir(), 2*time.Second, 10*time.Second)

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command": "echo ready",
			"workdir": filepath.Join(t.TempDir(), "missing"),
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, text = %q", pollWaitResultText(t, result))
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(runner.calls))
	}
	if !strings.Contains(strings.ToLower(pollWaitResultText(t, result)), "workdir") {
		t.Fatalf("result text = %q, want to mention workdir", pollWaitResultText(t, result))
	}
}

func TestPollWaitToolHandler_StopsAtMaxTimeout(t *testing.T) {
	baseWorkdir := t.TempDir()
	runner := &pollWaitStubRunner{
		defaults: pollWaitRunResponse{
			result: &Result{ExitCode: 1, Stderr: "not ready"},
		},
	}
	handler := newPollWaitToolHandler(runner, baseWorkdir, 2*time.Second, 10*time.Second)

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command":         "cat ready.txt",
			"interval":        "5ms",
			"attempt_timeout": "10ms",
			"max_timeout":     "20ms",
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, text = %q", pollWaitResultText(t, result))
	}
	if len(runner.calls) < 2 {
		t.Fatalf("calls = %d, want at least 2", len(runner.calls))
	}
	text := strings.ToLower(pollWaitResultText(t, result))
	if !strings.Contains(text, "timed out") {
		t.Fatalf("result text = %q, want timed out", pollWaitResultText(t, result))
	}
	if !strings.Contains(text, "not ready") {
		t.Fatalf("result text = %q, want to contain last failure", pollWaitResultText(t, result))
	}
}

func pollWaitResultText(t *testing.T, result *tools.Result) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	text, ok := result.Content[0].(tools.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", result.Content[0])
	}
	return text.Text
}
