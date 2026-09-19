package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/tools"
)

func TestBashToolKeepsFunctionalOptions(t *testing.T) {
	tool := NewBashTool(NewExecutor(DefaultConfig()), t.TempDir(), nil)
	for _, name := range []string{"command", "timeout", "workdir"} {
		if _, exists := tool.InputSchema.Properties[name]; !exists {
			t.Fatalf("parameter %q missing", name)
		}
	}
}

func TestCommandToolDefinitionsAreModelReady(t *testing.T) {
	exec := NewExecutor(DefaultConfig())
	manager := NewTaskManager(exec)
	toolList := []*tools.Tool{NewBashTool(exec, t.TempDir(), nil)}
	toolList = append(toolList, NewBackgroundTaskTools(manager, t.TempDir())...)
	for _, tool := range toolList {
		if issues := tool.ValidateDefinition(2); len(issues) != 0 {
			t.Fatalf("%s definition: %v", tool.Name, issues)
		}
		if len(tool.Examples) == 0 {
			t.Fatalf("%s has no model-facing example", tool.Name)
		}
	}
}

func TestResolveToolWorkdirInsideBaseIsAllowed(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	got, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": nested})
	if err != nil {
		t.Fatalf("resolveToolWorkdir() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolveToolWorkdir() = %q, want %q", got, want)
	}
}

func TestResolveToolWorkdirResolvesRelativePathFromBase(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": "nested"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("workdir = %q, want %q", got, want)
	}
}

func TestResolveToolWorkdirRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(base, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": "escape"}); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestResolveToolWorkdirOutsideBaseIsRejected(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir() // exists, is a dir, but outside the base workdir

	if _, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": outside}); err == nil {
		t.Fatal("expected error for workdir outside the base workdir")
	}
}

func TestResolveToolWorkdirRejectsEtc(t *testing.T) {
	base := t.TempDir()

	if _, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": "/etc"}); err == nil {
		t.Fatal("expected error for workdir=/etc")
	}
}

func TestResolveToolWorkdirRejectsHomeExpansionEscape(t *testing.T) {
	base := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir failed: %v", err)
	}
	// Only meaningful when the host home is not inside the base workdir.
	if filepath.Clean(base) == filepath.Clean(home) || isWithinWorkdir(base, home) {
		t.Skip("base workdir contains the host home directory")
	}

	if _, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": "~"}); err == nil {
		t.Fatal("expected error for workdir=~ escaping the base workdir")
	}
	if _, err := resolveToolWorkdir(base, map[string]interface{}{"workdir": "~/.ssh"}); err == nil {
		t.Fatal("expected error for workdir=~/.ssh escaping the base workdir")
	}
}

func TestBashToolHandlerRejectsWorkdirOutsideBase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = append(cfg.Permissions.Allow, "pwd")
	exec := NewExecutor(cfg)
	base := t.TempDir()

	handler := bashToolHandler(exec, base, nil)

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command": "pwd",
			"workdir": "/etc",
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for workdir outside the base workdir")
	}

	// Sanity check: a workdir inside the base still executes.
	inside := filepath.Join(base, "sub")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	result, err = handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command": "pwd",
			"workdir": inside,
		},
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(t, result))
	}
}

func TestBashToolHandlerAllowsWorkdirOutsideBaseWhenIsolationDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	exec := NewExecutor(cfg)
	base := t.TempDir()
	outside := t.TempDir()

	result, err := bashToolHandler(exec, base, nil)(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"command": "pwd",
			"workdir": outside,
		},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", textResult(t, result))
	}
	if !strings.Contains(textResult(t, result), outside) {
		t.Fatalf("result = %q, want outside workdir %q", textResult(t, result), outside)
	}
}

func TestBashToolHandlerReturnsTimeoutResult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	exec := NewExecutor(cfg)

	started := time.Now()
	tool := NewBashTool(exec, t.TempDir(), nil)
	result, err := tools.NewInvoker().Invoke(context.Background(), tool, &tools.Request{
		Arguments: map[string]interface{}{
			"command": "sleep 30",
			"timeout": "200ms",
		},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %+v, want tool error", result)
	}
	if !result.TimedOut || result.Status != tools.ResultStatusTimedOut || result.TimeoutKind != tools.TimeoutKindDeclared {
		t.Fatalf("timeout metadata = %+v, want declared timeout", result)
	}
	if result.ExitCode == nil || *result.ExitCode != 124 {
		t.Fatalf("exit code = %v, want 124", result.ExitCode)
	}
	if !strings.Contains(strings.ToLower(textResult(t, result)), "timed out") {
		t.Fatalf("result = %q, want timeout message", textResult(t, result))
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("handler returned after %s, want <= 3s", elapsed)
	}
}

func TestBashToolDeclaredTimeoutOverridesShorterConfigDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	cfg.Sandbox.Defaults.Timeout = "20ms"
	exec := NewExecutor(cfg)
	tool := NewBashTool(exec, t.TempDir(), nil)

	result, err := tools.NewInvoker().Invoke(context.Background(), tool, &tools.Request{
		Arguments: map[string]interface{}{
			"command": "sleep 0.05 && printf done",
			"timeout": "200ms",
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.IsError || textResult(t, result) != "done" {
		t.Fatalf("result = %+v, want successful command", result)
	}
}

func TestBashToolHeadlessDenialSuggestsCLI(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	tool := NewBashTool(exec, t.TempDir(), nil) // no approver: headless

	result, err := tools.NewInvoker().Invoke(context.Background(), tool, &tools.Request{
		Arguments: map[string]interface{}{"command": "printf headless"},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %+v, want tool error", result)
	}
	text := textResult(t, result)
	if !strings.Contains(text, "friday sandbox allow") {
		t.Fatalf("result = %q, want headless CLI suggestion", text)
	}
}

func TestBashToolApprovalPersistExecutesAndWritesOverlay(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	overlay := approvalOverlayPath(t)
	approver := NewCommandApprover(exec.Permission(), overlay)
	approver.Bind(&fakePrompter{queue: []string{approvalValuePersist}})
	tool := NewBashTool(exec, t.TempDir(), approver)

	result, err := tools.NewInvoker().Invoke(context.Background(), tool, &tools.Request{
		Arguments: map[string]interface{}{"command": "printf via-approval"},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result = %+v, want approved execution", result)
	}
	if !strings.Contains(textResult(t, result), "via-approval") {
		t.Fatalf("result = %q, want real command output", textResult(t, result))
	}
	allow, err := LoadProjectAllow(overlay)
	if err != nil || len(allow) != 1 || allow[0] != "printf" {
		t.Fatalf("overlay allow = %#v, err = %v; want persisted [printf]", allow, err)
	}
}

func TestBashToolApprovalDeniedByUser(t *testing.T) {
	exec := approvalTestExecutor([]string{"echo"}, nil)
	approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
	approver.Bind(&fakePrompter{queue: []string{approvalValueDeny}})
	tool := NewBashTool(exec, t.TempDir(), approver)

	result, err := tools.NewInvoker().Invoke(context.Background(), tool, &tools.Request{
		Arguments: map[string]interface{}{"command": "printf denied"},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %+v, want tool error", result)
	}
	if text := textResult(t, result); !strings.Contains(text, "declined") {
		t.Fatalf("result = %q, want user-declined hint", text)
	}
}
