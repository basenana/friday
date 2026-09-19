package sandbox

import (
	"errors"
	"strings"
	"sync"
)

// Decision represents the result of a permission check
type Decision int

const (
	// Allow means the command is permitted
	Allow Decision = iota
	// Deny means the command is blocked
	Deny
)

func (d Decision) String() string {
	if d == Allow {
		return "allow"
	}
	return "deny"
}

// Permission handles permission checking for commands
type Permission struct {
	mu     sync.RWMutex
	config *Config
}

// NewPermission creates a new Permission checker
func NewPermission(cfg *Config) *Permission {
	return &Permission{config: cfg}
}

// Check checks if a command string is allowed to execute
// It parses the command and checks each subcommand against the rules
func (p *Permission) Check(cmdStr string) (Decision, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	commands, err := ParseCommands(cmdStr)
	if err != nil {
		// If we can't parse the command, deny it for safety
		return Deny, err
	}

	// Empty command is allowed (no-op)
	if len(commands) == 0 {
		return Allow, nil
	}

	for _, cmd := range commands {
		decision := p.checkCommand(cmd)
		if decision == Deny {
			return Deny, nil
		}
	}

	return Allow, nil
}

// checkCommand checks a single command against the permission rules.
// The caller must hold at least a read lock.
func (p *Permission) checkCommand(cmd Command) Decision {
	// Check deny rules first (highest priority)
	for _, pattern := range p.config.Permissions.Deny {
		if cmd.MatchPattern(pattern) {
			return Deny
		}
	}

	// Check allow rules
	for _, pattern := range p.config.Permissions.Allow {
		if cmd.MatchPattern(pattern) {
			return Allow
		}
	}

	// If not in allow list, deny by default
	return Deny
}

// CheckWithReason checks if a command is allowed. A denial is reported as a
// *DeniedError whose ExplicitDeny field distinguishes an explicit deny-rule
// match (never grantable) from a command that is simply missing from the
// allow list (grantable through interactive approval). errors.Is(err,
// ErrPermissionDenied) keeps working through Unwrap.
func (p *Permission) CheckWithReason(cmdStr string) (Decision, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	commands, err := ParseCommands(cmdStr)
	if err != nil {
		// If we can't parse the command, deny it for safety; the parse error
		// is returned so callers can distinguish it from a rule denial.
		return Deny, err
	}

	if len(commands) == 0 {
		return Allow, nil
	}

	for _, cmd := range commands {
		// Check deny rules first
		for _, pattern := range p.config.Permissions.Deny {
			if cmd.MatchPattern(pattern) {
				return Deny, &DeniedError{
					Command:      cmd.Name,
					Reason:       "command '" + cmd.Name + "' matched deny rule: " + pattern,
					ExplicitDeny: true,
				}
			}
		}

		// Check allow rules
		allowed := false
		for _, pattern := range p.config.Permissions.Allow {
			if cmd.MatchPattern(pattern) {
				allowed = true
				break
			}
		}

		if !allowed {
			return Deny, &DeniedError{
				Command: cmd.Name,
				Reason:  "command '" + cmd.Name + "' is not in allow list",
			}
		}
	}

	return Allow, nil
}

// Grant adds an allow pattern to the live configuration. It takes effect
// immediately for subsequent checks. Patterns already present are ignored.
// Grant never removes or weakens deny rules.
func (p *Permission) Grant(pattern string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, existing := range p.config.Permissions.Allow {
		if existing == pattern {
			return
		}
	}
	p.config.Permissions.Allow = append(p.config.Permissions.Allow, pattern)
}

// ErrPermissionDenied is returned when a command is denied
var ErrPermissionDenied = errors.New("permission denied")

// IsDenied checks if an error is a permission denied error
func IsDenied(err error) bool {
	return errors.Is(err, ErrPermissionDenied)
}

// DeniedError describes a command permission denial with enough structure
// for callers to decide whether the denial can be lifted interactively.
type DeniedError struct {
	// Command is the denied sub-command name (for example "gofmt").
	Command string
	// Reason is a human-readable denial reason.
	Reason string
	// ExplicitDeny is true when a deny rule matched the command. Such
	// denials can never be lifted through interactive approval. It is false
	// when the command is simply missing from the allow list, which approval
	// can grant.
	ExplicitDeny bool
}

func (e *DeniedError) Error() string {
	if e == nil {
		return ErrPermissionDenied.Error()
	}
	if e.Reason == "" {
		return ErrPermissionDenied.Error()
	}
	return ErrPermissionDenied.Error() + ": " + e.Reason
}

func (e *DeniedError) Unwrap() error { return ErrPermissionDenied }
