package configtools

import "testing"

func TestManagementToolDefinitionsAreValid(t *testing.T) {
	store := &FileStore{}
	for _, tool := range []struct {
		name string
		tool interface{ ValidateDefinition(int) []error }
	}{
		{name: AgentToolName, tool: newAgentTool(store)},
		{name: MCPToolName, tool: newMCPTool(store)},
	} {
		if issues := tool.tool.ValidateDefinition(2); len(issues) != 0 {
			t.Fatalf("%s definition issues: %v", tool.name, issues)
		}
	}
}
