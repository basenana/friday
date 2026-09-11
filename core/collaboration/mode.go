package collaboration

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

// Mode is the collaboration contract applied to a session.
type Mode string

const (
	ModeDefault Mode = "default"
	ModePlan    Mode = "plan"

	EnterPlanModeToolName    = "enter_plan_mode"
	RequestUserInputToolName = "request_user_input"
	SubmitPlanToolName       = "submit_plan"
)

func ParseMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeDefault:
		return ModeDefault, nil
	case ModePlan:
		return ModePlan, nil
	default:
		return "", fmt.Errorf("unknown collaboration mode %q", value)
	}
}

// ModeProvider resolves the durable mode for a session.
type ModeProvider interface {
	CollaborationMode(sessionID string) Mode
}

// ModeController extends ModeProvider with durable mode changes. It is used
// by actor-owned collaboration tools; read-only hooks should continue to
// depend on ModeProvider.
type ModeController interface {
	ModeProvider
	SetMode(sessionID string, mode Mode) error
}

// Hook injects mode-specific instructions immediately before each model call.
// Keeping this request-scoped means a mode change does not require replacing
// the session or its transcript.
type Hook struct {
	modes  ModeProvider
	effort string
}

func NewHook(modes ModeProvider, reasoningEffort ...string) *Hook {
	h := &Hook{modes: modes}
	if len(reasoningEffort) > 0 {
		h.effort = reasoningEffort[0]
	}
	return h
}

func (h *Hook) BeforeModel(_ context.Context, sess *session.Session, req providers.Request) error {
	if h == nil || h.modes == nil {
		return nil
	}
	if h.modes.CollaborationMode(sess.ID) != ModePlan {
		hasEnterPlanTool := hasToolDefine(req.ToolDefines(), EnterPlanModeToolName)
		req.SetToolDefines(filterToolDefines(req.ToolDefines(), func(name string) bool {
			return name != RequestUserInputToolName && name != SubmitPlanToolName
		}))
		if hasEnterPlanTool {
			req.AppendSystemPrompt(DefaultInstructions)
		}
		return nil
	}
	req.SetToolDefines(filterToolDefines(req.ToolDefines(), func(name string) bool {
		return name != EnterPlanModeToolName && name != "request_form" && name != "write_todos"
	}))
	req.AppendSystemPrompt(PlanInstructions)
	if h.effort != "" {
		providers.SetRequestReasoningEffort(req, h.effort)
	}
	return nil
}

func hasToolDefine(defines []providers.ToolDefine, name string) bool {
	for _, define := range defines {
		if define != nil && define.GetName() == name {
			return true
		}
	}
	return false
}

func filterToolDefines(defines []providers.ToolDefine, keep func(string) bool) []providers.ToolDefine {
	filtered := make([]providers.ToolDefine, 0, len(defines))
	for _, define := range defines {
		if define != nil && keep(define.GetName()) {
			filtered = append(filtered, define)
		}
	}
	return filtered
}

const DefaultInstructions = `<collaboration_mode name="default">
You may call enter_plan_mode when a task has material ambiguity, requires significant design decisions, or should be reviewed before implementation. Do not enter Plan Mode merely because a routine task has multiple steps. If you enter Plan Mode, continue the same task under the Plan Mode instructions on the next model call.
</collaboration_mode>`

const PlanInstructions = `<collaboration_mode name="plan">
You are in Plan Mode. Stay in this mode until the user explicitly exits it.

Your only objective is to produce a decision-complete implementation plan. Do not implement the plan, edit files, change configuration, or perform external side effects. A request to implement while this mode is active is a request to plan that implementation.

Work conversationally in three phases:
1. Grounding: inspect the real environment first and answer every discoverable question from evidence.
2. Intent: resolve the goal, success criteria, scope, constraints, audience, and material preferences.
3. Implementation: resolve modules, public interfaces, data flow, failure behavior, migrations, tests, and acceptance criteria.

Ask only questions that materially change the plan. Prefer request_user_input for structured choices. Do not submit a plan while a high-impact ambiguity remains.

When the plan is decision complete, call submit_plan exactly once with a concise title and complete Markdown. The Markdown must contain Summary, Implementation Changes, Test Plan, and Assumptions sections. Do not emit a duplicate prose plan after calling submit_plan.
</collaboration_mode>`

var _ session.BeforeModelHook = (*Hook)(nil)
