package proposals

import (
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/teams"
)

// SessionFactory returns a persistent session for a given key (typically a
// member name within a proposal). Callers back this with the file store so
// sessions survive crashes.
type SessionFactory func(proposalID, assignee string) (*session.Session, error)

// TeamStrategy drives Plan/Review through the team leader and Execute through
// the assigned member. Each member gets a persistent session keyed by the
// (proposal, member) pair so context accumulates across that member's tasks.
type TeamStrategy struct {
	client         providers.Client
	agentFactory   AgentFactory
	team           *teams.Team
	members        []teams.Member
	tools          []*tools.Tool
	teamsPath      string
	teamsRegistry  *teams.Registry
	sessionFactory SessionFactory

	planTmpl    *template.Template
	executeTmpl *template.Template
	reviewTmpl  *template.Template
}

// TeamStrategyOption configures the strategy.
type TeamStrategyOption func(*TeamStrategy)

// WithTeamPromptTemplates overrides default phase templates.
func WithTeamPromptTemplates(plan, execute, review *template.Template) TeamStrategyOption {
	return func(s *TeamStrategy) {
		if plan != nil {
			s.planTmpl = plan
		}
		if execute != nil {
			s.executeTmpl = execute
		}
		if review != nil {
			s.reviewTmpl = review
		}
	}
}

// NewTeamStrategy constructs a TeamStrategy.
func NewTeamStrategy(
	client providers.Client,
	factory AgentFactory,
	team *teams.Team,
	members []teams.Member,
	allTools []*tools.Tool,
	teamsPath string,
	registry *teams.Registry,
	sessionFactory SessionFactory,
	opts ...TeamStrategyOption,
) (*TeamStrategy, error) {
	leader := teams.Leader(members)
	if leader == nil {
		return nil, fmt.Errorf("team %s has no leader", team.Name)
	}
	s := &TeamStrategy{
		client:         client,
		agentFactory:   factory,
		team:           team,
		members:        members,
		tools:          allTools,
		teamsPath:      teamsPath,
		teamsRegistry:  registry,
		sessionFactory: sessionFactory,
		planTmpl:       template.Must(template.New("team-plan").Parse(PlanPromptTmpl)),
		executeTmpl:    template.Must(template.New("team-execute").Parse(ExecutePromptTmpl)),
		reviewTmpl:     template.Must(template.New("team-review").Parse(ReviewPromptTmpl)),
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Plan uses the leader's session + StructuredPredict to derive the DAG.
func (s *TeamStrategy) Plan(ctx context.Context, proposal *Proposal) ([]Task, error) {
	designDoc, err := loadDesignDocOrFallback(proposal.ID)
	if err != nil {
		return nil, err
	}
	var buf strings.Builder
	if err := s.planTmpl.Execute(&buf, map[string]string{"DesignDoc": designDoc}); err != nil {
		return nil, err
	}
	prompt := buf.String()

	leader, leaderSession, err := s.leaderSessionForProposal(proposal)
	if err != nil {
		return nil, err
	}
	systemPrompt := composeLeaderPrompt(leader)

	var tasks []Task
	if err := structuredPredictWithSession(ctx, s.client, leaderSession, systemPrompt, prompt, &tasks); err != nil {
		return nil, fmt.Errorf("team plan: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("team plan produced no tasks")
	}
	for i := range tasks {
		if tasks[i].Status == "" {
			tasks[i].Status = TaskPending
		}
		if tasks[i].Deps == nil {
			tasks[i].Deps = []string{}
		}
	}
	return tasks, nil
}

// Execute dispatches the task to its assignee's session.
func (s *TeamStrategy) Execute(ctx context.Context, proposal *Proposal, task *Task) (string, error) {
	member := s.findAssignee(task)
	if member == nil {
		return "", fmt.Errorf("no member assignable for task %s (assignee=%q)", task.ID, task.Assignee)
	}

	sess, err := s.sessionFactory(proposal.ID, member.Name)
	if err != nil {
		return "", fmt.Errorf("member session: %w", err)
	}

	taskDoc, err := loadTaskDocOrFallback(proposal.ID, task.ID, task.Title)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := s.executeTmpl.Execute(&buf, map[string]string{
		"TaskID":  task.ID,
		"TaskDoc": taskDoc,
	}); err != nil {
		return "", err
	}

	systemPrompt := composeMemberPrompt(member)
	agent := s.agentFactory(systemPrompt, filterTools(s.tools, member.ToolsAllow))
	req := &api.Request{
		Session:     sess,
		UserMessage: buf.String(),
		Tools:       append(memberExtraTools(s.teamsPath, s.teamsRegistry, s.team, proposal, task), filterTools(s.tools, member.ToolsAllow)...),
	}
	content, err := api.ReadAllContent(ctx, agent.Chat(ctx, req))
	if err != nil {
		return "", fmt.Errorf("team execute %s: %w", task.ID, err)
	}

	// Surface progress as a team comment (best-effort; ignore error).
	_ = teams.AppendComment(s.teamsPath, s.team.Name, teams.Comment{
		From:   member.Name,
		Text:   fmt.Sprintf("%s %s executed", task.ID, task.Title),
		Kind:   teams.CommentKindProgress,
		Anchor: teams.Anchor{ProposalID: proposal.ID, TaskID: task.ID},
	})
	return content, nil
}

// Review uses the leader's session to judge the result.
func (s *TeamStrategy) Review(ctx context.Context, proposal *Proposal, task *Task, result string) (Decision, error) {
	taskDoc, err := loadTaskDocOrFallback(proposal.ID, task.ID, task.Title)
	if err != nil {
		return Decision{}, err
	}

	var buf strings.Builder
	if err := s.reviewTmpl.Execute(&buf, map[string]string{
		"TaskID":  task.ID,
		"TaskDoc": taskDoc,
		"Result":  result,
	}); err != nil {
		return Decision{}, err
	}
	prompt := buf.String()

	leader, leaderSession, err := s.leaderSessionForProposal(proposal)
	if err != nil {
		return Decision{}, err
	}
	systemPrompt := composeLeaderPrompt(leader)

	var dec Decision
	if err := structuredPredictWithSession(ctx, s.client, leaderSession, systemPrompt, prompt, &dec); err != nil {
		return Decision{}, fmt.Errorf("team review %s: %w", task.ID, err)
	}
	dec.Status = strings.ToLower(strings.TrimSpace(dec.Status))
	switch dec.Status {
	case "approved", "rejected", "failed":
	default:
		return Decision{}, fmt.Errorf("team review %s returned invalid status %q", task.ID, dec.Status)
	}

	// Surface the review decision as a team comment (best-effort).
	_ = teams.AppendComment(s.teamsPath, s.team.Name, teams.Comment{
		From:   leader.Name,
		Text:   fmt.Sprintf("%s → %s: %s", task.ID, dec.Status, dec.Comment),
		Kind:   teams.CommentKindReview,
		Anchor: teams.Anchor{ProposalID: proposal.ID, TaskID: task.ID},
	})
	return dec, nil
}

func (s *TeamStrategy) leaderSessionForProposal(proposal *Proposal) (*teams.Member, *session.Session, error) {
	leader := teams.Leader(s.members)
	if leader == nil {
		return nil, nil, fmt.Errorf("team %s has no leader", s.team.Name)
	}
	leaderSession, err := s.sessionFactory(proposal.ID, leader.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("leader session: %w", err)
	}
	return leader, leaderSession, nil
}

// findAssignee returns the member matching the task's Assignee field. If empty,
// returns the leader (sensible default for tasks without explicit assignment).
func (s *TeamStrategy) findAssignee(task *Task) *teams.Member {
	if task.Assignee == "" {
		return teams.Leader(s.members)
	}
	if m := teams.FindMember(s.members, task.Assignee); m != nil {
		return m
	}
	return nil
}

// filterTools returns the subset of tools whose names appear in allow.
// If allow is empty/missing, all tools are returned (no filtering).
func filterTools(all []*tools.Tool, allow []string) []*tools.Tool {
	if len(allow) == 0 {
		return all
	}
	allowed := make(map[string]bool, len(allow))
	for _, n := range allow {
		allowed[strings.ToLower(n)] = true
	}
	var out []*tools.Tool
	for _, t := range all {
		if allowed[strings.ToLower(t.Name)] {
			out = append(out, t)
		}
	}
	return out
}

// memberExtraTools injects coordination tools (team_comment etc.) into the
// member's toolset so they can post anchored comments during execution.
func memberExtraTools(teamsPath string, registry *teams.Registry, team *teams.Team, proposal *Proposal, task *Task) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("team_comment",
			tools.WithDescription("Post a comment to the active team's collective log (e.g. surface a blocker, question, or progress)."),
			tools.WithString("text", tools.Required()),
			tools.WithString("kind", tools.Description("review|note|question|progress")),
			tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
				text, _ := req.Arguments["text"].(string)
				kind, _ := req.Arguments["kind"].(string)
				if text == "" {
					return tools.NewToolResultError("text required"), nil
				}
				err := teams.AppendComment(teamsPath, team.Name, teams.Comment{
					From: "member",
					Text: text,
					Kind: teams.CommentKind(kind),
					Anchor: teams.Anchor{
						ProposalID: proposal.ID,
						TaskID:     task.ID,
					},
				})
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}
				return tools.NewToolResultText("posted"), nil
			}),
		),
	}
}

func composeLeaderPrompt(m *teams.Member) string {
	if m == nil {
		return ""
	}
	return m.Instructions + "\n" + teams.LeaderSystemPrompt
}

func composeMemberPrompt(m *teams.Member) string {
	if m == nil {
		return ""
	}
	// Minimal template substitution (no html/template to avoid escaping).
	skills := strings.Join(m.Skills, ", ")
	if skills == "" {
		skills = "(none declared)"
	}
	out := strings.ReplaceAll(teams.MemberSystemPromptTmpl, "{{.Name}}", m.Name)
	out = strings.ReplaceAll(out, "{{.Role}}", string(m.Role))
	out = strings.ReplaceAll(out, "{{.Skills}}", skills)
	out = strings.ReplaceAll(out, "{{.Instructions}}", m.Instructions)
	return m.Instructions + "\n" + out
}
