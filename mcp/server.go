package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

// Client returns the underlying MCP client, exposing it so callers can run
// protocol initialization (Start + Initialize) before invoking InitTools.
func (s *Server) Client() *client.Client { return s.client }

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
				Name:      mcpTool.Name,
				Arguments: request.Arguments,
				Meta:      nil,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("MCP tool %s request failed: %w", mcpTool.Name, err)
		}
		if result == nil {
			return nil, fmt.Errorf("MCP tool %s returned no response", mcpTool.Name)
		}
		return convertMCPResult(mcpTool.Name, result), nil
	}
}

func convertMCPResult(toolName string, result *mcp.CallToolResult) *tools.Result {
	if !result.IsError {
		return tools.NewToolResultText(tools.Res2Str(result))
	}
	cause := strings.TrimSpace(mcpTextContent(result.Content))
	if cause == "" {
		cause = fmt.Sprintf("MCP tool %s failed without a text error message", toolName)
	} else {
		cause = fmt.Sprintf("MCP tool %s failed: %s", toolName, cause)
	}
	toolResult := tools.NewToolResultActionableError(cause,
		"follow the MCP server error, correct the arguments or prerequisites, and then retry")
	toolResult.FYI = "Full MCP response:\n" + tools.Res2Str(result)
	return toolResult
}

func mcpTextContent(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if text, ok := item.(mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			parts = append(parts, strings.TrimSpace(text.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func covertMCPTool(tool *mcp.Tool) *tools.Tool {
	converted := &tools.Tool{
		Name:        tool.Name,
		Description: tool.Description,
		Annotations: make(map[string]string),
		InputSchema: tools.ToolInputSchema{
			Type:       tool.InputSchema.Type,
			Properties: tool.InputSchema.Properties,
			Required:   tool.InputSchema.Required,
		},
	}
	var schemaRaw []byte
	if len(tool.RawInputSchema) > 0 {
		schemaRaw = tool.RawInputSchema
	} else {
		schemaRaw, _ = json.Marshal(tool.InputSchema)
	}
	if len(schemaRaw) > 0 {
		_ = json.Unmarshal(schemaRaw, &converted.RawInputSchema)
	}
	return converted
}

type MCPSse struct {
	Endpoint string
	Headers  map[string]string
}
