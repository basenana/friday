package summarize

import (
	"context"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	react  agents.Agent
	llm    providers.Client
	option Option
	logger logger.Logger
}

func (a *Agent) Chat(ctx context.Context, req *api.Request) *api.Response {
	inputRole, inputMessage := req.InputMessage()
	if inputMessage == "" {
		inputMessage = DEFAULT_USER_MESSAGE
	}

	sess := req.Session
	if sess == nil {
		sess = session.New(types.NewID(), a.llm)
	}

	forwarded := &api.Request{Session: sess}
	if inputRole == types.RoleAgent {
		forwarded.AgentMessage = inputMessage
	} else {
		forwarded.UserMessage = inputMessage
	}
	return a.react.Chat(ctx, forwarded)
}

func New(llm providers.Client, option Option) *Agent {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_SUMMARIZE_PROMPT
	}
	return &Agent{
		react:  agents.New(llm, agents.Option{SystemPrompt: option.SystemPrompt}),
		llm:    llm,
		option: option,
		logger: logger.New("summarize"),
	}
}

type Option struct {
	SystemPrompt string
}
