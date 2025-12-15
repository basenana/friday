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

type Learner struct {
	name   string
	desc   string
	react  *react.Agent
	llm    openai.Client
	store  storehouse.Sotrehouse
	logger *zap.SugaredLogger
}

func (l *Learner) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	return l.react.Chat(ctx, req)
}

func NewLearner(name, desc string, llm openai.Client, store storehouse.Sotrehouse, chunkType string, chunkMetadata map[string]string, opt Option) *Learner {
	var searchTools []*tools.Tool
	searchTools = append(searchTools, opt.Tools...)
	searchTools = append(searchTools, storehouseTools(store, chunkType, chunkMetadata)...)

	if opt.SystemPrompt == "" {
		opt.SystemPrompt = DEFAULT_LEARNER_PROMPT
	}

	return &Learner{
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
		logger: logger.New("learner"),
	}
}
