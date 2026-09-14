package promptcontext

import (
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

func TestSetBlockMaintainsLeadingBuiltinMessageAndOrder(t *testing.T) {
	req := providers.NewRequest("", types.Message{Role: types.RoleAgent, Content: "memory"})

	SetBlock(req, ApprovedPlan, "<approved-plan>plan</approved-plan>")
	SetBlock(req, ProjectInstructions, "<system-reminder>rules</system-reminder>")

	history := req.History()
	if len(history) != 2 || !isBuiltin(history[0]) || history[1].Content != "memory" {
		t.Fatalf("history = %#v", history)
	}
	want := "<system-reminder>rules</system-reminder>\n\n<approved-plan>plan</approved-plan>"
	if history[0].Content != want {
		t.Fatalf("builtin content = %q, want %q", history[0].Content, want)
	}
}

func TestSetBlockReplacesAndRemovesOwnedContent(t *testing.T) {
	req := providers.NewRequest("")
	SetBlock(req, ProjectInstructions, "old")
	SetBlock(req, ProjectInstructions, "new")
	if got := req.History()[0].Content; got != "new" {
		t.Fatalf("replaced content = %q", got)
	}

	SetBlock(req, ProjectInstructions, "")
	if len(req.History()) != 0 {
		t.Fatalf("empty builtin message was retained: %#v", req.History())
	}
}
