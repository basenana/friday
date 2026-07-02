package commands

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/core/session"
)

// --- /cost ---

type costCmd struct{}

func (costCmd) Name() string        { return "cost" }
func (costCmd) Aliases() []string   { return nil }
func (costCmd) Description() string { return "Show token usage for the current session" }
func (costCmd) Execute(ctx *Context) (*Result, error) {
	sess := currentSession(ctx)
	if sess == nil {
		return &Result{Message: "no active session"}, nil
	}
	tokens := sess.Context.TokenCheckpoint.PromptTokens
	msg := fmt.Sprintf("Session tokens (prompt): %d\nMessages: %d", tokens, len(sess.History))
	return &Result{Message: msg}, nil
}

// --- /context ---

type contextCmd struct{}

func (contextCmd) Name() string        { return "context" }
func (contextCmd) Aliases() []string   { return nil }
func (contextCmd) Description() string { return "Show context window occupancy" }
func (contextCmd) Execute(ctx *Context) (*Result, error) {
	sess := currentSession(ctx)
	if sess == nil {
		return &Result{Message: "no active session"}, nil
	}
	window := sess.Context.PromptBudget.ContextWindow
	tokens := sess.Context.TokenCheckpoint.PromptTokens
	if window <= 0 {
		return &Result{Message: fmt.Sprintf("Prompt tokens: %d (context window unknown)", tokens)}, nil
	}
	pct := float64(tokens) / float64(window) * 100
	return &Result{Message: fmt.Sprintf("Context: %d / %d tokens (%.1f%%)", tokens, window, pct)}, nil
}

// --- /compact ---

type compactCmd struct{}

func (compactCmd) Name() string        { return "compact" }
func (compactCmd) Aliases() []string   { return nil }
func (compactCmd) Description() string { return "Compact the conversation history now" }
func (compactCmd) Execute(ctx *Context) (*Result, error) {
	sess := currentSession(ctx)
	if sess == nil {
		return &Result{Message: "no active session"}, nil
	}
	before := len(sess.History)
	if err := sess.CompactHistory(ctx.Ctx); err != nil {
		return &Result{Message: "compact failed: " + err.Error()}, nil
	}
	after := len(sess.History)
	return &Result{Message: fmt.Sprintf("Compacted: %d → %d messages", before, after)}, nil
}

// --- /model ---

type modelCmd struct{}

func (modelCmd) Name() string        { return "model" }
func (modelCmd) Aliases() []string   { return nil }
func (modelCmd) Description() string { return "Show the current model (or set with /model <name>)" }
func (modelCmd) Execute(ctx *Context) (*Result, error) {
	if ctx.Config == nil {
		return &Result{Message: "config unavailable"}, nil
	}
	if len(ctx.Args) == 0 {
		pm := ctx.Config.PrimaryModel()
		return &Result{Message: fmt.Sprintf("Current model: %s (provider: %s)", pm.Model, pm.Provider)}, nil
	}
	// Setting a model at runtime requires rewriting config and rebuilding the
	// provider client — out of scope for the first cut. Surface clearly.
	return &Result{Message: "switching models at runtime is not yet supported; edit your config file"}, nil
}

// --- /session ---

type sessionCmd struct{}

func (sessionCmd) Name() string        { return "session" }
func (sessionCmd) Aliases() []string   { return []string{"sessions"} }
func (sessionCmd) Description() string { return "Session operations: /session list | new | use <id>" }
func (sessionCmd) Execute(ctx *Context) (*Result, error) {
	if ctx.SessMgr == nil {
		return &Result{Message: "session manager unavailable"}, nil
	}
	sub := ""
	if len(ctx.Args) > 0 {
		sub = ctx.Args[0]
	}
	switch sub {
	case "", "list":
		// Manager does not expose List; show current only.
		id, err := ctx.SessMgr.GetCurrentID()
		if err != nil || id == "" {
			return &Result{Message: "current session: (none)"}, nil
		}
		return &Result{Message: fmt.Sprintf("current session: %s", id)}, nil

	case "new":
		sess, id, err := ctx.SessMgr.CreateIsolated()
		if err != nil {
			return &Result{Message: "create session failed: " + err.Error()}, nil
		}
		_ = sess
		return &Result{
			SwitchSession: id,
			Message:       fmt.Sprintf("created session %s", shortID(id)),
		}, nil

	case "use":
		if len(ctx.Args) < 2 {
			return &Result{Message: "usage: /session use <id>"}, nil
		}
		id := ctx.Args[1]
		exists, err := ctx.SessMgr.Exists(id)
		if err != nil {
			return &Result{Message: "lookup session failed: " + err.Error()}, nil
		}
		if !exists {
			return &Result{Message: "session not found: " + id}, nil
		}
		return &Result{SwitchSession: id}, nil

	default:
		return &Result{Message: "unknown subcommand: " + sub + "\nusage: /session list | new | use <id>"}, nil
	}
}

// currentSession fetches the session behind ctx.SessionID.
func currentSession(ctx *Context) *session.Session {
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

// RegisterInfoCommands registers /cost, /context, /compact, /model, /session.
func RegisterInfoCommands(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(costCmd{})
	reg.Register(contextCmd{})
	reg.Register(compactCmd{})
	reg.Register(modelCmd{})
	reg.Register(sessionCmd{})
}
