package summarize

import (
	"context"

	"github.com/basenana/friday/core/agents/react"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
)

type Agent struct {
	react  *react.Agent
	llm    openai.Client
	option Option
	logger logger.Logger
}

func (a *Agent) Chat(ctx context.Context, req *api.Request) *api.Response {
	userMessage := req.UserMessage
	if userMessage == "" {
		userMessage = DEFAULT_USER_MESSAGE
	}

	sess := req.Session
	if sess == nil {
		sess = session.New("", a.llm)
	}

	return a.react.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: userMessage,
	})
}

func New(name, desc string, llm openai.Client, option Option) *Agent {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_SUMMARIZE_PROMPT
	}
	return &Agent{
		react:  react.New(name, desc, llm, react.Option{SystemPrompt: option.SystemPrompt}),
		llm:    llm,
		option: option,
		logger: logger.New("summarize").With("name", name),
	}
}

type Option struct {
	SystemPrompt string
}
