package agents

import (
	"testing"

	"github.com/basenana/friday/core/tools"
)

func makeTools(names ...string) []*tools.Tool {
	out := make([]*tools.Tool, 0, len(names))
	for _, n := range names {
		out = append(out, tools.NewTool(n))
	}
	return out
}

func toolNames(ts []*tools.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

func TestToolPolicy_EmptyKeepsAll(t *testing.T) {
	all := makeTools("a", "b", "c")
	got := ToolPolicy{}.Apply(all)
	if len(got) != len(all) {
		t.Fatalf("empty policy changed tool count: got %d want %d", len(got), len(all))
	}
}

func TestToolPolicy_AllowWhitelist(t *testing.T) {
	all := makeTools("fs_read", "fs_write", "bash", "fs_list")
	got := ToolPolicy{Allow: []string{"fs_read", "fs_list"}}.Apply(all)
	want := []string{"fs_read", "fs_list"}
	gotNames := toolNames(got)
	if len(gotNames) != len(want) {
		t.Fatalf("allow list filter: got %v want %v", gotNames, want)
	}
	for i, n := range want {
		if gotNames[i] != n {
			t.Errorf("allow list filter: got[%d]=%q want %q", i, gotNames[i], n)
		}
	}
}

func TestToolPolicy_DenyBlacklist(t *testing.T) {
	all := makeTools("fs_read", "fs_write", "bash", "fs_list")
	got := ToolPolicy{Deny: []string{"fs_write", "bash"}}.Apply(all)
	want := []string{"fs_read", "fs_list"}
	gotNames := toolNames(got)
	if len(gotNames) != len(want) {
		t.Fatalf("deny list filter: got %v want %v", gotNames, want)
	}
}

func TestToolPolicy_AllowTakesPrecedenceOverDeny(t *testing.T) {
	// When both Allow and Deny are set, Allow wins.
	all := makeTools("a", "b", "c")
	got := ToolPolicy{Allow: []string{"a"}, Deny: []string{"a", "b"}}.Apply(all)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("allow should take precedence: got %v", toolNames(got))
	}
}

func TestToolPolicy_AllowUnknownNamesIgnored(t *testing.T) {
	all := makeTools("a", "b")
	got := ToolPolicy{Allow: []string{"a", "xyz"}}.Apply(all)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("unknown allow names should be ignored: got %v", toolNames(got))
	}
}
