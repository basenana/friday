package sandbox

import (
	"runtime"
	"time"
)

// Sandbox defines the interface for OS-level sandboxing
type Sandbox interface {
	// WrapCommand wraps a command to run in the sandbox
	// Returns the wrapped command, cleanup function, and any error
	WrapCommand(cmd string, opts ExecOptions) (wrappedCmd string, cleanup func(), err error)

	// IsAvailable checks if the sandbox is available on this system
	IsAvailable() bool

	// Name returns the name of the sandbox implementation
	Name() string
}

// ExecOptions defines options for command execution
type ExecOptions struct {
	// Workdir is the working directory for the command
	Workdir string
	// Env is the environment variables for the command
	Env []string
	// Timeout is the maximum time the command can run
	Timeout time.Duration
	// Stdin is the stdin for the command
	Stdin string
}

// Result is the result of command execution
type Result struct {
	// Stdout is the standard output
	Stdout string
	// Stderr is the standard error
	Stderr string
	// ExitCode is the exit code of the command
	ExitCode int
	// TimedOut indicates if the command timed out
	TimedOut bool
}

// NewSandbox creates a new sandbox based on the current OS
func NewSandbox(cfg *Config) Sandbox {
	switch runtime.GOOS {
	case "darwin":
		return NewSeatbelt(cfg)
	case "linux":
		return NewBwrap(cfg)
	default:
		// Unsupported OS, return a no-op sandbox
		return &noopSandbox{}
	}
}

// noopSandbox is a no-op sandbox for unsupported platforms
type noopSandbox struct{}

func (n *noopSandbox) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	return cmd, func() {}, nil
}

func (n *noopSandbox) IsAvailable() bool {
	return false
}

func (n *noopSandbox) Name() string {
	return "noop"
}

// NoSandbox indicates sandboxing is disabled
type NoSandbox struct{}

func (n *NoSandbox) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	return cmd, func() {}, nil
}

func (n *NoSandbox) IsAvailable() bool {
	return true
}

func (n *NoSandbox) Name() string {
	return "disabled"
}
