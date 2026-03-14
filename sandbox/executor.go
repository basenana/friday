package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultTimeout is the default command timeout
	DefaultTimeout = 5 * time.Minute
	// MaxOutputLines is the maximum number of output lines to keep
	MaxOutputLines = 300
	// MaxOutputBytes is the maximum output size in bytes
	MaxOutputBytes = 512 * 1024 // 512KB
)

// Executor handles command execution with sandboxing
type Executor struct {
	config  *Config
	perm    *Permission
	sandbox Sandbox
}

// NewExecutor creates a new Executor
func NewExecutor(cfg *Config) *Executor {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	perm := NewPermission(cfg)

	var sandbox Sandbox
	if cfg.Sandbox.Enabled {
		sandbox = NewSandbox(cfg)
	} else {
		sandbox = &NoSandbox{}
	}

	return &Executor{
		config:  cfg,
		perm:    perm,
		sandbox: sandbox,
	}
}

// Run executes a command with sandboxing and permission checks
func (e *Executor) Run(ctx context.Context, cmd string, opts ExecOptions) (*Result, error) {
	// 1. Check permissions
	decision, reason, err := e.perm.CheckWithReason(cmd)
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if decision == Deny {
		return &Result{
			ExitCode: 1,
			Stderr:   fmt.Sprintf("Permission denied: %s", reason),
		}, ErrPermissionDenied
	}

	// 2. Set default timeout
	if opts.Timeout == 0 {
		opts.Timeout = e.parseTimeout()
	}

	// 3. Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// 4. Wrap command with sandbox
	wrappedCmd, cleanup, err := e.sandbox.WrapCommand(cmd, opts)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to wrap command: %w", err)
	}

	// 5. Execute command
	result, err := e.execute(ctx, wrappedCmd, opts)
	if err != nil {
		return result, err
	}

	return result, nil
}

// execute runs the actual command
func (e *Executor) execute(ctx context.Context, cmdStr string, opts ExecOptions) (*Result, error) {
	// Use bash -c to handle complex commands
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)

	// Set working directory
	if opts.Workdir != "" {
		cmd.Dir = opts.Workdir
	}

	// Set environment
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	} else {
		cmd.Env = os.Environ()
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Handle stdin if provided
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	// Run the command
	err := cmd.Run()

	// Build result
	result := &Result{
		Stdout: truncateOutput(stdout.String()),
		Stderr: truncateOutput(stderr.String()),
	}

	// Handle exit code
	if err != nil {
		// Check for timeout first - context timeout takes priority
		if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = 124 // Standard timeout exit code
			result.TimedOut = true
			result.Stderr = "Command timed out"
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}

	return result, nil
}

// parseTimeout parses the timeout from config
func (e *Executor) parseTimeout() time.Duration {
	s := e.config.Sandbox.Defaults.Timeout
	if s == "" {
		return DefaultTimeout
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return DefaultTimeout
	}
	return d
}

// truncateOutput truncates output to max lines and max bytes
func truncateOutput(output string) string {
	if len(output) > MaxOutputBytes {
		output = output[len(output)-MaxOutputBytes:]
	}

	lines := strings.Split(output, "\n")
	if len(lines) > MaxOutputLines {
		lines = lines[len(lines)-MaxOutputLines:]
		// Add truncation indicator
		lines[0] = "... (output truncated)"
	}

	return strings.Join(lines, "\n")
}

// CheckPermission checks if a command would be allowed without executing it
func (e *Executor) CheckPermission(cmd string) (Decision, string, error) {
	return e.perm.CheckWithReason(cmd)
}

// SandboxName returns the name of the sandbox being used
func (e *Executor) SandboxName() string {
	return e.sandbox.Name()
}

// IsSandboxAvailable checks if the sandbox is available
func (e *Executor) IsSandboxAvailable() bool {
	return e.sandbox.IsAvailable()
}

// GetOSInfo returns information about the current OS for sandbox selection
func GetOSInfo() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

// ValidateWorkdir validates and expands the working directory
func ValidateWorkdir(workdir string) (string, error) {
	if workdir == "" {
		return os.Getwd()
	}

	// Expand ~ to home directory
	if strings.HasPrefix(workdir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		workdir = strings.Replace(workdir, "~", home, 1)
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absPath)
	}

	return absPath, nil
}
