package commands

import (
	"strings"

	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/core/collaboration"
)

// agentBackedCmd contains the shared metadata and input assembly for commands
// that delegate to a coder agent.
type agentBackedCmd struct {
	name        string
	aliases     []string
	desc        string
	agent       string
	requireArgs bool
}

func (c agentBackedCmd) Name() string        { return c.name }
func (c agentBackedCmd) Aliases() []string   { return c.aliases }
func (c agentBackedCmd) Description() string { return c.desc }

func (c agentBackedCmd) buildInput(args []string) string {
	return strings.Join(args, " ")
}

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

// --- /review ---

type reviewCmd struct{ agentBackedCmd }

func newReviewCmd() reviewCmd {
	return reviewCmd{agentBackedCmd{
		name:  "review",
		desc:  "Review uncommitted changes via the reviewer agent (reads git diff)",
		agent: coderagents.NameReviewer,
	}}
}

func (reviewCmd) Metadata() Metadata {
	return Metadata{Usage: "/review [instructions]", Category: "Collaborate", Policy: PolicyDeferred}
}

func (r reviewCmd) Execute(ctx *Context) (*Result, error) {
	input := r.buildInput(ctx.Args)
	// If the user did not specify, ask reviewer to review the current diff.
	if strings.TrimSpace(input) == "" {
		input = "Review the uncommitted changes in this repository. Run `git status --short`, `git diff`, and `git diff --staged` to see them. If there are untracked files, read those files directly and include them in the review, then produce your verdict."
	}
	return ResultOf(RunAgentAction{Agent: r.agent, Input: input}), nil
}

// --- /advisor ---

type advisorCmd struct{ agentBackedCmd }

func newAdvisorCmd() advisorCmd {
	return advisorCmd{agentBackedCmd{
		name:        "advisor",
		aliases:     []string{"advise"},
		desc:        "Ask the advisor agent for pragmatic, minimal advice",
		agent:       coderagents.NameAdvisor,
		requireArgs: true,
	}}
}

func (advisorCmd) Metadata() Metadata {
	return Metadata{Usage: "/advisor <question>", Category: "Collaborate", Policy: PolicyDeferred}
}

func (a advisorCmd) Execute(ctx *Context) (*Result, error) {
	if a.requireArgs && len(ctx.Args) == 0 {
		return MessageResult("usage: /advisor <question>"), nil
	}
	return ResultOf(RunAgentAction{Agent: a.agent, Input: a.buildInput(ctx.Args)}), nil
}

// RegisterAgentCommands registers /plan, /review, /advisor.
func RegisterAgentCommands(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(newPlanCmd())
	reg.Register(newReviewCmd())
	reg.Register(newAdvisorCmd())
}
