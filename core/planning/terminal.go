package planning

import (
	"context"

	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

const SubmitPlanToolName = collaboration.SubmitPlanToolName

// TerminalHook makes submit_plan the terminal action of a planning turn. The
// tool call is still executed and persisted, but the react loop does not make
// a redundant follow-up model call after its result.
type TerminalHook struct {
	ModeProvider collaboration.ModeProvider
}

func (h TerminalHook) AfterModel(_ context.Context, sess *session.Session, _ providers.Request, apply *providers.Apply) error {
	if h.ModeProvider != nil && h.ModeProvider.CollaborationMode(sess.ID) != collaboration.ModePlan {
		return nil
	}
	for _, call := range apply.ToolUse {
		if call.Name == SubmitPlanToolName {
			apply.Abort = true
			return nil
		}
	}
	return nil
}

var _ session.AfterModelHook = TerminalHook{}
