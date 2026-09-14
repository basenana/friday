package loop

import (
	"strings"
	"testing"
)

func TestNonUpdatePromptsForbidFinishLoop(t *testing.T) {
	for name, prompt := range map[string]string{
		"bootstrap": BootstrapPrompt,
		"select":    SelectPrompt,
		"develop":   DevelopPrompt,
		"review":    ReviewPrompt,
		"recovery":  RecoveryPrompt,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(prompt, "finish_loop is not available") {
				t.Fatalf("prompt does not forbid finish_loop:\n%s", prompt)
			}
			if !strings.Contains(prompt, "Working Note") || !strings.Contains(strings.ToLower(prompt), "end the turn normally") {
				t.Fatalf("prompt does not direct a normal Working Note handoff:\n%s", prompt)
			}
		})
	}
}

func TestUpdatePromptDefinesExclusiveCompletionAudit(t *testing.T) {
	for _, want := range []string{
		"This is the only phase in which finish_loop may be called",
		"Every planned task and selected task",
		"Every follow-up and next step",
		"No useful or actionable work remains",
		"Completing the most recent selected task or development slice is not sufficient",
	} {
		if !strings.Contains(UpdatePrompt, want) {
			t.Fatalf("UpdatePrompt missing %q:\n%s", want, UpdatePrompt)
		}
	}
}
