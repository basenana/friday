package summarize

import (
	"context"
	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Agent struct {
	react  *react.Agent
	option Option
	logger *zap.SugaredLogger
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	userMessage := req.UserMessage
	if userMessage == "" {
		userMessage = DEFAULT_USER_MESSAGE
	}
	return a.react.Chat(ctx, &agtapi.Request{
		Session:     req.Session,
		UserMessage: userMessage,
		Memory:      memory.NewEmpty(req.Session.ID, memory.WithHistory(req.Memory.History()...), memory.WithUnlimitedSession()),
	})
}

func New(name, desc string, llm openai.Client, option Option) *Agent {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_SUMMARIZE_PROMPT
	}
	return &Agent{
		react:  react.New(name, desc, llm, react.Option{SystemPrompt: option.SystemPrompt}),
		option: option,
		logger: logger.New("summarize").With(zap.String("name", name)),
	}
}

type Option struct {
	SystemPrompt string
}
