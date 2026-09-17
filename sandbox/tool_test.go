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
	tool := NewBashTool(NewExecutor(DefaultConfig()), t.TempDir())
	for _, name := range []string{"command", "timeout", "workdir"} {
		if _, exists := tool.InputSchema.Properties[name]; !exists {
			t.Fatalf("parameter %q missing", name)
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
	if got != nested {
		t.Fatalf("resolveToolWorkdir() = %q, want %q", got, nested)
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

	handler := bashToolHandler(exec, base)

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

	result, err := bashToolHandler(exec, base)(context.Background(), &tools.Request{
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
	tool := NewBashTool(exec, t.TempDir())
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
	tool := NewBashTool(exec, t.TempDir())

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
