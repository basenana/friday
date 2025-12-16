package knowledge

import (
	"context"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Inquirer struct {
	name   string
	desc   string
	react  *react.Agent
	llm    openai.Client
	store  storehouse.Storehouse
	logger *zap.SugaredLogger
}

func (q *Inquirer) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	return q.react.Chat(ctx, req)
}

func NewInquirer(name, desc string, llm openai.Client, store storehouse.Storehouse, opt Option) *Inquirer {
	var searchTools []*tools.Tool
	searchTools = append(searchTools, opt.Tools...)
	searchTools = append(searchTools, store.SearchTools()...)

	if opt.SystemPrompt == "" {
		opt.SystemPrompt = DEFAULT_INQUIRER_PROMPT
	}

	return &Inquirer{
		name: name,
		desc: desc,
		react: react.New(name, desc, llm, react.Option{
			SystemPrompt: opt.SystemPrompt,
			MaxLoopTimes: 5,
			MaxToolCalls: 20,
			Tools:        searchTools,
		}),
		llm:    llm,
		store:  store,
		logger: logger.New("inquirer"),
	}
}
