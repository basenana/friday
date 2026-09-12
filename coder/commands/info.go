package commands

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/core/session"
)

// --- /context ---

type contextCmd struct{}

func (contextCmd) Name() string        { return "context" }
func (contextCmd) Aliases() []string   { return nil }
func (contextCmd) Description() string { return "Show context window occupancy" }
func (contextCmd) Metadata() Metadata {
	return Metadata{Usage: "/context", Category: "Info", Policy: PolicyImmediate}
}
func (contextCmd) Execute(ctx *Context) (*Result, error) {
	sess := currentSession(ctx)
	if sess == nil {
		return MessageResult("no active session"), nil
	}
	window := sess.Context.PromptBudget.ContextWindow
	tokens := sess.Context.TokenCheckpoint.PromptTokens
	if window <= 0 {
		return MessageResult(fmt.Sprintf("Prompt tokens: %d (context window unknown)", tokens)), nil
	}
	pct := float64(tokens) / float64(window) * 100
	return MessageResult(fmt.Sprintf("Context: %d / %d tokens (%.1f%%)", tokens, window, pct)), nil
}

// --- /compact ---

type compactCmd struct{}

func (compactCmd) Name() string        { return "compact" }
func (compactCmd) Aliases() []string   { return nil }
func (compactCmd) Description() string { return "Compact the conversation history now" }
func (compactCmd) Metadata() Metadata {
	return Metadata{Usage: "/compact", Category: "Info", Policy: PolicyDeferred}
}
func (compactCmd) Execute(ctx *Context) (*Result, error) {
	sess := currentSession(ctx)
	if sess == nil {
		return MessageResult("no active session"), nil
	}
	before := len(sess.History)
	if err := sess.CompactHistory(ctx.Ctx); err != nil {
		return MessageResult("compact failed: " + err.Error()), nil
	}
	after := len(sess.History)
	return MessageResult(fmt.Sprintf("Compacted: %d → %d messages", before, after)), nil
}

// --- /model ---

type modelCmd struct{}

func (modelCmd) Name() string        { return "model" }
func (modelCmd) Aliases() []string   { return nil }
func (modelCmd) Description() string { return "Show the current model (or set with /model <name>)" }
func (modelCmd) Metadata() Metadata {
	return Metadata{Usage: "/model [provider/model|model]", Category: "Info", Policy: PolicyDeferred}
}
func (modelCmd) Execute(ctx *Context) (*Result, error) {
	if ctx.Config == nil {
		return MessageResult("config unavailable"), nil
	}
	if len(ctx.Args) == 0 {
		return ResultOf(OpenModelAction{}), nil
	}
	return ResultOf(SetModelAction{Target: strings.TrimSpace(ctx.RawArgs)}), nil
}

type statusCmd struct{}

func (statusCmd) Name() string        { return "status" }
func (statusCmd) Aliases() []string   { return nil }
func (statusCmd) Description() string { return "Show session, model, context and sandbox status" }
func (statusCmd) Metadata() Metadata {
	return Metadata{Usage: "/status", Category: "Info", Policy: PolicyImmediate}
}
func (statusCmd) Execute(_ *Context) (*Result, error) { return ResultOf(ShowStatusAction{}), nil }

// currentSession fetches the session behind ctx.SessionID.
func currentSession(ctx *Context) *session.Session {
	if ctx.Session != nil {
		return ctx.Session.Current()
	}
	if ctx.SessMgr == nil || ctx.SessionID == "" {
		return nil
	}
	sess, _, err := ctx.SessMgr.GetOrCreateByID(ctx.SessionID)
	if err != nil {
		return nil
	}
	return sess
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// RegisterInfoCommands registers the informational commands.
func RegisterInfoCommands(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(contextCmd{})
	reg.Register(compactCmd{})
	reg.Register(modelCmd{})
	reg.Register(statusCmd{})
}
