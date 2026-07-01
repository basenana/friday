package actor

import (
	"context"
	"encoding/json"

	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

// emitFn is the subset of *Actor.emit that hooks need. Decoupling makes the
// hook trivially testable without spinning up a real Actor.
type emitFn func(Event)

// actorHook injects actor capabilities into a freshly-built core Agent via
// the session hook system. The only capability today is the emit_activity
// tool, which lets the agent push structured cards (plan / progress / ...)
// into the event stream on its own initiative.
//
// Pattern mirrors core/planning/todo.go: BeforeAgent injects a tool whose
// handler produces events; the hook itself holds no *Actor reference.
type actorHook struct {
	emit  emitFn
	runID string
}

var _ coreSession.BeforeAgentHook = (*actorHook)(nil)
var _ coreSession.AfterToolHook = (*actorHook)(nil)

func newActorHook(emit emitFn, runID string) *actorHook {
	return &actorHook{emit: emit, runID: runID}
}

// BeforeAgent injects the activity tool so the agent can emit structured
// cards during a run.
func (h *actorHook) BeforeAgent(ctx context.Context, sess *coreSession.Session, req coreSession.AgentRequest) error {
	req.AppendTools(h.activityTool())
	return nil
}

// AfterTool is reserved for future per-batch state projection. It is a no-op
// for now; defined so the hook satisfies AfterToolHook if registered as one.
func (h *actorHook) AfterTool(ctx context.Context, sess *coreSession.Session, payload coreSession.ToolPayload) error {
	return nil
}

// activityTool returns the "emit_activity" tool definition. When the agent
// invokes it, an ACTIVITY_SNAPSHOT event is pushed onto the actor outcome.
func (h *actorHook) activityTool() *tools.Tool {
	const (
		name        = "emit_activity"
		description = "Emit a structured activity card visible to the user (e.g. PLAN, SEARCH, PROGRESS)."
	)
	return tools.NewTool(name,
		tools.WithDescription(description),
		tools.WithString("activity_type",
			tools.Description("Type of activity: PLAN, SEARCH, PROGRESS, etc."),
			tools.Required(),
		),
		tools.WithString("content",
			tools.Description("JSON-encoded structured content for the activity card."),
			tools.Required(),
		),
		tools.WithToolHandler(func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			activityType, _ := req.Arguments["activity_type"].(string)
			contentRaw, _ := req.Arguments["content"].(string)

			var content any
			if err := json.Unmarshal([]byte(contentRaw), &content); err != nil {
				// Fall back to raw text so the agent still gets feedback.
				content = contentRaw
			}

			h.emit(Event{
				Type:  EventActivitySnapshot,
				RunID: h.runID,
				Data: map[string]any{
					"activity_type": activityType,
					"content":       content,
				},
			})

			return tools.NewToolResultText("activity emitted"), nil
		}),
	)
}
