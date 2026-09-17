package mcp

import (
	"context"
	"sort"

	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

// ToolProvider is the narrow capability required by Hook. Implementations own
// any caching or reload policy; Hook deliberately asks for a fresh view on
// every agent run.
type ToolProvider interface {
	Tools() []*tools.Tool
}

// Hook injects the provider's latest tool snapshot before every agent run. It
// is inherited by forked sessions, so explorer agents see the
// same shared MCP registry.
type Hook struct{ provider ToolProvider }

func NewHook(provider ToolProvider) *Hook { return &Hook{provider: provider} }

func (h *Hook) BeforeAgent(_ context.Context, _ *coresession.Session, req coresession.AgentRequest) error {
	if h == nil || h.provider == nil || req == nil {
		return nil
	}
	available := append([]*tools.Tool(nil), h.provider.Tools()...)
	sort.SliceStable(available, func(i, j int) bool {
		if available[i] == nil {
			return false
		}
		if available[j] == nil {
			return true
		}
		if available[i].Name != available[j].Name {
			return available[i].Name < available[j].Name
		}
		return available[i].Description < available[j].Description
	})
	existing := make(map[string]struct{})
	for _, tool := range req.GetTools() {
		if tool == nil {
			continue
		}
		existing[tool.Name] = struct{}{}
	}
	for _, tool := range available {
		if tool == nil {
			continue
		}
		if _, ok := existing[tool.Name]; !ok {
			req.AppendTools(tool)
			existing[tool.Name] = struct{}{}
		}
	}
	return nil
}
