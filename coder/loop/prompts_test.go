package loop

import (
	"strings"
	"testing"
)

func TestNonUpdatePromptsForbidFinishLoop(t *testing.T) {
	for name, prompt := range map[string]string{
		"bootstrap": BootstrapPrompt,
		"develop":   DevelopPrompt,
		"review":    ReviewPrompt,
		"recovery":  RecoveryPrompt,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(prompt, "finish_loop is not available") {
				t.Fatalf("prompt does not forbid finish_loop:\n%s", prompt)
			}
			for _, section := range []string{"Purpose", "Work for this phase", "Boundary", "Complete when"} {
				if !strings.Contains(prompt, section) {
					t.Fatalf("prompt does not define %q:\n%s", section, prompt)
				}
			}
			if !strings.Contains(strings.ToLower(prompt), "end the turn normally") {
				t.Fatalf("prompt does not direct a normal phase handoff:\n%s", prompt)
			}
		})
	}
}

func TestCommonPromptExplainsLifecycleAndContextAwareNoteReads(t *testing.T) {
	for _, want := range []string{
		"A phase is one complete actor turn",
		"several model calls and tool calls",
		"All phases share the same session history",
		"bootstrap -> develop -> review -> update",
		"recovery runs before develop",
		"When visible context contains the complete note",
		"absent, incomplete, possibly stale",
		"information need rather than treating a read as a required phase ritual",
		"concise handoff",
	} {
		if !strings.Contains(CommonSystemPrompt, want) {
			t.Fatalf("CommonSystemPrompt missing %q:\n%s", want, CommonSystemPrompt)
		}
	}
}

func TestPhasePromptsDefineDistinctBoundaries(t *testing.T) {
	for name, tc := range map[string]struct {
		prompt string
		wants  []string
	}{
		"bootstrap": {BootstrapPrompt, []string{"plans the work", "Leave implementation"}},
		"develop":   {DevelopPrompt, []string{"Choose, implement, and verify one bounded work item", "within about one hour", "owns one work item, not the rest of the plan"}},
		"review":    {ReviewPrompt, []string{"focused review of the developed work item", "unrelated findings are not implemented here"}},
		"update":    {UpdatePrompt, []string{"accounts for work", "does not implement fixes"}},
		"recovery":  {RecoveryPrompt, []string{"reconciles state", "does not continue feature implementation"}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, want := range tc.wants {
				if !strings.Contains(tc.prompt, want) {
					t.Fatalf("prompt missing boundary %q:\n%s", want, tc.prompt)
				}
			}
		})
	}
}

func TestUpdatePromptDefinesExclusiveCompletionAudit(t *testing.T) {
	for _, want := range []string{
		"This is the only phase in which finish_loop may be called",
		"every in-scope task is complete",
		"no actionable in-scope work remains",
		"Complete path",
		"Continue path",
	} {
		if !strings.Contains(UpdatePrompt, want) {
			t.Fatalf("UpdatePrompt missing %q:\n%s", want, UpdatePrompt)
		}
	}
}
