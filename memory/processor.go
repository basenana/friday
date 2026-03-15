package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/types"
)

type Processor struct {
	agent  agents.Agent
	config ProcessorConfig
}

func NewProcessor(agent agents.Agent, config ProcessorConfig) *Processor {
	if config.RecentDays <= 0 {
		config.RecentDays = 5
	}
	return &Processor{
		agent:  agent,
		config: config,
	}
}

func (p *Processor) ProcessSession(ctx context.Context, history *SessionHistory) (string, error) {
	convText := FormatConversation(history.Messages)
	req := &api.Request{
		UserMessage: buildPrompt(history.ID, history.CreatedAt, convText),
	}

	resp := p.agent.Chat(ctx, req)
	response, err := api.ReadAllContent(ctx, resp)
	if err != nil {
		return "", fmt.Errorf("agent chat failed: %w", err)
	}

	return response, nil
}

func FormatConversation(messages []types.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}

		switch msg.Role {
		case types.RoleUser:
			sb.WriteString(fmt.Sprintf("USER:\n%s\n", msg.Content))
		case types.RoleAssistant:
			sb.WriteString(fmt.Sprintf("ASSISTANT:\n%s\n", msg.Content))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

type ProcessorConfig struct {
	MemoryPath    string
	WorkspacePath string
	RecentDays    int
}
