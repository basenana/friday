package proposals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/teams"
)

// RunnerFactory builds a Runner from a freshly-created Proposal. The factory
// is responsible for picking the right ExecutionStrategy (SingleAgent vs Team)
// and constructing the agents / sessions it needs. Injected by setup.go.
type RunnerFactory func(proposal *Proposal, designDoc string) (*Runner, ExecutionStrategy, error)

// Hook injects the proposal_run tool into the main agent on every agent
// invocation. The factory is invoked when the agent calls proposal_run.
type Hook struct {
	loader  *Loader
	factory RunnerFactory
}

var _ session.BeforeAgentHook = &Hook{}

// NewHook constructs a Hook. The factory decides strategy wiring.
func NewHook(loader *Loader, factory RunnerFactory) *Hook {
	return &Hook{loader: loader, factory: factory}
}

// BeforeAgent injects proposal_run.
func (h *Hook) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	req.AppendTools(ProposalRunTool(h.loader, h.factory))
	return nil
}

// ProposalRunTool returns the proposal_run tool. Exported so setup.go can
// inject it directly if it prefers tool composition over the hook.
func ProposalRunTool(loader *Loader, factory RunnerFactory) *tools.Tool {
	return tools.NewTool("proposal_run",
		tools.WithDescription(
			"Create and execute a proposal. Takes over the agent control loop until the task DAG completes or fails. "+
				"Use this when a request is large enough to warrant splitting into a planned, reviewed workflow."),
		tools.WithString("title", tools.Required(), tools.Description("Short proposal title")),
		tools.WithString("content", tools.Required(), tools.Description("Markdown design document describing goals, scope, and acceptance criteria")),
		tools.WithString("team", tools.Description("Team name to use (Team Strategy). Omit for single-agent mode.")),
		tools.WithToolHandler(proposalRunHandler(loader, factory)),
	)
}

func proposalRunHandler(loader *Loader, factory RunnerFactory) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		title, _ := req.Arguments["title"].(string)
		content, _ := req.Arguments["content"].(string)
		if title == "" || content == "" {
			return tools.NewToolResultError("title and content are required"), nil
		}
		proposalID := newProposalID(title)

		proposal := &Proposal{
			ID:        proposalID,
			Title:     title,
			Status:    ProposalDraft,
			Sessions:  map[string]string{},
			CreatedAt: time.Now().UTC(),
		}
		if team, ok := req.Arguments["team"].(string); ok && team != "" {
			proposal.OwningTeam = team
		}
		if err := loader.InitProposal(proposal, content); err != nil {
			return tools.NewToolResultError(fmt.Sprintf("init proposal: %v", err)), nil
		}

		runner, _, err := factory(proposal, content)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("build runner: %v", err)), nil
		}

		summary, err := runner.Run(ctx)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("run proposal: %v", err)), nil
		}
		return tools.NewToolResultText(summary), nil
	}
}

// newProposalID derives a short slug from the title + timestamp.
func newProposalID(title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ' || r == '-' || r == '_':
			return '-'
		}
		return -1
	}, slug)
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "proposal"
	}
	return fmt.Sprintf("%s-%d", slug, time.Now().Unix())
}

// AffectedTeamRef is a hint that callers can stuff into the factory closure
// to surface which team (if any) the proposal used. Reserved for future use.
type AffectedTeamRef struct{ Team *teams.Team }
