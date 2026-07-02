package commands

import (
	"context"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
)

// Result is what a command returns. The TUI inspects the populated fields
// and performs the corresponding UI action. Fields are mutually independent:
// multiple may be set in a single Result (e.g. ClearMessages + Message).
type Result struct {
	// Message is appended to the chat view as an assistant block.
	Message string
	// ClearMessages wipes the visible chat history.
	ClearMessages bool
	// SwitchSession switches the TUI to the named session ID.
	SwitchSession string
	// Quit exits the TUI.
	Quit bool

	// RunAgent names a coder agent to invoke asynchronously (e.g. "planner").
	// When non-empty, the TUI routes the request through the main actor, which
	// delegates to the named expert via the run_task tool.
	RunAgent string
	// AgentInput is the input text passed to the RunAgent.
	AgentInput string
}

// Context carries the dependencies a command may need at execution time.
// Not all fields are populated for every invocation — commands should
// nil-check before use.
type Context struct {
	Ctx       context.Context
	SessionID string
	Args      []string // tokens after the command name (whitespace-split)
	SessMgr   *sessions.Manager
	ActorReg  *actor.Registry
	Config    *config.Config
}

// Command is a single slash command.
type Command interface {
	Name() string
	Aliases() []string
	Description() string
	Execute(ctx *Context) (*Result, error)
}
