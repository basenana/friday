package planning

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
)

func TestTerminalHookAbortsAfterSubmitPlan(t *testing.T) {
	apply := &providers.Apply{ToolUse: []providers.ToolCall{{Name: SubmitPlanToolName}}}
	if err := (TerminalHook{}).AfterModel(context.Background(), session.New("s", nil), providers.NewRequest(""), apply); err != nil {
		t.Fatal(err)
	}
	if !apply.Abort {
		t.Fatal("submit_plan must terminate the react loop")
	}
}

type terminalModes collaboration.Mode

func (m terminalModes) CollaborationMode(string) collaboration.Mode { return collaboration.Mode(m) }

func TestTerminalHookIgnoresSubmitPlanOutsidePlanMode(t *testing.T) {
	apply := &providers.Apply{ToolUse: []providers.ToolCall{{Name: SubmitPlanToolName}}}
	hook := TerminalHook{ModeProvider: terminalModes(collaboration.ModeDefault)}
	if err := hook.AfterModel(context.Background(), session.New("s", nil), providers.NewRequest(""), apply); err != nil {
		t.Fatal(err)
	}
	if apply.Abort {
		t.Fatal("submit_plan name must not terminate a default-mode turn")
	}
}
