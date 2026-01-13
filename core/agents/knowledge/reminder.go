package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/basenana/friday/core/agents/agtapi"
	"github.com/basenana/friday/core/agents/react"
	"github.com/basenana/friday/core/providers/openai"
	tools2 "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Reminder struct {
	name          string
	desc          string
	chunkMetadata map[string]string
	react         *react.Agent
	llm           openai.Client
	store         storehouse.Storehouse
	logger        *zap.SugaredLogger
}

func (r *Reminder) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	return r.react.Chat(ctx, &agtapi.Request{
		Session:     req.Session,
		UserMessage: "Please actively organize and tidy up your memory cards based on historical messages, and save them using save_memory_card_to_base tool for quick recall later.",
		ImageURLs:   req.ImageURLs,
		Memory:      req.Memory,
	})
}

func (r *Reminder) storehouseTools() []*tools2.Tool {
	common := []*tools2.Tool{
		tools2.NewTool("save_memory_card_to_base",
			tools2.WithDescription("Store facts, events, and important information in your memory so you can quickly recall what happened or important information later."),
			tools2.WithString("overview",
				tools2.Required(),
				tools2.Description("Summarize the content to be memorized in one sentence for quick retrieval. Do not exceed 50 words."),
			),
			tools2.WithString("details",
				tools2.Required(),
				tools2.Description("A complete memory needs to contain enough details for recall, including cause, process, and result. Do not exceed 500 words"),
			),
			tools2.WithString("relevant",
				tools2.Required(),
				tools2.Description("Describe the person or thing related to this memory. Do not exceed 50 words."),
			),
			tools2.WithString("comment",
				tools2.Required(),
				tools2.Description("Your own subjective evaluation of this memory is used to quickly recall your current state. Do not exceed 100 words."),
			),
			tools2.WithString("time_of_occurrence",
				tools2.Description("If this memory has a time of occurrence, provide a time string according to the RFC3339 standard, such as 2006-01-02T15:04:05Z07:00. If no time is specified, pass blank."),
			),
			tools2.WithToolHandler(func(ctx context.Context, request *tools2.Request) (*tools2.Result, error) {
				overview, ok := request.Arguments["overview"].(string)
				if !ok || overview == "" {
					return nil, fmt.Errorf("missing required parameter: overview")
				}
				details, ok := request.Arguments["details"].(string)
				if !ok || details == "" {
					return nil, fmt.Errorf("missing required parameter: details")
				}
				relevant, ok := request.Arguments["relevant"].(string)
				if !ok || relevant == "" {
					return nil, fmt.Errorf("missing required parameter: relevant")
				}
				comment, ok := request.Arguments["comment"].(string)
				if !ok || comment == "" {
					return nil, fmt.Errorf("missing required parameter: comment")
				}
				timeOfOccurrence, ok := request.Arguments["time_of_occurrence"].(string)
				if !ok || timeOfOccurrence == "" || timeOfOccurrence == time.RFC3339 {
					timeOfOccurrence = time.Now().Format(time.RFC3339)
				}

				memTime, err := time.Parse(time.RFC3339, timeOfOccurrence)
				if err != nil {
					memTime = time.Now()
				}

				err = r.store.AppendMemories(ctx, &types.Memory{
					ID:        "",
					Type:      types.TypeMemory,
					Overview:  overview,
					Details:   details,
					Relevant:  relevant,
					Comment:   comment,
					Metadata:  r.chunkMetadata,
					CreatedAt: memTime,
				})

				if err != nil {
					return tools2.NewToolResultError(err.Error()), nil
				}

				return tools2.NewToolResultText("Memory has been updated."), nil
			}),
		),
	}

	common = append(common, tools2.SearchTools(r.store)...)
	return common
}

func NewReminder(name, desc string, llm openai.Client, store storehouse.Storehouse, chunkMetadata map[string]string, opt Option) *Reminder {
	if opt.SystemPrompt == "" {
		opt.SystemPrompt = DEFAULT_REMINDER_PROMPT
	}

	if chunkMetadata == nil {
		chunkMetadata = map[string]string{}
	}
	chunkMetadata["friday.agent"] = "reminder"

	reminder := &Reminder{
		chunkMetadata: chunkMetadata,
		llm:           llm,
		store:         store,
		logger:        logger.New("reminder").With(zap.String("name", name)),
	}

	var searchTools []*tools2.Tool
	searchTools = append(searchTools, opt.Tools...)
	searchTools = append(searchTools, reminder.storehouseTools()...)

	reminder.react = react.New("reminder", "", llm, react.Option{
		SystemPrompt: opt.SystemPrompt,
		MaxLoopTimes: 5,
		MaxToolCalls: 50,
		Tools:        searchTools,
	})

	return reminder
}
