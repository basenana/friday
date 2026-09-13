package research

import (
	"testing"

	"github.com/basenana/friday/core/session"
)

func TestResearchToolContractsAreFlat(t *testing.T) {
	batch := newLeaderTool(nil, session.New("session", nil), nil, Option{})[0]
	if _, exists := batch.InputSchema.Properties["tasks"]; !exists {
		t.Fatal("tasks field missing")
	}
	for _, removed := range []string{"task_describe_list", "reasoning"} {
		if _, exists := batch.InputSchema.Properties[removed]; exists {
			t.Fatalf("removed field %q exposed", removed)
		}
	}
	if errors := batch.ValidateDefinition(2); len(errors) != 0 {
		t.Fatalf("batch definition errors: %v", errors)
	}

	report := NewReport().submitReportTool()
	if _, exists := report.InputSchema.Properties["title"]; exists {
		t.Fatal("report title should be derived from markdown")
	}
}
