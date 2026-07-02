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
		tools.WithString("task_describe",
			tools.Required(),
			tools.Description("Describe what to explore, research, or investigate. Be specific about what information you need back in the report."),
		),
		tools.WithToolHandler(callExploreTool(a.option.SelfAgent, sess, a.option.ExploreTools)),
	)
}

func (a *Subagents) buildRunTaskTool(sess *session.Session) *tools.Tool {
	return tools.NewTool("run_task",
		tools.WithDescription(a.runTaskToolDescription),
		tools.WithString("agent_name",
			tools.Required(),
			tools.Description("The name of the expert agent to delegate to."),
		),
		tools.WithString("task_describe",
			tools.Required(),
			tools.Description("Provide a concise description of the task for the expert agent, including all necessary context."),
		),
		tools.WithToolHandler(callSubagentTool(a.option.ExpertAgents, sess, a.option.ExpertTools)),
	)
}

func NewHook(_ providers.Client, opt Option) *Subagents {
	opt = cloneOption(opt)
	if opt.ExploreSystemPrompt == "" {
		opt.ExploreSystemPrompt = EXPLORE_SYSTEM_PROMPT
	}
	if opt.ExploreDescribePrompt == "" {
		opt.ExploreDescribePrompt = EXPLORE_DESC_PROMPT
	}
	if opt.RunTaskSystemPrompt == "" {
		opt.RunTaskSystemPrompt = EXPERT_SYSTEM_PROMPT
	}
	if opt.RunTaskDescribePrompt == "" {
		opt.RunTaskDescribePrompt = EXPERT_DESC_PROMPT
	}

	return &Subagents{
		option:                 opt,
		systemPrompts:          initSystemPrompts(opt),
		exploreToolDescription: opt.ExploreDescribePrompt,
		runTaskToolDescription: initExpertDescribePrompt(opt),
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
	ExploreSystemPrompt   string
	ExploreDescribePrompt string

	RunTaskSystemPrompt   string
	RunTaskDescribePrompt string

	ExploreTools []*tools.Tool
	ExpertTools  []*tools.Tool

	SelfAgent    *ExpertAgent
	ExpertAgents []ExpertAgent
}

type ExpertAgent struct {
	Name     string
	Describe string
	Agent    agents.Agent
}
