package mcp

import (
	"context"

	coresession "github.com/basenana/friday/core/session"
)

// Hook injects the manager's latest tool snapshot before every agent run. It
// is inherited by forked sessions, so explorer and proposal agents see the
// same shared MCP registry.
type Hook struct{ manager *Manager }

func NewHook(manager *Manager) *Hook { return &Hook{manager: manager} }

func (h *Hook) BeforeAgent(_ context.Context, _ *coresession.Session, req coresession.AgentRequest) error {
	if h == nil || h.manager == nil || req == nil {
		return nil
	}
	existing := make(map[string]struct{})
	for _, tool := range req.GetTools() {
		existing[tool.Name] = struct{}{}
	}
	for _, tool := range h.manager.Tools() {
		if _, ok := existing[tool.Name]; !ok {
			req.AppendTools(tool)
		}
	}
	return nil
}
