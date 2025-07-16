package agent

import (
	"context"
	"github.com/basenana/friday/pkg/llm/client/openai"
	"github.com/basenana/friday/pkg/tools"
)

type Agent struct {
	systemPrompt string
	session      *openai.Session
	tools        []tools.Tool
	client       *openai.Client
}

func (a *Agent) Chat(ctx context.Context, message string, option Option) (*Reply, error) {
	if a.session == nil {
		a.session = &openai.Session{
			Prompt:  a.systemPrompt,
			History: make([]openai.Message, 0),
			Tools:   a.tools,
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

func (a *Agent) Reset(ctx context.Context) error {
	a.session = nil
	return nil
}

func New(prompt string, client *openai.Client, tools []tools.Tool) *Agent {
	return &Agent{
		systemPrompt: prompt,
		tools:        tools,
		client:       client,
	}
}
