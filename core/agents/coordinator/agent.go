package coordinator

import (
	"context"
	"fmt"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/agents/react"
	agtapi "github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	llm       openai.Client
	subAgents []ExpertAgent
	option    Option
	logger    logger.Logger
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	sess := req.Session
	if sess == nil {
		sess = session.New(types.NewID(), a.llm)
	}

	mainAgent := react.New(a.llm, react.Option{
		SystemPrompt: initSystemPrompt(a.option),
		MaxLoopTimes: 5,
		Tools:        a.buildMainAgentTools(req.Session, req.Tools),
	})

	return mainAgent.Chat(ctx, req)
}

func (a *Agent) buildMainAgentTools(sess *session.Session, reqTools []*tools.Tool) []*tools.Tool {

	subAgentTools := make([]*tools.Tool, 0)
	subAgentTools = append(subAgentTools, a.option.Tools...)
	subAgentTools = append(subAgentTools, reqTools...)

	agentTools := make([]*tools.Tool, 0, len(a.subAgents)+len(a.option.Tools))
	agentTools = append(agentTools, a.option.Tools...)

	for _, subagent := range a.subAgents {
		agentTools = append(agentTools,
			tools.NewTool(fmt.Sprintf("run_task_%s", subagent.Name),
				tools.WithDescription(subagent.Describe),
				tools.WithString("task_describe",
					tools.Required(),
					tools.Description("Provide a concise description of the problem you are requesting a solution to, along with suggestions for the expected response."),
				),
				tools.WithToolHandler(callSubagentTool(subagent.Agent, sess, subAgentTools)),
			),
		)
	}
	return agentTools
}

func New(llm openai.Client, opt Option) *Agent {
	if opt.CoordinatePrompt == "" {
		opt.CoordinatePrompt = COORDINATE_PROMPT
	}

	agt := &Agent{
		llm:       llm,
		subAgents: opt.SubAgents,
		option:    opt,
		logger:    logger.New("coordinator"),
	}

	return agt
}

type Option struct {
	SystemPrompt     string
	CoordinatePrompt string
	Tools            []*tools.Tool
	SubAgents        []ExpertAgent
}

type ExpertAgent struct {
	Name     string
	Describe string
	Agent    agents.Agent
}
