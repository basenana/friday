package sandbox

import (
	"errors"
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
	config *Config
}

// NewPermission creates a new Permission checker
func NewPermission(cfg *Config) *Permission {
	return &Permission{config: cfg}
}

// Check checks if a command string is allowed to execute
// It parses the command and checks each subcommand against the rules
func (p *Permission) Check(cmdStr string) (Decision, error) {
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

// checkCommand checks a single command against the permission rules
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

// CheckWithReason checks if a command is allowed and returns the reason
func (p *Permission) CheckWithReason(cmdStr string) (Decision, string, error) {
	commands, err := ParseCommands(cmdStr)
	if err != nil {
		return Deny, "failed to parse command", err
	}

	if len(commands) == 0 {
		return Allow, "empty command", nil
	}

	for _, cmd := range commands {
		// Check deny rules first
		for _, pattern := range p.config.Permissions.Deny {
			if cmd.MatchPattern(pattern) {
				return Deny, "command '" + cmd.Name + "' matched deny rule: " + pattern, nil
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
			return Deny, "command '" + cmd.Name + "' is not in allow list", nil
		}
	}

	return Allow, "all commands allowed", nil
}

// ErrPermissionDenied is returned when a command is denied
var ErrPermissionDenied = errors.New("permission denied")

// IsDenied checks if an error is a permission denied error
func IsDenied(err error) bool {
	return errors.Is(err, ErrPermissionDenied)
}
