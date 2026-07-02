package commands

import (
	"strings"

	coderagents "github.com/basenana/friday/coder/agents"
)

// agentBackedCmd is a command that delegates to a coder agent.
// It returns a Result with RunAgent set; the TUI forwards that request through
// the main actor, which delegates via the run_task tool.
type agentBackedCmd struct {
	name        string
	aliases     []string
	desc        string
	agent       string
	prepend     string // optional text prepended to user input
	requireArgs bool
}

func (c agentBackedCmd) Name() string        { return c.name }
func (c agentBackedCmd) Aliases() []string   { return c.aliases }
func (c agentBackedCmd) Description() string { return c.desc }

// buildInput combines the prepend text (if any) with the user's args.
func (c agentBackedCmd) buildInput(args []string) string {
	parts := make([]string, 0, 2)
	if c.prepend != "" {
		parts = append(parts, c.prepend)
	}
	if len(args) > 0 {
		parts = append(parts, strings.Join(args, " "))
	}
	return strings.Join(parts, "\n\n")
}

// --- /plan ---

type planCmd struct{ agentBackedCmd }

func newPlanCmd() planCmd {
	return planCmd{agentBackedCmd{
		name:        "plan",
		desc:        "Interview the planner agent to produce a structured implementation plan",
		agent:       coderagents.NamePlanner,
		requireArgs: true,
	}}
}

func (p planCmd) Execute(ctx *Context) (*Result, error) {
	if p.requireArgs && len(ctx.Args) == 0 {
		return &Result{Message: "usage: /plan <task description>"}, nil
	}
	return &Result{
		RunAgent:   p.agent,
		AgentInput: p.buildInput(ctx.Args),
	}, nil
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

func (r reviewCmd) Execute(ctx *Context) (*Result, error) {
	input := r.buildInput(ctx.Args)
	// If the user did not specify, ask reviewer to review the current diff.
	if strings.TrimSpace(stripPrefix(input)) == "" {
		input = "Review the uncommitted changes in this repository. Run `git status --short`, `git diff`, and `git diff --staged` to see them. If there are untracked files, read those files directly and include them in the review, then produce your verdict."
	}
	return &Result{
		RunAgent:   r.agent,
		AgentInput: input,
	}, nil
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

func (a advisorCmd) Execute(ctx *Context) (*Result, error) {
	if a.requireArgs && len(ctx.Args) == 0 {
		return &Result{Message: "usage: /advisor <question>"}, nil
	}
	return &Result{
		RunAgent:   a.agent,
		AgentInput: a.buildInput(ctx.Args),
	}, nil
}

// stripPrefix is a tiny helper that removes the standard review preamble when
// checking whether the user supplied any args of their own.
func stripPrefix(s string) string {
	const preamble = "Review the uncommitted changes in this repository."
	if len(s) >= len(preamble) && s[:len(preamble)] == preamble {
		return s[len(preamble):]
	}
	return s
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
