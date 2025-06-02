package agent

import (
	"context"
	"fmt"
	"github.com/basenana/friday/pkg/llm/client/openai"
	mcpclient "github.com/basenana/friday/pkg/mcp"
)

type Agent struct {
	systemPrompt string
	session      *openai.Session
	mcpClients   []mcpclient.Client
	client       *openai.Client
}

func (a *Agent) Chat(ctx context.Context, message string, option Option) (*Reply, error) {
	if a.session == nil {
		a.session = &openai.Session{
			Prompt:  a.systemPrompt,
			History: make([]openai.Message, 0),
		}

		for _, s := range a.mcpClients {
			if err := a.session.AddMcpServer(ctx, s); err != nil {
				return nil, fmt.Errorf("failed to add mcp server: %w", err)
			}
		}
	}

	var (
		reply  = &Reply{Delta: make(chan string, 5)}
		stream = a.client.Chat(ctx, message, a.session)
	)

	go func() {
		for {
			select {
			case <-ctx.Done():
				reply.Error <- ctx.Err()
				return
			case err := <-stream.Error():
				reply.Error <- err
				return
			case msg, ok := <-stream.Message():
				if !ok {
					return
				}
				reply.Delta <- msg
			}
		}
	}()
	return reply, nil
}

func New(prompt string, client *openai.Client, mcpClients []mcpclient.Client) *Agent {
	return &Agent{
		systemPrompt: prompt,
		mcpClients:   mcpClients,
		client:       client,
	}
}
