package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/logger"
	coretools "github.com/basenana/friday/core/tools"
)

const (
	// DefaultTimeout is the default command timeout
	DefaultTimeout = 5 * time.Minute
	// commandWaitDelay bounds the time spent waiting for descendant processes
	// that outlive the command and keep its output pipes open.
	commandWaitDelay = time.Second
	// MaxOutputLines is the maximum number of output lines to keep
	MaxOutputLines = 300
	// MaxOutputBytes is the maximum output size in bytes
	MaxOutputBytes = 512 * 1024 // 512KB
)

var (
	ErrTimeoutOutOfRange  = errors.New("command timeout must be greater than zero and no more than 15m")
	ErrSandboxUnavailable = errors.New("configured OS sandbox is unavailable")
)

// Executor handles command execution with sandboxing
type Executor struct {
	config  *Config
	perm    *Permission
	sandbox Sandbox

	warnUnsandboxedOnce sync.Once
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
	if !e.config.IsolationDisabled() {
		decision, err := e.perm.CheckWithReason(cmd)
		if decision == Deny {
			var denied *DeniedError
			if errors.As(err, &denied) {
				return &Result{
					ExitCode: 1,
					Stderr:   denied.Error(),
				}, denied
			}
			return nil, fmt.Errorf("permission check failed: %w", err)
		}
		if err != nil {
			return nil, fmt.Errorf("permission check failed: %w", err)
		}
	}

	// 2. Set default timeout
	if opts.Timeout == 0 {
		opts.Timeout = e.parseTimeout()
	}
	if opts.Timeout <= 0 || opts.Timeout > coretools.MaxDeclaredToolTimeout {
		return &Result{ExitCode: 1, Stderr: ErrTimeoutOutOfRange.Error()}, ErrTimeoutOutOfRange
	}

	// 3. Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// 4. Wrap command with sandbox
	if e.config.Sandbox.Enabled && !e.sandbox.IsAvailable() {
		e.warnUnsandboxedOnce.Do(func() {
			logger.New("sandbox").Warnw("sandbox is unavailable; command execution is disabled",
				"sandbox", e.sandbox.Name(),
			)
		})
		return &Result{ExitCode: 1, Stderr: ErrSandboxUnavailable.Error()}, fmt.Errorf("%w: %s", ErrSandboxUnavailable, e.sandbox.Name())
	}
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
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return terminateProcessGroup(commandProcessGroupID(cmd), true)
	}
	cmd.WaitDelay = commandWaitDelay

	// Set working directory
	if opts.Workdir != "" {
		cmd.Dir = opts.Workdir
	}

	// Set environment: build the child environment from a minimal safe base
	// plus anything the caller passed explicitly.
	cmd.Env = e.buildCommandEnv(opts.Env, opts.HomeDir)

	// Capture output with a hard cap per stream so a runaway command cannot
	// exhaust memory.
	stdout := newBoundedOutputBuffer(maxCaptureBytes)
	stderr := newBoundedOutputBuffer(maxCaptureBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Handle stdin if provided
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	// Run the command
	err := cmd.Run()

	// Build result
	result := &Result{}
	result.Stdout, result.StdoutTruncated = truncateOutputWithFlag(stdout.String())
	if stdout.Truncated() {
		result.StdoutTruncated = true
	}
	result.Stderr, result.StderrTruncated = truncateOutputWithFlag(stderr.String())
	if stderr.Truncated() {
		result.StderrTruncated = true
	}

	// Handle exit code
	if err != nil {
		// Check for timeout first - context timeout takes priority
		if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = 124 // Standard timeout exit code
			result.TimedOut = true
			if strings.TrimSpace(result.Stderr) == "" {
				result.Stderr = "Command timed out"
			} else {
				result.Stderr = "Command timed out\n" + result.Stderr
			}
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}

	return result, nil
}

// maxCaptureBytes is the hard cap for a single output stream (8MB).
const maxCaptureBytes = 8 * 1024 * 1024

type boundedOutputBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedOutputBuffer(limit int) *boundedOutputBuffer {
	return &boundedOutputBuffer{limit: limit}
}

func (b *boundedOutputBuffer) Write(p []byte) (int, error) {
	requested := len(p)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		toWrite := p
		if len(toWrite) > remaining {
			toWrite = toWrite[:remaining]
		}
		_, _ = b.buffer.Write(toWrite)
	}
	if requested > 0 && (remaining <= 0 || requested > remaining) {
		b.truncated = true
	}
	return requested, nil
}

func (b *boundedOutputBuffer) String() string {
	return b.buffer.String()
}

func (b *boundedOutputBuffer) Truncated() bool {
	return b.truncated
}

// buildCommandEnv builds the child environment from a minimal, safe base
// (PATH, TERM, TZ, LANG/LC_*, HOME) plus the caller-provided entries. The
// host environment is deliberately not inherited: sandboxed commands must not
// see host credentials such as API keys.
//
// HOME is only overridden by homeDir when the caller did not set an explicit
// HOME entry in extraEnv.
func buildCommandEnv(extraEnv []string, homeDir string) []string {
	env := mergeEnvLists(safeChildEnvBase(), extraEnv)
	if strings.TrimSpace(homeDir) != "" && !envListHas(extraEnv, "HOME") {
		env = mergeEnvLists(env, []string{"HOME=" + strings.TrimSpace(homeDir)})
	}
	return env
}

func (e *Executor) buildCommandEnv(extraEnv []string, homeDir string) []string {
	if e != nil && e.config != nil && e.config.IsolationDisabled() {
		env := mergeEnvLists(os.Environ(), extraEnv)
		if strings.TrimSpace(homeDir) != "" && !envListHas(extraEnv, "HOME") {
			env = mergeEnvLists(env, []string{"HOME=" + strings.TrimSpace(homeDir)})
		}
		return env
	}
	return buildCommandEnv(extraEnv, homeDir)
}

// safeChildEnvBase returns the minimal host environment variables inherited
// by executed commands.
func safeChildEnvBase() []string {
	var base []string
	for _, key := range []string{"PATH", "TERM", "TZ", "LANG", "HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			base = append(base, key+"="+value)
		}
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(envKey(entry), "LC_") {
			base = append(base, entry)
		}
	}
	return base
}

func envListHas(env []string, key string) bool {
	for _, entry := range env {
		if envKey(entry) == key {
			return true
		}
	}
	return false
}

func mergeEnvLists(base, overrides []string) []string {
	if len(overrides) == 0 {
		return append([]string{}, base...)
	}

	merged := append([]string{}, base...)
	indexByKey := make(map[string]int, len(merged))
	for idx, entry := range merged {
		indexByKey[envKey(entry)] = idx
	}

	for _, entry := range overrides {
		key := envKey(entry)
		if idx, ok := indexByKey[key]; ok {
			merged[idx] = entry
			continue
		}
		indexByKey[key] = len(merged)
		merged = append(merged, entry)
	}

	return merged
}

func envKey(entry string) string {
	if idx := strings.IndexByte(entry, '='); idx >= 0 {
		return entry[:idx]
	}
	return entry
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
	truncated, _ := truncateOutputWithFlag(output)
	return truncated
}

func truncateOutputWithFlag(output string) (string, bool) {
	wasTruncated := false
	if len(output) > MaxOutputBytes {
		output = output[len(output)-MaxOutputBytes:]
		wasTruncated = true
	}

	lines := strings.Split(output, "\n")
	if len(lines) > MaxOutputLines {
		lines = lines[len(lines)-MaxOutputLines:]
		// Add truncation indicator
		lines[0] = "... (output truncated)"
		wasTruncated = true
	}

	return strings.Join(lines, "\n"), wasTruncated
}

// CheckPermission checks if a command would be allowed without executing it
func (e *Executor) CheckPermission(cmd string) (Decision, string, error) {
	if e.config.IsolationDisabled() {
		return Allow, "isolation disabled by outer sandbox", nil
	}
	decision, err := e.perm.CheckWithReason(cmd)
	if err != nil {
		var denied *DeniedError
		if errors.As(err, &denied) {
			return decision, denied.Reason, nil
		}
		return decision, "", err
	}
	if decision == Allow {
		return Allow, "all commands allowed", nil
	}
	return Deny, "permission denied", nil
}

// Permission returns the live permission checker shared by this executor.
// Grants applied to it take effect immediately for subsequent runs.
func (e *Executor) Permission() *Permission {
	return e.perm
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

// WrapCommand wraps a command with sandbox isolation.
// Returns the wrapped command string, a cleanup function, and any error.
func (e *Executor) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	if e.config.Sandbox.Enabled && !e.sandbox.IsAvailable() {
		return "", nil, fmt.Errorf("%w: %s", ErrSandboxUnavailable, e.sandbox.Name())
	}
	return e.sandbox.WrapCommand(cmd, opts)
}

// ValidateWorkdir validates and expands the working directory
func ValidateWorkdir(workdir string) (string, error) {
	if workdir == "" {
		return os.Getwd()
	}

	// Expand ~ to home directory
	workdir = expandPath(workdir, "", "")

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
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
