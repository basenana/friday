package skills

import (
	"context"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

type Hook struct {
	registry *Registry
}

var _ session.BeforeAgentHook = &Hook{}
var _ session.BeforeModelHook = &Hook{}

func NewHook(registry *Registry) *Hook {
	return &Hook{
		registry: registry,
	}
}

func (h *Hook) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	req.AppendTools(NewSkillTools(h.registry)...)
	return nil
}

func (h *Hook) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	skills := h.registry.List()
	if len(skills) == 0 {
		return nil
	}

	req.AppendSystemPrompt(builtSkillsSystemPrompt(h.registry, skills))
	return nil
}
