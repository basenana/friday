package commands

import (
	"context"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/sessions"
)

// Result is what a command returns. Actions are applied in order, which keeps
// command intent explicit and avoids invalid combinations of optional fields.
type Result struct {
	Actions []Action
}

// Action is a closed command-to-UI contract. Concrete action types make
// payload requirements visible at compile time and let the TUI route work by
// responsibility instead of inspecting a growing bag of flags.
type Action interface{ commandAction() }

type AppendMessageAction struct{ Content string }
type ClearSessionAction struct{ SessionID string }
type QuitAction struct{}
type RunAgentAction struct{ Agent, Input string }
type OpenCardAction struct{ ID string }
type ShowToolAction struct{ ID string }
type ShowDiffAction struct{}
type CopyResponseAction struct{ Index int }
type ShowTasksAction struct{}
type StopTaskAction struct{ ID string }
type OpenResumeAction struct{}
type ResumeSessionAction struct{ Target string }
type RenameSessionAction struct{ Name string }
type ArchiveSessionAction struct{ Target string }
type DeleteSessionAction struct{ Target string }
type OpenModelAction struct{}
type SetModelAction struct{ Target string }
type ShowStatusAction struct{}
type CompactSessionAction struct{}
type SetModeAction struct {
	Mode   collaboration.Mode
	Prompt string
}

func (AppendMessageAction) commandAction()  {}
func (ClearSessionAction) commandAction()   {}
func (QuitAction) commandAction()           {}
func (RunAgentAction) commandAction()       {}
func (OpenCardAction) commandAction()       {}
func (ShowToolAction) commandAction()       {}
func (ShowDiffAction) commandAction()       {}
func (CopyResponseAction) commandAction()   {}
func (ShowTasksAction) commandAction()      {}
func (StopTaskAction) commandAction()       {}
func (OpenResumeAction) commandAction()     {}
func (ResumeSessionAction) commandAction()  {}
func (RenameSessionAction) commandAction()  {}
func (ArchiveSessionAction) commandAction() {}
func (DeleteSessionAction) commandAction()  {}
func (OpenModelAction) commandAction()      {}
func (SetModelAction) commandAction()       {}
func (ShowStatusAction) commandAction()     {}
func (CompactSessionAction) commandAction() {}
func (SetModeAction) commandAction()        {}

func ResultOf(actions ...Action) *Result { return &Result{Actions: actions} }

func MessageResult(content string) *Result {
	return ResultOf(AppendMessageAction{Content: content})
}

// Context carries the dependencies a command may need at execution time.
// Not all fields are populated for every invocation — commands should
// nil-check before use.
type Context struct {
	Ctx       context.Context
	SessionID string
	Args      []string // tokens after the command name (whitespace-split)
	RawArgs   string   // exact text after the command name
	Session   sessions.SessionLifecycle
	// SessMgr is retained for non-project compatibility while callers migrate
	// to the root-bound Session capability above.
	SessMgr *sessions.Manager
	Config  *config.Config
}

type RunPolicy string

const (
	PolicyImmediate RunPolicy = "immediate"
	PolicyDeferred  RunPolicy = "deferred"
)

type Metadata struct {
	Usage    string
	Category string
	Policy   RunPolicy
}

type DetailedCommand interface {
	Command
	Metadata() Metadata
}

func CommandMetadata(cmd Command) Metadata {
	if detailed, ok := cmd.(DetailedCommand); ok {
		meta := detailed.Metadata()
		if meta.Usage == "" {
			meta.Usage = "/" + cmd.Name()
		}
		if meta.Policy == "" {
			meta.Policy = PolicyDeferred
		}
		return meta
	}
	return Metadata{Usage: "/" + cmd.Name(), Category: "General", Policy: PolicyDeferred}
}

// Command is a single slash command.
type Command interface {
	Name() string
	Aliases() []string
	Description() string
	Execute(ctx *Context) (*Result, error)
}

// RegisterAll installs the complete canonical TUI command set. Keeping this
// in one place prevents alternate entry points and tests from exposing a
// partial or stale command surface.
func RegisterAll(reg *Registry) {
	RegisterBuiltins(reg)
	RegisterInfoCommands(reg)
	RegisterAgentCommands(reg)
	RegisterSessionCommands(reg)
	RegisterUICommands(reg)
}
