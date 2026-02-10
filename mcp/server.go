package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/basenana/friday/core/tools"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type Server struct {
	Name     string
	Describe string

	SSE *MCPSse

	client *client.Client
}

func (s *Server) Connect() error {
	httpTransport, err := transport.NewStreamableHTTP(s.SSE.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport: %w", err)
	}
	s.client = client.NewClient(httpTransport)
	return nil
}

func (s *Server) InitTools(ctx context.Context) ([]*tools.Tool, error) {
	result, err := s.client.ListTools(ctx, mcp.ListToolsRequest{Header: s.sseHeaders()})
	if err != nil {
		return nil, err
	}
	tools := make([]*tools.Tool, len(result.Tools))
	for i := range result.Tools {
		tool := &result.Tools[i]
		tools[i] = covertMCPTool(tool)
		tools[i].Handler = s.mcpToolAdaptor(tool)
	}
	return tools, nil
}

func (s *Server) sseHeaders() http.Header {
	if s.SSE.Headers == nil {
		return http.Header{}
	}
	h := http.Header{}
	for k, v := range s.SSE.Headers {
		h.Set(k, v)
	}
	return h
}

func (s *Server) mcpToolAdaptor(mcpTool *mcp.Tool) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		result, err := s.client.CallTool(ctx, mcp.CallToolRequest{
			Request: mcp.Request{},
			Header:  s.sseHeaders(),
			Params: mcp.CallToolParams{
				Name:      "",
				Arguments: request.Arguments,
				Meta:      nil,
			},
		})
		if err != nil {
			return nil, err
		}
		return tools.NewToolResultText(tools.Res2Str(result)), nil
	}
}

func covertMCPTool(tool *mcp.Tool) *tools.Tool {
	return &tools.Tool{
		Name:        tool.Name,
		Description: tool.Description,
		Annotations: make(map[string]string),
		InputSchema: tools.ToolInputSchema{
			Type:       tool.InputSchema.Type,
			Properties: tool.InputSchema.Properties,
			Required:   tool.InputSchema.Required,
		},
	}

}

type MCPSse struct {
	Endpoint string
	Headers  map[string]string
}
