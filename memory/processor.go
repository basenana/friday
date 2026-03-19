package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/types"
)

type Agent interface {
	Chat(ctx context.Context, message string) *api.Response
}

type Processor struct {
	agent  Agent
	config ProcessorConfig
}

func NewProcessor(agent Agent, config ProcessorConfig) *Processor {
	return &Processor{
		agent:  agent,
		config: config,
	}
}

func (p *Processor) ProcessSession(ctx context.Context, history *SessionHistory) (string, error) {
	convText := FormatConversation(history.Messages)
	resp := p.agent.Chat(ctx,
		buildPrompt(history.ID, history.CreatedAt, convText, p.config.MemoryPath, p.config.WorkspacePath))

	result, err := api.ReadAllContent(ctx, resp)
	if err != nil {
		return "", fmt.Errorf("agent chat failed: %w", err)
	}

	return result, nil
}

func FormatConversation(messages []types.Message) string {
	if len(messages) == 0 {
		return ""
	}

	var sb strings.Builder

	for _, msg := range messages {
		switch msg.Role {
		case types.RoleUser:
			sb.WriteString(fmt.Sprintf("USER: %s", msg.Content))
		case types.RoleAssistant:
			if msg.Reasoning != "" {
				sb.WriteString(fmt.Sprintf("ASSISTANT [thinking]: %s\n", msg.Reasoning))
			}
			sb.WriteString(fmt.Sprintf("ASSISTANT: %s", msg.Content))
		case types.RoleTool:
			if msg.ToolResult != nil {
				sb.WriteString(fmt.Sprintf("TOOL RESULT: %s", msg.ToolResult.Content))
			}
		}

		// 每条消息后都添加 \n\n
		sb.WriteString("\n\n")
	}

	return sb.String()
}

type ProcessorConfig struct {
	MemoryPath    string
	WorkspacePath string
}
