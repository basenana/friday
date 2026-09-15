package commands

import (
	"fmt"
	"strings"
)

// --- /context ---

type contextCmd struct{}

func (contextCmd) Name() string        { return "context" }
func (contextCmd) Aliases() []string   { return nil }
func (contextCmd) Description() string { return "Show context window occupancy" }
func (contextCmd) Metadata() Metadata {
	return Metadata{Usage: "/context", Category: "Info", Policy: PolicyImmediate}
}
func (contextCmd) Execute(_ *Context) (*Result, error) { return ResultOf(ShowContextAction{}), nil }

// --- /compact ---

type compactCmd struct{}

func (compactCmd) Name() string        { return "compact" }
func (compactCmd) Aliases() []string   { return nil }
func (compactCmd) Description() string { return "Compact the conversation history now" }
func (compactCmd) Metadata() Metadata {
	return Metadata{Usage: "/compact", Category: "Info", Policy: PolicyDeferred}
}
func (compactCmd) Execute(_ *Context) (*Result, error) {
	return ResultOf(CompactSessionAction{}), nil
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

type mcpCmd struct{}

func (mcpCmd) Name() string        { return "mcp" }
func (mcpCmd) Aliases() []string   { return nil }
func (mcpCmd) Description() string { return "List and manage MCP servers" }
func (mcpCmd) Metadata() Metadata {
	return Metadata{Usage: "/mcp [inspect|trust|untrust|refresh|reconnect] [server]", Category: "Info", Policy: PolicyImmediate}
}
func (mcpCmd) Execute(ctx *Context) (*Result, error) {
	op, server := "list", ""
	var args []string
	if ctx != nil {
		args = ctx.Args
	}
	if len(args) > 0 {
		op = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		server = args[1]
	}
	switch op {
	case "list":
		if len(args) > 0 {
			return nil, fmt.Errorf("usage: /mcp")
		}
	case "refresh":
		if len(args) > 2 {
			return nil, fmt.Errorf("usage: /mcp refresh [server]")
		}
	case "inspect", "trust", "untrust", "reconnect":
		if server == "" || len(args) != 2 {
			return nil, fmt.Errorf("usage: /mcp %s <server>", op)
		}
	default:
		return nil, fmt.Errorf("unknown MCP operation %q", op)
	}
	return ResultOf(MCPAction{Operation: op, Server: server}), nil
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
	reg.Register(mcpCmd{})
}
