package subagents

import (
	"context"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type Subagents struct {
	option                 Option
	systemPrompts          []string
	exploreToolDescription string
	runTaskToolDescription string
}

// SessionForker is the root-bound capability exposed to subagent tools. It
// intentionally has no global session lookup or mutation methods.
type SessionForker interface {
	Fork() (*session.Session, error)
	Release(*session.Session) error
}

var _ session.BeforeModelHook = &Subagents{}
var _ session.BeforeAgentHook = &Subagents{}

func (a *Subagents) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	// Always inject tools so forked sessions share the same tool definitions
	// (cache prefix consistency). Recursion is prevented at the tool handler level.
	var toolsToAdd []*tools.Tool
	if a.option.SelfAgent != nil {
		toolsToAdd = append(toolsToAdd, a.buildExploreTool(sess))
	}
	if len(a.option.ExpertAgents) > 0 {
		toolsToAdd = append(toolsToAdd, a.buildRunTaskTool(sess))
	}
	if len(toolsToAdd) > 0 {
		req.AppendTools(toolsToAdd...)
	}
	return nil
}

func (a *Subagents) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	// Always inject system prompts so forked sessions share the same cache prefix
	req.AppendSystemPrompt(a.systemPrompts...)
	return nil
}

func (a *Subagents) buildExploreTool(sess *session.Session) *tools.Tool {
	return tools.NewTool("explore",
		tools.WithDescription(a.exploreToolDescription),
		tools.WithString("task",
			tools.Required(),
			tools.Description("Complete investigation request, including scope and the exact findings needed."),
		),
		tools.WithToolHandler(callExploreToolWithForker(a.option.SelfAgent, sess, a.option.SessionForker, a.option.ExploreTools)),
	)
}

func (a *Subagents) buildRunTaskTool(sess *session.Session) *tools.Tool {
	return tools.NewTool("run_task",
		tools.WithDescription(a.runTaskToolDescription),
		tools.WithString("agent_name",
			tools.Required(),
			tools.Enum(expertAgentNames(a.option.ExpertAgents)...),
			tools.Description("The name of the expert agent to delegate to."),
		),
		tools.WithString("task",
			tools.Required(),
			tools.Description("Complete task request, including context, constraints, and expected output."),
		),
		tools.WithToolHandler(callSubagentToolWithForker(a.option.ExpertAgents, sess, a.option.SessionForker, a.option.ExpertTools)),
	)
}

func expertAgentNames(agents []ExpertAgent) []string {
	names := make([]string, len(agents))
	for i, agent := range agents {
		names[i] = agent.Name
	}
	return names
}

func NewHook(_ providers.Client, opt Option) *Subagents {
	opt = cloneOption(opt)
	if opt.ExploreSystemPrompt == "" {
		opt.ExploreSystemPrompt = EXPLORE_SYSTEM_PROMPT
	}
	if opt.ExploreDescriptionPrompt == "" {
		opt.ExploreDescriptionPrompt = EXPLORE_DESCRIPTION_PROMPT
	}
	if opt.RunTaskSystemPrompt == "" {
		opt.RunTaskSystemPrompt = EXPERT_SYSTEM_PROMPT
	}
	if opt.RunTaskDescriptionPrompt == "" {
		opt.RunTaskDescriptionPrompt = EXPERT_DESCRIPTION_PROMPT
	}

	return &Subagents{
		option:                 opt,
		systemPrompts:          initSystemPrompts(opt),
		exploreToolDescription: opt.ExploreDescriptionPrompt,
		runTaskToolDescription: initExpertDescriptionPrompt(opt),
	}
}

func cloneOption(opt Option) Option {
	cloned := opt
	if opt.SelfAgent != nil {
		self := *opt.SelfAgent
		cloned.SelfAgent = &self
	}
	cloned.ExpertAgents = append([]ExpertAgent(nil), opt.ExpertAgents...)
	cloned.ExploreTools = append([]*tools.Tool(nil), opt.ExploreTools...)
	cloned.ExpertTools = append([]*tools.Tool(nil), opt.ExpertTools...)
	return cloned
}

type Option struct {
	ExploreSystemPrompt      string
	ExploreDescriptionPrompt string

	RunTaskSystemPrompt      string
	RunTaskDescriptionPrompt string

	ExploreTools []*tools.Tool
	ExpertTools  []*tools.Tool

	SelfAgent     *ExpertAgent
	ExpertAgents  []ExpertAgent
	SessionForker SessionForker
}

type ExpertAgent struct {
	Name        string
	Description string
	Agent       agents.Agent
}
