package mcp

import "github.com/mark3labs/mcp-go/mcp"

type Backend struct {
	Enable      bool
	Name        string
	Description string
	Type        string // SSE

	// SSE
	URL     string
	Headers map[string]string

	Tools     []mcp.Tool
	Prompt    []mcp.Prompt
	Resources []mcp.Resource
}
