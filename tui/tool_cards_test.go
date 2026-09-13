package tui

import (
	"strings"
	"testing"
)

func TestBuiltinToolCardPresentations(t *testing.T) {
	configureTheme(true)
	m := &model{width: 100}
	tests := []struct {
		name, args, wantTitle, wantBody string
	}{
		{"write_todos", `{"todo_list":[]}`, "Update todos", ""},
		{"fs_read", `{"path":"main.go"}`, "Read file", "main.go"},
		{"fs_list", `{}`, "List directory", "."},
		{"fs_write", `{"path":"out.txt","content":"hello"}`, "Write file", "5 bytes"},
		{"fs_edit", `{"path":"main.go","search_string":"old","replace_string":"new","occurrences":"all"}`, "Edit file", "scope · all"},
		{"fs_mkdir", `{"path":"build"}`, "Create directory", "build"},
		{"fs_delete", `{"path":"old.txt"}`, "Delete", "old.txt"},
		{"bash", `{"command":"go test ./tui","workdir":"/repo","timeout":"30s"}`, "Run command", "go test ./tui"},
		{"poll_wait", `{"command":"curl localhost","interval":"2s","max_timeout":"1m"}`, "Poll command", "max timeout · 1m"},
		{"background_task", `{"command":"make serve"}`, "Start background task", "make serve"},
		{"list_tasks", `{}`, "List background tasks", "status · all"},
		{"wait_task", `{"task_id":"abc"}`, "Wait for task", "timeout · 60s"},
		{"kill_task", `{"task_id":"abc"}`, "Stop task", "task · abc"},
		{"explore", `{"task":"inspect the TUI"}`, "Explore", "inspect the TUI"},
		{"run_task", `{"agent_name":"reviewer","task":"review changes"}`, "Delegate to reviewer", "review changes"},
		{"run_blocking_subagents", `{"tasks":["inspect UI","inspect events"]}`, "Run 2 subagents", "2. inspect events"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			block := &chatBlock{toolName: test.name, toolArgs: test.args, toolArgsComplete: true}
			got := m.presentTool(block)
			if !got.specialized || got.title != test.wantTitle || !strings.Contains(terminalSafe(got.body), test.wantBody) {
				t.Fatalf("presentation = %#v", got)
			}
		})
	}
}

func TestSpecializedToolCardsHideOutputAndKeepDetails(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{
		id: "call-specialized", kind: blockToolCall, toolName: "run_task",
		toolArgs: `{"agent_name":"reviewer","task":"review this patch"}`, toolArgsComplete: true,
		toolOutput: "SECRET LONG SUBAGENT REPORT", success: true,
	}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "Delegate to reviewer") || !strings.Contains(card, "review this patch") {
		t.Fatalf("card = %q", card)
	}
	if strings.Contains(card, "SECRET") {
		t.Fatalf("specialized card exposed output: %q", card)
	}
	detail := toolDetailContent(block)
	if !strings.Contains(detail, "SECRET LONG SUBAGENT REPORT") || !strings.Contains(detail, "review this patch") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestShowCommandRevealsSpecializedToolDetails(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 30
	m.messages = []chatBlock{{
		id: "call-specialized", kind: blockToolCall, toolName: "explore",
		toolArgs: `{"task":"inspect events"}`, toolArgsComplete: true,
		toolOutput: "full exploration report", success: true,
	}}
	m.handleShowCommand([]string{"call-spe"})
	if m.detail == nil {
		t.Fatal("show command did not open tool details")
	}
	detail := terminalSafe(m.detail.View(m.width))
	if !strings.Contains(detail, "inspect events") || !strings.Contains(detail, "full exploration report") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestSpecializedFailureOffersDetails(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{id: "123456789", toolName: "bash", toolArgs: `{"command":"false"}`, toolArgsComplete: true, toolOutput: "failed output"}
	card := terminalSafe(m.renderToolCard(block))
	if strings.Contains(card, "failed output") || !strings.Contains(card, "/show 12345678 · error details") {
		t.Fatalf("card = %q", card)
	}
}

func TestGenericToolCardStillShowsArgumentsAndResult(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{toolName: "mcp_search", toolArgs: `{"query":"friday"}`, toolArgsComplete: true, toolOutput: "one result", success: true}
	card := terminalSafe(m.renderToolCard(block))
	for _, want := range []string{"mcp_search", "Arguments", "query", "friday", "Result", "one result"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q: %q", want, card)
		}
	}
}

func TestFsWriteAndEditUseCompactArgumentPreviews(t *testing.T) {
	configureTheme(true)
	m := &model{width: 50}
	write := &chatBlock{toolName: "fs_write", toolArgs: `{"path":"secret.txt","content":"do-not-render"}`, toolArgsComplete: true, success: true}
	if card := terminalSafe(m.renderToolCard(write)); strings.Contains(card, "do-not-render") || !strings.Contains(card, "13 bytes") {
		t.Fatalf("write card = %q", card)
	}
	edit := &chatBlock{toolName: "fs_edit", toolArgs: `{"path":"main.go","search_string":"first\nsecond","replace_string":"next\nline"}`, toolArgsComplete: true, success: true}
	card := terminalSafe(m.renderToolCard(edit))
	if !strings.Contains(card, "first · +1 lines") || !strings.Contains(card, "next · +1 lines") {
		t.Fatalf("edit card = %q", card)
	}
}

func TestPartialToolArgumentsDoNotRenderBrokenJSON(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{toolName: "bash", toolArgs: `{"command":"go test`, pending: true}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "receiving arguments") || strings.Contains(card, `{"command"`) {
		t.Fatalf("card = %q", card)
	}
}

func TestInterruptedToolHasDistinctState(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{toolName: "bash", toolArgs: `{"command":"sleep 10"}`, toolArgsComplete: true, interrupted: true}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "■ Run command") || !strings.Contains(card, "interrupted") || strings.Contains(card, "✗") {
		t.Fatalf("card = %q", card)
	}
}
