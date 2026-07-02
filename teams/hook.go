package teams

import (
	"context"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

// Hook injects team tools (team_load, team_list, team_comment, team_list_comments)
// into the agent and surfaces an active-team system-prompt hint.
type Hook struct {
	registry  *Registry
	teamsPath string
}

var _ session.BeforeAgentHook = &Hook{}
var _ session.BeforeModelHook = &Hook{}

// NewHook constructs a Hook that injects team tools and prompt context.
func NewHook(registry *Registry, teamsPath string) *Hook {
	return &Hook{registry: registry, teamsPath: teamsPath}
}

// BeforeAgent injects team tools on every agent invocation.
func (h *Hook) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	req.AppendTools(NewTeamTools(h.registry, h.teamsPath)...)
	return nil
}

// BeforeModel appends a short prompt describing the active team (if any).
func (h *Hook) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	t := h.registry.Active()
	if t == nil {
		return nil
	}
	var b strings.Builder
	b.WriteString("\n\n## Active Team: " + t.Name + "\n")
	b.WriteString(t.Description + "\nMembers:\n")
	for _, m := range t.Members {
		b.WriteString("  - " + m.Name + " (" + string(m.Role) + ")\n")
	}
	b.WriteString("\nUse the team_comment and team_list_comments tools to coordinate. " +
		"Use proposal_run with team=\"" + t.Name + "\" to drive a DAG through the team.\n")
	req.AppendSystemPrompt(b.String())
	return nil
}
