package skills

import (
	"context"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

type Hook struct {
	catalog Catalog
}

var _ session.BeforeAgentHook = &Hook{}
var _ session.BeforeModelHook = &Hook{}

func NewHook(catalog Catalog) *Hook {
	return &Hook{
		catalog: catalog,
	}
}

func (h *Hook) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	if h == nil || h.catalog == nil || req == nil {
		return nil
	}
	req.AppendTools(NewSkillTools(h.catalog)...)
	return nil
}

func (h *Hook) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	if h == nil || h.catalog == nil || req == nil {
		return nil
	}
	skills := h.catalog.List()
	if len(skills) == 0 {
		return nil
	}

	req.AppendSystemPrompt(builtSkillsSystemPrompt(h.catalog, skills))
	return nil
}
