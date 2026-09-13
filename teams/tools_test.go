package teams

import "testing"

func TestTeamToolContracts(t *testing.T) {
	toolset := NewTeamTools(&Registry{}, ".")
	comment := toolset[2]
	if errors := comment.ValidateDefinition(2); len(errors) != 0 {
		t.Fatalf("team_comment definition errors: %v", errors)
	}
	if comment.InputSchema.Properties["to"].(map[string]any)["type"] != "array" {
		t.Fatal("team_comment.to must be a string array")
	}

	limit := toolset[3].InputSchema.Properties["limit"].(map[string]any)
	if limit["type"] != "integer" || limit["minimum"] != float64(1) {
		t.Fatalf("limit schema = %#v", limit)
	}
}
