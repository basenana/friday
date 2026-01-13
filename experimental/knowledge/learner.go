package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/basenana/friday/core/agents/agtapi"
	"github.com/basenana/friday/core/agents/react"
	"github.com/basenana/friday/core/providers/openai"
	tools2 "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Learner struct {
	chunkType     string
	chunkMetadata map[string]string
	react         *react.Agent
	store         storehouse.Storehouse
	logger        *zap.SugaredLogger
}

func (l *Learner) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	buf := &bytes.Buffer{}
	buf.WriteString("Instructions: Analyze the following text, break it down into different knowledge cards, and save them to the knowledge base using the save_knowledge_to_base tool.\n")
	buf.WriteString("---\n")
	buf.WriteString(req.UserMessage)

	return l.react.Chat(ctx, &agtapi.Request{
		Session:     req.Session,
		UserMessage: buf.String(),
		ImageURLs:   req.ImageURLs,
		Memory:      nil, // create new memory in react loop
	})
}

func (l *Learner) storehouseTools() []*tools2.Tool {
	common := []*tools2.Tool{
		tools2.NewTool("save_knowledge_to_base",
			tools2.WithDescription("Save knowledge card into the knowledge base for subsequent recall and utilization."),
			tools2.WithString("card_content",
				tools2.Required(),
				tools2.Description("The content of the knowledge card, Do not exceed 500 words"),
			),
			tools2.WithToolHandler(func(ctx context.Context, request *tools2.Request) (*tools2.Result, error) {
				content, ok := request.Arguments["card_content"].(string)
				if !ok || content == "" {
					return nil, fmt.Errorf("missing required parameter: card_content")
				}

				chunks, err := l.store.SaveChunks(ctx, &types.Chunk{
					ID:       "",
					Type:     l.chunkType,
					Metadata: l.chunkMetadata,
					Content:  content,
				})

				if err != nil {
					return tools2.NewToolResultError(err.Error()), nil
				}

				if len(chunks) != 1 {
					return tools2.NewToolResultError("Expected 1 chunk but got " + strconv.Itoa(len(chunks))), nil
				}

				return tools2.NewToolResultText(fmt.Sprintf("chunk %s saved", chunks[0].ID)), nil
			}),
		),
	}

	common = append(common, SearchTools(l.store, l.chunkType)...)
	return common
}

func NewLearner(name, desc string, llm openai.Client, store storehouse.Storehouse, chunkType string, chunkMetadata map[string]string, opt Option) *Learner {
	if opt.SystemPrompt == "" {
		opt.SystemPrompt = DEFAULT_LEARNER_PROMPT
	}

	if chunkMetadata == nil {
		chunkMetadata = map[string]string{}
	}
	chunkMetadata["friday.agent"] = "learner"

	learner := &Learner{
		chunkType:     chunkType,
		chunkMetadata: chunkMetadata,
		store:         store,
		logger:        logger.New("learner").With(zap.String("name", name)),
	}

	var searchTools []*tools2.Tool
	searchTools = append(searchTools, opt.Tools...)
	searchTools = append(searchTools, learner.storehouseTools()...)

	learner.react = react.New("learner", "", llm, react.Option{
		SystemPrompt: opt.SystemPrompt,
		MaxLoopTimes: 5,
		MaxToolCalls: 50,
		Tools:        searchTools,
	})

	return learner
}
