package commands

import (
	"strings"

	"github.com/basenana/friday/core/collaboration"
)

// --- /plan ---

type planCmd struct{}

func newPlanCmd() planCmd         { return planCmd{} }
func (planCmd) Name() string      { return "plan" }
func (planCmd) Aliases() []string { return nil }
func (planCmd) Description() string {
	return "Enter Plan Mode, optionally with a planning task (/plan off to exit)"
}

func (planCmd) Metadata() Metadata {
	return Metadata{Usage: "/plan [task] | /plan off", Category: "Collaborate", Policy: PolicyDeferred}
}

func (p planCmd) Execute(ctx *Context) (*Result, error) {
	if len(ctx.Args) > 0 && strings.EqualFold(ctx.Args[0], "off") && len(ctx.Args) == 1 {
		return ResultOf(SetModeAction{Mode: collaboration.ModeDefault}), nil
	}
	return ResultOf(SetModeAction{Mode: collaboration.ModePlan, Prompt: strings.TrimSpace(ctx.RawArgs)}), nil
}

// --- /loop ---

type loopCmd struct{}

func (loopCmd) Name() string        { return "loop" }
func (loopCmd) Aliases() []string   { return nil }
func (loopCmd) Description() string { return "Run an autonomous Ralph Loop for a development task" }
func (loopCmd) Metadata() Metadata {
	return Metadata{Usage: "/loop <task>", Category: "Collaborate", Policy: PolicyDeferred}
}
func (loopCmd) Execute(ctx *Context) (*Result, error) {
	task := strings.TrimSpace(ctx.RawArgs)
	if task == "" {
		return MessageResult("usage: /loop <task>"), nil
	}
	if ctx.Mode == collaboration.ModePlan {
		return MessageResult("/loop is unavailable in Plan Mode; run /plan off first"), nil
	}
	return ResultOf(StartLoopAction{Task: task}), nil
}

// RegisterAgentCommands registers the agent and autonomous collaboration commands.
func RegisterAgentCommands(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(newPlanCmd())
	reg.Register(loopCmd{})
}
