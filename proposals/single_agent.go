package proposals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

// AgentFactory builds the agent used for each phase. Strategies receive this
// rather than constructing agents themselves, keeping the proposal package
// decoupled from concrete agent wiring.
type AgentFactory func(systemPrompt string, tools []*tools.Tool) agents.Agent

// SingleAgentStrategy drives all three phases through one persistent session.
// The same agent is reused for Plan, Execute, and Review so that context
// accumulates across tasks within the proposal.
type SingleAgentStrategy struct {
	client       providers.Client
	agentFactory AgentFactory
	tools        []*tools.Tool
	session      *session.Session
	systemPrompt string

	// promptTmpl may be overridden for testing; defaults come from prompts.go.
	planTmpl    *template.Template
	executeTmpl *template.Template
	reviewTmpl  *template.Template
}

// SingleAgentOption configures the strategy.
type SingleAgentOption func(*SingleAgentStrategy)

// WithSingleAgentPromptTemplates overrides the default phase prompt templates.
func WithSingleAgentPromptTemplates(plan, execute, review *template.Template) SingleAgentOption {
	return func(s *SingleAgentStrategy) {
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

// NewSingleAgentStrategy constructs the strategy. The session must outlive the
// proposal run (file-backed sessions resume across crashes).
func NewSingleAgentStrategy(
	client providers.Client,
	factory AgentFactory,
	tools []*tools.Tool,
	sess *session.Session,
	systemPrompt string,
	opts ...SingleAgentOption,
) *SingleAgentStrategy {
	s := &SingleAgentStrategy{
		client:       client,
		agentFactory: factory,
		tools:        tools,
		session:      sess,
		systemPrompt: systemPrompt,
		planTmpl:     template.Must(template.New("plan").Parse(PlanPromptTmpl)),
		executeTmpl:  template.Must(template.New("execute").Parse(ExecutePromptTmpl)),
		reviewTmpl:   template.Must(template.New("review").Parse(ReviewPromptTmpl)),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Plan asks the LLM to read the proposal design doc and produce a task DAG.
func (s *SingleAgentStrategy) Plan(ctx context.Context, proposal *Proposal) ([]Task, error) {
	designDoc, err := loadDesignDocOrFallback(proposal.ID)
	if err != nil {
		return nil, err
	}

	var buf strings.Builder
	if err := s.planTmpl.Execute(&buf, map[string]string{"DesignDoc": designDoc}); err != nil {
		return nil, err
	}

	var tasks []Task
	if err := s.structuredPredict(ctx, buf.String(), &tasks); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("plan produced no tasks")
	}
	// Normalize: ensure IDs and status defaults.
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

// Execute runs one task against the persistent session.
func (s *SingleAgentStrategy) Execute(ctx context.Context, proposal *Proposal, task *Task) (string, error) {
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

	agent := s.agentFactory(s.systemPrompt, s.tools)
	req := &api.Request{
		Session:     s.session,
		UserMessage: buf.String(),
		Tools:       s.tools,
	}
	content, err := api.ReadAllContent(ctx, agent.Chat(ctx, req))
	if err != nil {
		return "", fmt.Errorf("execute %s: %w", task.ID, err)
	}
	return content, nil
}

// Review judges an execution result. Returns a Decision.
func (s *SingleAgentStrategy) Review(ctx context.Context, proposal *Proposal, task *Task, result string) (Decision, error) {
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

	var dec Decision
	if err := s.structuredPredict(ctx, buf.String(), &dec); err != nil {
		return Decision{}, fmt.Errorf("review %s: %w", task.ID, err)
	}
	dec.Status = strings.ToLower(strings.TrimSpace(dec.Status))
	switch dec.Status {
	case "approved", "rejected", "failed":
	default:
		return Decision{}, fmt.Errorf("review %s returned invalid status %q", task.ID, dec.Status)
	}
	return dec, nil
}

// structuredPredict wraps client.StructuredPredict with a fresh request
// built from the session history + a new user prompt so the prediction has
// accumulated context across tasks.
func (s *SingleAgentStrategy) structuredPredict(ctx context.Context, prompt string, out interface{}) error {
	return structuredPredictWithSession(ctx, s.client, s.session, s.systemPrompt, prompt, out)
}

// loadDesignDocOrFallback tries the loader-backed path; if unavailable,
// returns an empty doc so planning can still proceed (caller may inject doc).
func loadDesignDocOrFallback(proposalID string) (string, error) {
	if globalLoader != nil {
		return globalLoader.LoadDesignDoc(proposalID)
	}
	return "", nil
}

func loadTaskDocOrFallback(proposalID, taskID, title string) (string, error) {
	if globalLoader != nil {
		if doc, err := globalLoader.LoadTaskDoc(proposalID, taskID); err == nil {
			return doc, nil
		}
	}
	return fmt.Sprintf("# %s %s\n\n(No task brief written; title only.)", taskID, title), nil
}

// globalLoader is set by the runner before invoking strategies. Kept at
// package level so the strategy implementation stays simple; this avoids
// threading the loader through every method signature.
var globalLoader *Loader

// SetGlobalLoader wires the package-level loader used by the strategies.
// Called by the ProposalRunner on construction.
func SetGlobalLoader(l *Loader) { globalLoader = l }

func structuredPredictWithSession(
	ctx context.Context,
	client providers.Client,
	sess *session.Session,
	systemPrompt, prompt string,
	out interface{},
) error {
	req := providers.NewRequest(systemPrompt, sess.GetHistory()...)
	req.AppendHistory(types.Message{Role: types.RoleUser, Content: prompt})
	if err := sess.RunHooks(ctx, types.SessionHookBeforeModel, session.HookPayload{ModelRequest: req}); err != nil {
		return err
	}
	if err := client.StructuredPredict(ctx, req, out); err != nil {
		return err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	sess.AppendMessage(
		&types.Message{Role: types.RoleUser, Content: prompt},
		&types.Message{Role: types.RoleAssistant, Content: string(data)},
	)
	return nil
}

// ParseTasksFromJSON is a helper for tests / external callers that want to
// parse plan output without going through the LLM.
func ParseTasksFromJSON(raw string) ([]Task, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var tasks []Task
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}
