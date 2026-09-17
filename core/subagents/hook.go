package subagents

import (
	"context"
	"sort"
	"strings"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type Subagents struct {
	option                 Option
	exploreToolDescription string
	runTaskToolDescription string
	agents                 AgentProvider
	parallel               chan struct{}
}

const defaultMaxParallelSubagents = 4

// SessionForker is the root-bound capability exposed to subagent tools. It
// intentionally has no global session lookup or mutation methods.
type SessionForker interface {
	Fork() (*session.Session, error)
	Release(*session.Session) error
}

// AgentProvider supplies the current expert-agent catalog. Implementations own
// caching and reload policy; Subagents deliberately reads it for every hook
// invocation so long-lived sessions can observe catalog changes.
type AgentProvider interface {
	List() []ExpertAgent
}

type staticAgentProvider []ExpertAgent

func (p staticAgentProvider) List() []ExpertAgent {
	return append([]ExpertAgent(nil), p...)
}

var _ session.BeforeModelHook = &Subagents{}
var _ session.BeforeAgentHook = &Subagents{}

func (a *Subagents) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	if a == nil || req == nil {
		return nil
	}
	// Always inject tools so forked sessions share the same tool definitions
	// (cache prefix consistency). Recursion is prevented at the tool handler level.
	var toolsToAdd []*tools.Tool
	if a.option.SelfAgent != nil {
		toolsToAdd = append(toolsToAdd, a.buildExploreTool(sess))
	}
	experts := a.expertAgents()
	if len(experts) > 0 {
		toolsToAdd = append(toolsToAdd, a.buildRunTaskTool(sess, experts...))
	}
	if len(toolsToAdd) > 0 {
		req.AppendTools(toolsToAdd...)
	}
	return nil
}

func (a *Subagents) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	if a == nil || req == nil {
		return nil
	}
	// Always inject system prompts so forked sessions share the same cache prefix
	req.AppendSystemPrompt(initSystemPrompts(a.option, a.expertAgents())...)
	return nil
}

func (a *Subagents) buildExploreTool(sess *session.Session) *tools.Tool {
	return tools.NewTool("explore",
		tools.WithDescription(a.exploreToolDescription),
		tools.WithArray("tasks",
			tools.Required(),
			tools.MinItems(1),
			tools.UniqueItems(true),
			tools.Items(map[string]interface{}{"type": "string", "minLength": 1}),
			tools.Description("All independent investigations that can run now. Each task must be self-contained and include its scope and expected findings; dependent tasks belong in a later call."),
		),
		tools.WithExample(map[string]interface{}{"tasks": []interface{}{
			"Trace the authentication call path and report the relevant files and control flow.",
			"Inspect cache invalidation behavior and report correctness risks with file references.",
		}}),
		tools.WithToolHandler(callExploreToolWithForker(a.option.SelfAgent, sess, a.option.SessionForker, a.option.ExploreTools, a.parallel)),
	)
}

func (a *Subagents) buildRunTaskTool(sess *session.Session, current ...ExpertAgent) *tools.Tool {
	experts := current
	if len(experts) == 0 {
		experts = a.expertAgents()
	}
	description := a.runTaskToolDescription
	if strings.TrimSpace(description) == "" || a.agents != nil {
		description = initExpertDescriptionPrompt(a.option, experts)
	}
	return tools.NewTool("run_task",
		tools.WithDescription(description),
		tools.WithArray("tasks",
			tools.Required(),
			tools.MinItems(1),
			tools.UniqueItems(true),
			tools.Items(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_name": map[string]interface{}{
						"type":        "string",
						"enum":        expertAgentNames(experts),
						"description": "The expert agent whose description matches this task.",
					},
					"task": map[string]interface{}{
						"type":        "string",
						"minLength":   1,
						"description": "A self-contained task with context, constraints, and expected output.",
					},
				},
				"required":             []string{"agent_name", "task"},
				"additionalProperties": false,
			}),
			tools.Description("All independent expert tasks that can run now. Tasks may use different experts; dependent or conflicting tasks belong in a later call."),
		),
		tools.WithExample(map[string]interface{}{"tasks": []interface{}{
			map[string]interface{}{"agent_name": experts[0].Name, "task": "Analyze the implementation path and report concrete risks with file references."},
			map[string]interface{}{"agent_name": experts[0].Name, "task": "Independently inspect the test coverage and report missing scenarios."},
		}}),
		tools.WithToolHandler(callSubagentToolWithForker(experts, sess, a.option.SessionForker, a.option.ExpertTools, a.parallel)),
	)
}

func (a *Subagents) expertAgents() []ExpertAgent {
	if a == nil {
		return nil
	}
	var experts []ExpertAgent
	if a.agents != nil {
		experts = a.agents.List()
	} else {
		experts = a.option.ExpertAgents
	}
	experts = append([]ExpertAgent(nil), experts...)
	sort.SliceStable(experts, func(i, j int) bool {
		left, right := strings.ToLower(experts[i].Name), strings.ToLower(experts[j].Name)
		if left != right {
			return left < right
		}
		return experts[i].Description < experts[j].Description
	})
	out := experts[:0]
	seen := make(map[string]struct{}, len(experts))
	for _, expert := range experts {
		name := strings.ToLower(strings.TrimSpace(expert.Name))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, expert)
	}
	return out
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
	agentProvider := opt.AgentProvider
	if agentProvider == nil && len(opt.ExpertAgents) > 0 {
		agentProvider = staticAgentProvider(opt.ExpertAgents)
	}
	if opt.MaxParallelSubagents <= 0 {
		opt.MaxParallelSubagents = defaultMaxParallelSubagents
	}
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
		exploreToolDescription: opt.ExploreDescriptionPrompt,
		runTaskToolDescription: opt.RunTaskDescriptionPrompt,
		agents:                 agentProvider,
		parallel:               make(chan struct{}, opt.MaxParallelSubagents),
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

	SelfAgent    *ExpertAgent
	ExpertAgents []ExpertAgent
	// AgentProvider is preferred over ExpertAgents for dynamic catalogs.
	// ExpertAgents remains as a backwards-compatible static source.
	AgentProvider AgentProvider
	SessionForker SessionForker

	// MaxParallelSubagents limits actively running explore and expert tasks
	// across all batches created by this hook. Queued task count is unlimited.
	// Values <= 0 use the default of 4.
	MaxParallelSubagents int
}

type ExpertAgent struct {
	Name        string
	Description string
	Agent       agents.Agent
}
