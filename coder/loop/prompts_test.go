package loop

import (
	"strings"
	"testing"
)

func TestCommonPromptExplainsTwoCyclesAndCompletionTools(t *testing.T) {
	for _, want := range []string{
		"one continuous Loop",
		"bootstrap -> develop -> update",
		"review -> revise",
		"finish_devloop is only a development-to-review handoff",
		"Only finish_reviewloop completes the Loop",
		"returns to the development or review cycle that was interrupted",
		"delegated the complete task to you for autonomous execution",
		"responsible for the correctness, completeness, and verification",
		"Never call request_user_input or enter_plan_mode",
		"choose the approach you judge most appropriate and execute it directly",
		"Record material assumptions and decisions in the Working Note",
		"Optional Observations",
	} {
		if !strings.Contains(CommonSystemPrompt, want) {
			t.Fatalf("CommonSystemPrompt missing %q:\n%s", want, CommonSystemPrompt)
		}
	}
}

func TestPhasePromptsDefineWorkBoundariesAndHandoffs(t *testing.T) {
	for name, tc := range map[string]struct {
		prompt string
		wants  []string
	}{
		"bootstrap": {
			BootstrapPrompt,
			[]string{"establish the baseline", "Do not implement production changes", "finish_devloop and finish_reviewloop are not available"},
		},
		"develop": {
			DevelopPrompt,
			[]string{"one bounded development work item", "Do not review the complete accumulated diff", "update performs the readiness decision"},
		},
		"update": {
			UpdatePrompt,
			[]string{"hand the complete candidate to final review", "Do not edit product code", "call finish_devloop last", "not final completion"},
		},
		"review": {
			ReviewPrompt,
			[]string{"complete Loop delivery", "complete set of material blocking findings", "read-only review", "Optional Observations", "call finish_reviewloop"},
		},
		"revise": {
			RevisePrompt,
			[]string{"finite set of blocking findings", "Fix only those blockers", "Do not conduct a new global review", "cannot declare review passed"},
		},
		"development recovery": {
			DevelopRecoveryPrompt,
			[]string{"Recovery target: development cycle", "Reconcile state only", "returns to develop"},
		},
		"review recovery": {
			ReviewRecoveryPrompt,
			[]string{"Recovery target: review cycle", "Do not continue a revision", "returns to review"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, section := range []string{"Purpose", "Work for this phase", "Boundary", "Complete when"} {
				if !strings.Contains(tc.prompt, section) {
					t.Fatalf("prompt does not define %q:\n%s", section, tc.prompt)
				}
			}
			for _, want := range tc.wants {
				if !strings.Contains(tc.prompt, want) {
					t.Fatalf("prompt missing %q:\n%s", want, tc.prompt)
				}
			}
		})
	}
}

func TestReviewAndRevisePreventScopeExpansion(t *testing.T) {
	for _, want := range []string{"style preferences", "unrelated legacy problems", "optional refactors", "must not enter revise work"} {
		if !strings.Contains(ReviewPrompt, want) {
			t.Fatalf("ReviewPrompt missing %q", want)
		}
	}
	for _, want := range []string{"Optional Observations", "expand scope", "unrelated abstractions", "finish_devloop and finish_reviewloop are not available"} {
		if !strings.Contains(RevisePrompt, want) {
			t.Fatalf("RevisePrompt missing %q", want)
		}
	}
}

func TestRecoveryPromptsShareTUIPhasePrefix(t *testing.T) {
	if firstLine(DevelopRecoveryPrompt) != "Phase: recovery" || firstLine(ReviewRecoveryPrompt) != "Phase: recovery" {
		t.Fatalf("recovery prompt prefixes = %q, %q", firstLine(DevelopRecoveryPrompt), firstLine(ReviewRecoveryPrompt))
	}
	if RecoveryPrompt != DevelopRecoveryPrompt {
		t.Fatal("legacy RecoveryPrompt is not the conservative development recovery prompt")
	}
}

func firstLine(text string) string {
	if before, _, ok := strings.Cut(text, "\n"); ok {
		return before
	}
	return text
}
