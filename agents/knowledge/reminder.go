package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/tools"
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

func (r *Reminder) storehouseTools() []*tools.Tool {
	common := []*tools.Tool{
		tools.NewTool("save_memory_card_to_base",
			tools.WithDescription("Store facts, events, and important information in your memory so you can quickly recall what happened or important information later."),
			tools.WithString("overview",
				tools.Required(),
				tools.Description("Summarize the content to be memorized in one sentence for quick retrieval. Do not exceed 50 words."),
			),
			tools.WithString("details",
				tools.Required(),
				tools.Description("A complete memory needs to contain enough details for recall, including cause, process, and result. Do not exceed 500 words"),
			),
			tools.WithString("relevant",
				tools.Required(),
				tools.Description("Describe the person or thing related to this memory. Do not exceed 50 words."),
			),
			tools.WithString("comment",
				tools.Required(),
				tools.Description("Your own subjective evaluation of this memory is used to quickly recall your current state. Do not exceed 100 words."),
			),
			tools.WithString("time_of_occurrence",
				tools.Description("If this memory has a time of occurrence, provide a time string according to the RFC3339 standard, such as 2006-01-02T15:04:05Z07:00. If no time is specified, pass blank."),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
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
					ID:       "",
					Overview: overview,
					Details:  details,
					Relevant: relevant,
					Comment:  comment,
					Metadata: r.chunkMetadata,
					Time:     memTime,
				})

				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText("Memory has been updated."), nil
			}),
		),
	}

	common = append(common, r.store.SearchTools()...)
	return common
}

func NewReminder(llm openai.Client, store storehouse.Storehouse, chunkMetadata map[string]string, opt Option) *Reminder {
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
		logger:        logger.New("reminder"),
	}

	var searchTools []*tools.Tool
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
