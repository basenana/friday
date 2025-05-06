package mcp

import (
	"context"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

type Client interface {
	Initialize(ctx context.Context) error
	Tools(ctx context.Context) ([]mcp.Tool, error)
	CallTool(ctx context.Context, tool string, arguments map[string]interface{}) ([]mcp.Content, error)
}

type SSEClient struct {
	b       *Backend
	c       *client.Client
	server  string
	version string
	started bool
}

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

func (c *SSEClient) Tools(ctx context.Context) ([]mcp.Tool, error) {
	if c.b.Tools != nil {
		return c.b.Tools, nil
	}
	toolsRequest := mcp.ListToolsRequest{}
	toolListResult, err := c.c.ListTools(ctx, toolsRequest)
	if err != nil {
		return nil, err
	}

	c.b.Tools = toolListResult.Tools
	return c.b.Tools, nil
}

func (c *SSEClient) CallTool(ctx context.Context, tool string, arguments map[string]interface{}) ([]mcp.Content, error) {
	request := mcp.CallToolRequest{}
	request.Params.Name = tool
	request.Params.Arguments = arguments

	result, err := c.c.CallTool(ctx, request)
	if err != nil {
		return nil, err
	}

	return result.Content, nil
}

func NewSSEClient(name, desc, url string, headers map[string]string) (*SSEClient, error) {
	b := &Backend{
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

	return &SSEClient{b: b, c: c}, nil
}
