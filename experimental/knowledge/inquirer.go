package knowledge

import (
	"context"

	"github.com/basenana/friday/core/agents/react"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers/openai"
	tools2 "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Inquirer struct {
	name   string
	desc   string
	react  *react.Agent
	store  storehouse.Storehouse
	logger *zap.SugaredLogger
}

func (q *Inquirer) Name() string {
	return q.name
}

func (q *Inquirer) Describe() string {
	return q.desc
}

func (q *Inquirer) Chat(ctx context.Context, req *api.Request) *api.Response {
	return q.react.Chat(ctx, req)
}

func NewInquirer(name, desc string, llm openai.Client, store storehouse.Storehouse, opt Option) *Inquirer {
	var searchTools []*tools2.Tool
	searchTools = append(searchTools, opt.Tools...)
	searchTools = append(searchTools, SearchTools(store)...)

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
		store:  store,
		logger: logger.New("inquirer").With(zap.String("name", name)),
	}
}
