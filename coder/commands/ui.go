package commands

import (
	"strconv"
	"strings"
)

type uiCmd struct {
	name, desc, usage, category string
	policy                      RunPolicy
}

func (c uiCmd) Name() string        { return c.name }
func (c uiCmd) Aliases() []string   { return nil }
func (c uiCmd) Description() string { return c.desc }
func (c uiCmd) Metadata() Metadata {
	return Metadata{Usage: c.usage, Category: c.category, Policy: c.policy}
}
func (c uiCmd) Execute(ctx *Context) (*Result, error) {
	raw := strings.TrimSpace(ctx.RawArgs)
	switch c.name {
	case "open":
		if raw == "" {
			return MessageResult("usage: /open <card-id>"), nil
		}
		return ResultOf(OpenCardAction{ID: strings.Fields(raw)[0]}), nil
	case "show":
		if raw == "" {
			return MessageResult("usage: /show <tool-call-id>"), nil
		}
		return ResultOf(ShowToolAction{ID: strings.Fields(raw)[0]}), nil
	case "diff":
		return ResultOf(ShowDiffAction{}), nil
	case "copy":
		index := 1
		if raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				return MessageResult("usage: /copy [n]"), nil
			}
			index = parsed
		}
		return ResultOf(CopyResponseAction{Index: index}), nil
	case "tasks":
		return ResultOf(ShowTasksAction{}), nil
	case "stop":
		if raw == "" {
			return MessageResult("usage: /stop <task-id|all>"), nil
		}
		return ResultOf(StopTaskAction{ID: strings.Fields(raw)[0]}), nil
	}
	return nil, nil
}

func RegisterUICommands(reg *Registry) {
	for _, cmd := range []uiCmd{
		{name: "open", desc: "Open a rich-card artifact", usage: "/open <card-id>", category: "Content", policy: PolicyImmediate},
		{name: "show", desc: "Inspect complete tool output", usage: "/show <tool-call-id>", category: "Content", policy: PolicyImmediate},
		{name: "diff", desc: "Show working tree changes", usage: "/diff", category: "Develop", policy: PolicyImmediate},
		{name: "copy", desc: "Copy a recent assistant response", usage: "/copy [n]", category: "Develop", policy: PolicyImmediate},
		{name: "tasks", desc: "Show background tasks", usage: "/tasks", category: "Runtime", policy: PolicyImmediate},
		{name: "stop", desc: "Stop a background task", usage: "/stop <task-id|all>", category: "Runtime", policy: PolicyImmediate},
	} {
		reg.Register(cmd)
	}
}
