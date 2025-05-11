package openai

import (
	"context"
	"fmt"
	mcpclient "github.com/basenana/friday/pkg/mcp"
	"github.com/mark3labs/mcp-go/mcp"
)

type Session struct {
	Prompt  string
	History []Message
	Tools   []MCPTool
}

func (s *Session) AddMcpServer(ctx context.Context, c mcpclient.Client) error {
	tList, err := c.GetTools(ctx)
	if err != nil {
		return fmt.Errorf("list mcp tools error %w", err)
	}

	server := &MCPServer{client: c}
	for _, t := range tList {
		s.Tools = append(s.Tools, MCPTool{
			Tool:   t,
			server: server,
		})
	}
	return nil
}

type MCPServer struct {
	client mcpclient.Client
}

type MCPTool struct {
	mcp.Tool
	server *MCPServer
}

type Message struct {
	SystemMessage    string
	UserMessage      string
	AssistantMessage string
	ToolCallID       string
	ToolContent      string
}
