package tools

import (
	"context"
	"encoding/json"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type MCPClient interface {
	Initialize(ctx context.Context) error
	GetTools(ctx context.Context) ([]mcp.Tool, error)
	CallTool(ctx context.Context, tool string, arguments map[string]interface{}) (string, error)
}

type SSEClient struct {
	Enable      bool
	Name        string
	Description string
	Type        string // SSE

	// SSE
	URL     string
	Headers map[string]string

	tools     []mcp.Tool
	prompt    []mcp.Prompt
	resources []mcp.Resource
	c         *client.Client
	server    string
	version   string
	started   bool
}

var _ MCPClient = &SSEClient{}

func (c *SSEClient) Initialize(ctx context.Context) error {
	if c.started {
		return nil
	}
	err := c.c.Start(ctx)
	if err != nil {
		return err
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "friday",
		Version: "1.0.0",
	}

	result, err := c.c.Initialize(ctx, initRequest)
	if err != nil {
		return err
	}

	c.server = result.ServerInfo.Name
	c.version = result.ServerInfo.Version

	if err = c.c.Ping(ctx); err != nil {
		_ = c.c.Close()
		return err
	}
	c.started = true
	return nil
}

func (c *SSEClient) GetTools(ctx context.Context) ([]mcp.Tool, error) {
	if c.tools != nil {
		return c.tools, nil
	}
	toolsRequest := mcp.ListToolsRequest{}
	toolListResult, err := c.c.ListTools(ctx, toolsRequest)
	if err != nil {
		return nil, err
	}

	c.tools = toolListResult.Tools
	return c.tools, nil
}

func (c *SSEClient) CallTool(ctx context.Context, tool string, arguments map[string]interface{}) (string, error) {
	request := mcp.CallToolRequest{}
	request.Params.Name = tool
	request.Params.Arguments = arguments

	result, err := c.c.CallTool(ctx, request)
	if err != nil {
		return "", err
	}

	var content string
	for _, c := range result.Content {
		raw, err := json.Marshal(c)
		if err != nil {
			return content, err
		}
		content += string(raw)
	}
	return content, nil
}

func NewSSEClient(name, desc, url string, headers map[string]string) (MCPClient, error) {
	sse := &SSEClient{
		Enable:      false,
		Name:        name,
		Description: desc,
		Type:        "sse",
		URL:         url,
		Headers:     headers,
	}

	c, err := client.NewSSEMCPClient(url, client.WithHeaders(headers))
	if err != nil {
		return nil, err
	}
	sse.c = c

	return sse, nil
}

func LoadMCPTools(ctx context.Context, client MCPClient) ([]Tool, error) {
	toolInfos, err := client.GetTools(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]Tool, 0, len(toolInfos))
	for i := range toolInfos {
		define := toolInfos[i]
		result = append(result, &mcpTool{
			define: define,
			client: client,
		})
	}
	return result, nil
}

type mcpTool struct {
	define mcp.Tool
	client MCPClient
}

func (m *mcpTool) Name() string {
	return m.define.Name
}

func (m *mcpTool) Description() string {
	return m.define.Description
}

func (m *mcpTool) APISchema() map[string]any {
	return map[string]interface{}{"type": "object", "properties": m.define.InputSchema.Properties}
}

func (m *mcpTool) Call(ctx context.Context, tool string, arguments map[string]interface{}) (string, error) {
	return m.client.CallTool(ctx, tool, arguments)
}

var _ Tool = &mcpTool{}
