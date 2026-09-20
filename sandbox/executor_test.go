package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newTestExecutor() *Executor {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	return NewExecutor(cfg)
}

func TestExecuteSimpleCommand(t *testing.T) {
	exec := newTestExecutor()

	ctx := context.Background()
	result, err := exec.Run(ctx, "echo hello", ExecOptions{})

	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("Stdout = %q, want to contain 'hello'", result.Stdout)
	}
}

func TestExecuteCommandWithExitCode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = append(cfg.Permissions.Allow, "false")
	exec := NewExecutor(cfg)

	ctx := context.Background()
	result, err := exec.Run(ctx, "false", ExecOptions{})

	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Errorf("ExitCode = %d, want non-zero", result.ExitCode)
	}
}

func TestExecuteTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = append(cfg.Permissions.Allow, "sleep")
	exec := NewExecutor(cfg)

	ctx := context.Background()
	// Use 5 second timeout with 2 second sleep
	result, err := exec.Run(ctx, "sleep 2", ExecOptions{
		Timeout: 500 * time.Millisecond,
	})

	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if !result.TimedOut {
		t.Errorf("TimedOut = %v, want true", result.TimedOut)
	}
	if result.ExitCode != 124 {
		t.Errorf("ExitCode = %d, want 124 (timeout)", result.ExitCode)
	}
}

func TestExecuteTimeoutKillsProcessGroupHoldingOutputPipe(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	exec := NewExecutor(cfg)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf("sh -c 'sleep 30 & echo $! > %q' | cat", pidFile)

	started := time.Now()
	result, err := exec.Run(context.Background(), command, ExecOptions{
		Timeout: 500 * time.Millisecond,
	})
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if !result.TimedOut || result.ExitCode != 124 {
		t.Fatalf("result = %+v, want timed out with exit code 124", result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout returned after %s, want <= 3s", elapsed)
	}

	pid := readPIDFile(t, pidFile)
	waitForPIDExit(t, pid, 2*time.Second)
}

func TestExecuteCancellationKillsProcessGroup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	exec := NewExecutor(cfg)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf("sh -c 'sleep 30 & echo $! > %q' | cat", pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	time.AfterFunc(500*time.Millisecond, cancel)
	started := time.Now()
	result, err := exec.Run(ctx, command, ExecOptions{Timeout: 30 * time.Second})
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if result.TimedOut {
		t.Fatalf("result = %+v, cancellation must not be reported as timeout", result)
	}
	if result.ExitCode == 0 {
		t.Fatalf("result = %+v, want non-zero exit code after cancellation", result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("cancellation returned after %s, want <= 3s", elapsed)
	}

	pid := readPIDFile(t, pidFile)
	waitForPIDExit(t, pid, 2*time.Second)
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid file %q: %v", data, err)
	}
	return pid
}

func waitForPIDExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if err != nil {
			t.Fatalf("probe process %d: %v", pid, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d still exists after %s", pid, timeout)
}

func TestExecutePermissionDenied(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{"echo"}
	cfg.Permissions.Deny = []string{}
	exec := NewExecutor(cfg)

	ctx := context.Background()
	result, err := exec.Run(ctx, "sudo ls", ExecOptions{})

	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("Executor.Run error = %v, want ErrPermissionDenied", err)
	}
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("Executor.Run error = %T, want *DeniedError", err)
	}
	if denied.Command != "sudo" || denied.ExplicitDeny {
		t.Errorf("denied = %+v, want grantable denial for sudo", denied)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.ExitCode == 0 {
		t.Errorf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "not in allow list") {
		t.Errorf("Stderr = %q, want to contain the denial reason", result.Stderr)
	}
}

func TestExecuteSkipsPermissionsWhenIsolationDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Permissions.Allow = []string{"echo"}
	cfg.Permissions.Deny = []string{"printf"}
	cfg.DisableIsolation()
	exec := NewExecutor(cfg)

	result, err := exec.Run(context.Background(), "printf unrestricted", ExecOptions{})
	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "unrestricted" {
		t.Fatalf("result = %+v, want unrestricted output", result)
	}
}

func TestExecuteInheritsProcessEnvironmentWhenIsolationDisabled(t *testing.T) {
	t.Setenv("FRIDAY_OUTER_SANDBOX_SECRET", "visible-to-child")
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	exec := NewExecutor(cfg)

	result, err := exec.Run(context.Background(), `printf %s "$FRIDAY_OUTER_SANDBOX_SECRET"`, ExecOptions{})
	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if result.Stdout != "visible-to-child" {
		t.Fatalf("Stdout = %q, want inherited environment value", result.Stdout)
	}
}

func TestExecuteWorkdir(t *testing.T) {
	exec := newTestExecutor()

	ctx := context.Background()
	result, err := exec.Run(ctx, "pwd", ExecOptions{
		Workdir: "/tmp",
	})

	if err != nil {
		t.Fatalf("Executor.Run error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "/tmp") {
		t.Errorf("Stdout = %q, want to contain '/tmp'", result.Stdout)
	}
}

type unavailableTestSandbox struct {
	called bool
}

func (s *unavailableTestSandbox) WrapCommand(string, ExecOptions) (string, func(), error) {
	s.called = true
	return "exit 99", func() {}, nil
}

func (*unavailableTestSandbox) IsAvailable() bool { return false }
func (*unavailableTestSandbox) Name() string      { return "test-unavailable" }

func TestUnavailableSandboxFailsClosed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = true
	exec := NewExecutor(cfg)
	backend := &unavailableTestSandbox{}
	exec.sandbox = backend

	result, err := exec.Run(context.Background(), "echo fallback-ok", ExecOptions{})
	if !errors.Is(err, ErrSandboxUnavailable) {
		t.Fatalf("error = %v, want ErrSandboxUnavailable", err)
	}
	if backend.called {
		t.Fatal("unavailable sandbox backend was still used")
	}
	if result == nil || result.ExitCode == 0 || !strings.Contains(result.Stderr, "sandbox") {
		t.Fatalf("fail-closed result = %+v", result)
	}
}

func TestTruncateOutputBytes(t *testing.T) {
	longOutput := strings.Repeat("a", MaxOutputBytes+1000)
	truncated := truncateOutput(longOutput)
	if len(truncated) > MaxOutputBytes {
		t.Errorf("truncateOutput length = %d, want <= %d", len(truncated), MaxOutputBytes)
	}
}

func TestTruncateOutputLines(t *testing.T) {
	lines := make([]string, MaxOutputLines+100)
	for i := range lines {
		lines[i] = "line"
	}
	multiLine := strings.Join(lines, "\n")
	truncated := truncateOutput(multiLine)
	outputLines := strings.Count(truncated, "\n") + 1
	if outputLines > MaxOutputLines {
		t.Errorf("truncateOutput lines = %d, want <= %d", outputLines, MaxOutputLines)
	}
}

func TestParseTimeoutConfig(t *testing.T) {
	tests := []struct {
		config string
		want   time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"30s", 30 * time.Second},
		{"1h", time.Hour},
		{"", DefaultTimeout},
	}

	for _, tt := range tests {
		cfg := &Config{
			Sandbox: SandboxConfig{
				Defaults: DefaultsConfig{Timeout: tt.config},
			},
		}
		exec := NewExecutor(cfg)
		got := exec.parseTimeout()
		if got != tt.want {
			t.Errorf("parseTimeout(%q) = %v, want %v", tt.config, got, tt.want)
		}
	}
}

func TestExecutorCheckPermission(t *testing.T) {
	exec := newTestExecutor()

	decision, reason, err := exec.CheckPermission("echo hello")
	if err != nil {
		t.Errorf("CheckPermission error = %v", err)
	}
	if decision != Allow {
		t.Errorf("CheckPermission decision = %v, want Allow", decision)
	}
	if reason == "" {
		t.Error("CheckPermission reason should not be empty")
	}
}

func TestExecutorSandboxName(t *testing.T) {
	cfg := DefaultConfig()
	exec := NewExecutor(cfg)

	name := exec.SandboxName()
	if name == "" {
		t.Error("SandboxName should not be empty")
	}
}

func TestGetOSInfo(t *testing.T) {
	info := GetOSInfo()
	if info == "" {
		t.Error("GetOSInfo should not return empty string")
	}
}

func TestBuildCommandEnvDoesNotInheritHostEnv(t *testing.T) {
	t.Setenv("FRIDAY_TEST_API_KEY", "super-secret-value")

	env := buildCommandEnv(nil, "")
	for _, entry := range env {
		if strings.Contains(entry, "FRIDAY_TEST_API_KEY") || strings.Contains(entry, "super-secret-value") {
			t.Errorf("child env must not inherit host secrets: %q", entry)
		}
	}

	// Integration: the executed command must not see the host secret either.
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = append(cfg.Permissions.Allow, "env")
	exec := NewExecutor(cfg)
	result, err := exec.Run(context.Background(), "env", ExecOptions{})
	if err != nil {
		t.Fatalf("Executor.Run error: %v", err)
	}
	if strings.Contains(result.Stdout+result.Stderr, "super-secret-value") {
		t.Errorf("executed command saw the host secret: %q", result.Stdout)
	}
}

func TestResolveExecutionHomeUsesExplicitChildHome(t *testing.T) {
	home, err := resolveExecutionHome("/option/home", []string{"HOME=/child/home"})
	if err != nil {
		t.Fatal(err)
	}
	if home != "/child/home" {
		t.Fatalf("execution HOME = %q, want /child/home", home)
	}
	for _, env := range [][]string{{"HOME="}, {"HOME=relative"}} {
		if _, err := resolveExecutionHome("/option/home", env); err == nil {
			t.Fatalf("expected invalid HOME %q to fail", env[0])
		}
	}
}

func TestBuildCommandEnvPreservesExplicitHome(t *testing.T) {
	env := buildCommandEnv([]string{"HOME=/custom/home"}, "/sandbox/home")

	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			if entry != "HOME=/custom/home" {
				t.Errorf("HOME entry = %q, want the explicit HOME to be preserved", entry)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected HOME to be present in the child env")
	}
}

func TestBuildCommandEnvOverridesHomeFromHomeDir(t *testing.T) {
	env := buildCommandEnv(nil, "/sandbox/home")

	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			if entry != "HOME=/sandbox/home" {
				t.Errorf("HOME entry = %q, want HOME=/sandbox/home", entry)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected HOME to be overridden by HomeDir")
	}
}

func TestBuildCommandEnvKeepsSafeBase(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "C.UTF-8")

	env := buildCommandEnv([]string{"CUSTOM=1"}, "")
	joined := strings.Join(env, "\n")
	for _, want := range []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "LC_ALL=C.UTF-8", "CUSTOM=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("child env missing %q: %v", want, env)
		}
	}
}
