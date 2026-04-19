package session

import (
	"testing"

	"github.com/basenana/friday/core/types"
)

// helpers

func userMsg(content string) types.Message {
	return types.Message{Role: types.RoleUser, Content: content}
}

func assistantMsg(content string) types.Message {
	return types.Message{Role: types.RoleAssistant, Content: content}
}

func toolCallMsg(calls ...types.ToolCall) types.Message {
	return types.Message{Role: types.RoleAssistant, ToolCalls: calls}
}

func toolResultMsg(callID, content string) types.Message {
	return types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: callID, Content: content}}
}

func tc(id, name string) types.ToolCall {
	return types.ToolCall{ID: id, Name: name}
}

// --- trimOrphanedToolCalls unit tests ---

func TestTrimOrphanedToolCalls_EmptyHistory(t *testing.T) {
	got := trimOrphanedToolCalls(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
	got = trimOrphanedToolCalls([]types.Message{})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

func TestTrimOrphanedToolCalls_NoToolCalls(t *testing.T) {
	history := []types.Message{userMsg("hi"), assistantMsg("hello")}
	got := trimOrphanedToolCalls(history)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestTrimOrphanedToolCalls_FullyPaired(t *testing.T) {
	history := []types.Message{
		userMsg("do it"),
		toolCallMsg(tc("c1", "tool_a")),
		toolResultMsg("c1", "ok"),
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 3 {
		t.Fatalf("expected 3 (all paired), got %d", len(got))
	}
}

func TestTrimOrphanedToolCalls_SingleOrphan(t *testing.T) {
	history := []types.Message{
		userMsg("do it"),
		toolCallMsg(tc("c1", "tool_a")),
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 1 {
		t.Fatalf("expected 1 (orphan trimmed), got %d", len(got))
	}
	if got[0].Role != types.RoleUser {
		t.Fatalf("expected user msg to remain, got %q", got[0].Role)
	}
}

func TestTrimOrphanedToolCalls_MultiCallPartialOrphan(t *testing.T) {
	// Two calls in one message, only one result — whole message should be trimmed
	history := []types.Message{
		userMsg("do it"),
		toolCallMsg(tc("c1", "tool_a"), tc("c2", "tool_b")),
		toolResultMsg("c1", "ok"),
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 1 {
		t.Fatalf("expected 1 (partial orphan trimmed), got %d", len(got))
	}
}

func TestTrimOrphanedToolCalls_MultiCallAllPaired(t *testing.T) {
	// Two calls, both results present
	history := []types.Message{
		userMsg("do it"),
		toolCallMsg(tc("c1", "tool_a"), tc("c2", "tool_b")),
		toolResultMsg("c1", "ok"),
		toolResultMsg("c2", "ok"),
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 4 {
		t.Fatalf("expected 4 (all paired), got %d", len(got))
	}
}

func TestTrimOrphanedToolCalls_PairedThenOrphan(t *testing.T) {
	// First tool call round is complete; second is orphaned (the subagent fork scenario)
	history := []types.Message{
		userMsg("step 1"),
		toolCallMsg(tc("c1", "tool_a")),
		toolResultMsg("c1", "done"),
		assistantMsg("thinking"),
		toolCallMsg(tc("c2", "tool_b")), // orphan — fork happens here
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 4 {
		t.Fatalf("expected 4 (orphan tail trimmed), got %d", len(got))
	}
	last := got[len(got)-1]
	if last.Content != "thinking" {
		t.Fatalf("expected last msg to be 'thinking', got %q", last.Content)
	}
}

func TestTrimOrphanedToolCalls_OrphanNotAtTail(t *testing.T) {
	// An orphaned call in the middle followed by a complete round — should not trim
	// (trimming only looks at the last tool-call message)
	history := []types.Message{
		userMsg("step 1"),
		toolCallMsg(tc("c1", "tool_a")),
		toolResultMsg("c1", "done"),
		toolCallMsg(tc("c2", "tool_b")),
		toolResultMsg("c2", "done"),
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 5 {
		t.Fatalf("expected 5 (nothing trimmed), got %d", len(got))
	}
}

func TestTrimOrphanedToolCalls_OnlyOrphanNoUser(t *testing.T) {
	history := []types.Message{
		toolCallMsg(tc("c1", "tool_a")),
	}
	got := trimOrphanedToolCalls(history)
	if len(got) != 0 {
		t.Fatalf("expected 0 (only orphan trimmed), got %d", len(got))
	}
}

// --- Fork integration tests ---

func newTestSession(history ...types.Message) *Session {
	return New("test-session", nil, WithHistory(history...))
}

func TestFork_PreservesParentHistory(t *testing.T) {
	parent := newTestSession(userMsg("hi"), assistantMsg("hello"))
	fork := parent.Fork()

	if len(fork.History) != 2 {
		t.Fatalf("expected 2 messages in fork, got %d", len(fork.History))
	}
	// Parent history must be unchanged
	if len(parent.History) != 2 {
		t.Fatalf("parent history mutated, got %d", len(parent.History))
	}
}

func TestFork_IsolatesHistory(t *testing.T) {
	parent := newTestSession(userMsg("hi"))
	fork := parent.Fork()

	fork.History = append(fork.History, assistantMsg("fork-only"))
	if len(parent.History) != 1 {
		t.Fatalf("fork mutation leaked into parent, parent has %d msgs", len(parent.History))
	}
}

func TestFork_TrimsOrphanedToolCall(t *testing.T) {
	// Simulate the subagent scenario: parent has an in-flight tool call with no result yet
	parent := newTestSession(
		userMsg("run subagent"),
		toolCallMsg(tc("c1", "run_task")), // orphan — fork happens before result is appended
	)
	fork := parent.Fork()

	if len(fork.History) != 1 {
		t.Fatalf("expected orphan trimmed in fork, got %d messages", len(fork.History))
	}
	if fork.History[0].Role != types.RoleUser {
		t.Fatalf("expected user msg in fork, got %q", fork.History[0].Role)
	}
	// Parent must still have the original 2 messages
	if len(parent.History) != 2 {
		t.Fatalf("parent history should be untouched, got %d", len(parent.History))
	}
}

func TestFork_KeepsPairedToolCallsIntact(t *testing.T) {
	parent := newTestSession(
		userMsg("do it"),
		toolCallMsg(tc("c1", "tool_a")),
		toolResultMsg("c1", "done"),
	)
	fork := parent.Fork()

	if len(fork.History) != 3 {
		t.Fatalf("expected 3 messages (paired calls kept), got %d", len(fork.History))
	}
}

func TestFork_SetsParentAndRoot(t *testing.T) {
	root := newTestSession(userMsg("root"))
	child := root.Fork()
	grandchild := child.Fork()

	if child.Parent != root {
		t.Fatal("child.Parent should be root")
	}
	if child.Root != root {
		t.Fatal("child.Root should be root")
	}
	if grandchild.Parent != child {
		t.Fatal("grandchild.Parent should be child")
	}
	if grandchild.Root != root {
		t.Fatal("grandchild.Root should be root")
	}
}

func TestFork_RegisteredInParentChildren(t *testing.T) {
	parent := newTestSession(userMsg("hi"))
	f1 := parent.Fork()
	f2 := parent.Fork()

	if len(parent.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(parent.Children))
	}
	if parent.Children[0] != f1 || parent.Children[1] != f2 {
		t.Fatal("children not registered in order")
	}
}

func TestFork_MultipleOrphanedCallsInLastMessage(t *testing.T) {
	// Three calls in one message, none have results
	parent := newTestSession(
		userMsg("batch"),
		toolCallMsg(tc("c1", "t1"), tc("c2", "t2"), tc("c3", "t3")),
	)
	fork := parent.Fork()

	if len(fork.History) != 1 {
		t.Fatalf("expected 1 (all orphans trimmed), got %d", len(fork.History))
	}
}
