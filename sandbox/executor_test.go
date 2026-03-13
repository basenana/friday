package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecuteSimpleCommand(t *testing.T) {
	cfg := DefaultConfig()
	exec := NewExecutor(cfg)

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

func TestExecutePermissionDenied(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Permissions.Allow = []string{"echo"}
	cfg.Permissions.Deny = []string{}
	exec := NewExecutor(cfg)

	ctx := context.Background()
	result, err := exec.Run(ctx, "sudo ls", ExecOptions{})

	if err != ErrPermissionDenied {
		t.Errorf("Executor.Run error = %v, want ErrPermissionDenied", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.ExitCode == 0 {
		t.Errorf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Permission denied") {
		t.Errorf("Stderr = %q, want to contain 'Permission denied'", result.Stderr)
	}
}

func TestExecuteWorkdir(t *testing.T) {
	cfg := DefaultConfig()
	exec := NewExecutor(cfg)

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
	cfg := DefaultConfig()
	exec := NewExecutor(cfg)

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
