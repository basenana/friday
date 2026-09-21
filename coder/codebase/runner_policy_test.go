package codebase

import (
	"testing"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/sandbox"
)

func TestContextToolsExcludeMutationAndShell(t *testing.T) {
	all := []*tools.Tool{
		{Name: sandbox.FsReadToolName}, {Name: sandbox.FsListToolName},
		{Name: sandbox.FsFindToolName}, {Name: sandbox.FsSearchToolName},
		{Name: sandbox.FsWriteToolName}, {Name: sandbox.FsEditToolName},
		{Name: sandbox.FsDeleteToolName}, {Name: "bash"},
	}
	got := contextTools(all)
	if len(got) != 4 {
		t.Fatalf("context tools = %v, want four read-only filesystem tools", toolNames(got))
	}
	for _, tool := range got {
		switch tool.Name {
		case sandbox.FsReadToolName, sandbox.FsListToolName, sandbox.FsFindToolName, sandbox.FsSearchToolName:
		default:
			t.Fatalf("context tool %q is not read-only", tool.Name)
		}
	}
}

func TestReadOnlyGitToolExposesNoArbitraryCommand(t *testing.T) {
	tool := newReadOnlyGitTool(nil, t.TempDir())
	if tool.Name != readOnlyGitToolName {
		t.Fatalf("tool name = %q", tool.Name)
	}
	properties := tool.JsonSchema()["properties"].(map[string]interface{})
	if _, ok := properties["command"]; ok {
		t.Fatal("read-only Git tool exposes arbitrary command input")
	}
	for _, required := range []string{"operation", "path", "search"} {
		if _, ok := properties[required]; !ok {
			t.Fatalf("Git tool missing %q input", required)
		}
	}
}

func TestQueryOutputLimitAlwaysAppliesPackageCeiling(t *testing.T) {
	if got := queryOutputLimit(0); got != maxCodebaseQueryOutputRunes {
		t.Fatalf("zero caller hint limit = %d, want %d", got, maxCodebaseQueryOutputRunes)
	}
	if got := queryOutputLimit(123); got != 123 {
		t.Fatalf("smaller caller hint limit = %d, want 123", got)
	}
	if got := queryOutputLimit(maxCodebaseQueryOutputRunes + 1); got != maxCodebaseQueryOutputRunes {
		t.Fatalf("larger caller hint limit = %d, want package ceiling", got)
	}
}

func toolNames(in []*tools.Tool) []string {
	out := make([]string, 0, len(in))
	for _, tool := range in {
		out = append(out, tool.Name)
	}
	return out
}
