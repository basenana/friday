package configtools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/tools"
)

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

func invokeAgentTool(t *testing.T, tool *tools.Tool, arguments map[string]any) (*tools.Result, map[string]any) {
	t.Helper()
	result, err := tool.Handler(context.Background(), &tools.Request{Arguments: arguments})
	if err != nil {
		t.Fatalf("handler error for %v: %v", arguments["action"], err)
	}
	text, _ := result.Content[0].(tools.TextContent)
	var payload map[string]any
	_ = json.Unmarshal([]byte(text.Text), &payload)
	return result, payload
}

func TestAgentToolCreateUpdateModelFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	store := NewFileStore([]string{root}, nil, fixedCatalog{[]string{"MiniMax-M3", "glm-5.3"}})
	tool := newAgentTool(store)

	schema := tool.JsonSchema()
	properties := schema["properties"].(map[string]any)
	modelProperty := properties["model"].(map[string]any)
	if description, _ := modelProperty["description"].(string); !strings.Contains(description, "MiniMax-M3, glm-5.3") {
		t.Fatalf("model description = %q", description)
	}

	result, payload := invokeAgentTool(t, tool, map[string]any{
		"action": "create", "name": "writer", "description": "Writes",
		"instructions": "Write clearly.", "model": "minimax-m3",
		"effort": "high", "max_loop_times": float64(5),
	})
	if result.IsError || payload["created"] != "writer" {
		t.Fatalf("create result = %+v payload = %#v", result, payload)
	}
	spec, err := store.GetAgent("writer")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Model != "MiniMax-M3" || spec.Effort != "high" || spec.MaxLoopTimes != 5 {
		t.Fatalf("created spec = %#v", spec)
	}

	_, payload = invokeAgentTool(t, tool, map[string]any{"action": "get", "name": "writer"})
	for key, want := range map[string]any{
		"name": "writer", "description": "Writes", "instructions": "Write clearly.",
		"model": "MiniMax-M3", "effort": "high", "max_loop_times": float64(5),
	} {
		if payload[key] != want {
			t.Fatalf("get %s = %#v, want %#v", key, payload[key], want)
		}
	}

	_, payload = invokeAgentTool(t, tool, map[string]any{"action": "update", "name": "writer", "model": "glm-5.3"})
	if result.IsError || payload["updated"] != "writer" {
		t.Fatalf("update payload = %#v", payload)
	}
	spec, err = store.GetAgent("writer")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Model != "glm-5.3" || spec.Effort != "high" || spec.MaxLoopTimes != 5 {
		t.Fatalf("updated spec = %#v", spec)
	}

	result, _ = invokeAgentTool(t, tool, map[string]any{"action": "create", "name": "bad", "instructions": "x", "model": "does-not-exist"})
	if !result.IsError {
		t.Fatal("unknown model create should fail")
	}
	text, _ := result.Content[0].(tools.TextContent)
	if !strings.Contains(text.Text, "available models: MiniMax-M3, glm-5.3") {
		t.Fatalf("unknown model error = %q", text.Text)
	}
	if _, err := store.GetAgent("bad"); err == nil {
		t.Fatal("failed create should not persist an agent")
	}

	result, _ = invokeAgentTool(t, tool, map[string]any{"action": "delete", "name": "writer"})
	if !result.IsError || !strings.Contains(result.Content[0].(tools.TextContent).Text, "confirm_delete") {
		t.Fatalf("unconfirmed delete result = %+v", result)
	}
	result, payload = invokeAgentTool(t, tool, map[string]any{"action": "delete", "name": "writer", "confirm_delete": true})
	if result.IsError || payload["deleted"] != "writer" {
		t.Fatalf("delete result = %+v payload = %#v", result, payload)
	}
	if _, err := store.GetAgent("writer"); err == nil {
		t.Fatal("agent should be deleted")
	}
}
