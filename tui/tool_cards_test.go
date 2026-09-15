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
		{"request_user_input", `{"question_1":"Which scope?","options_1":["Small","Large"]}`, "Ask user", ""},
		{"submit_plan", `{"markdown":"# Plan\\n\\nComplete plan"}`, "Submit plan", ""},
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
		{"run_task", `{"agent_name":"analyst","task":"analyze changes"}`, "Delegate to analyst", "analyze changes"},
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
		toolArgs: `{"agent_name":"analyst","task":"analyze this patch"}`, toolArgsComplete: true,
		toolOutput: "SECRET LONG SUBAGENT REPORT", success: true,
	}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "Delegate to analyst") || !strings.Contains(card, "analyze this patch") {
		t.Fatalf("card = %q", card)
	}
	if strings.Contains(card, "SECRET") {
		t.Fatalf("specialized card exposed output: %q", card)
	}
	detail := toolDetailContent(block)
	if !strings.Contains(detail, "SECRET LONG SUBAGENT REPORT") || !strings.Contains(detail, "analyze this patch") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestLoadSkillCardOnlyShowsNameAndDescription(t *testing.T) {
	configureTheme(true)
	m := &model{width: 100}
	block := &chatBlock{
		id: "call-load-skill", kind: blockToolCall, toolName: "load_skill",
		toolArgs: `{"name":"writer"}`, toolArgsComplete: true,
		toolOutput: `{"name":"writer","description":"Draft release notes","dir_path":"/skills/writer","instructions":"SECRET WORKFLOW","allowed_tools":"fs_read"}`,
		success:    true,
	}
	card := terminalSafe(m.renderToolCard(block))
	for _, want := range []string{"Load skill", "name · writer", "description · Draft release notes"} {
		if !strings.Contains(card, want) {
			t.Fatalf("load_skill card missing %q: %q", want, card)
		}
	}
	for _, hidden := range []string{"Arguments", "Result", "SECRET WORKFLOW", "/skills/writer", "allowed_tools", "fs_read"} {
		if strings.Contains(card, hidden) {
			t.Fatalf("load_skill card exposed %q: %q", hidden, card)
		}
	}
	detail := toolDetailContent(block)
	if !strings.Contains(detail, "SECRET WORKFLOW") || !strings.Contains(detail, "/skills/writer") {
		t.Fatalf("load_skill details lost full result: %q", detail)
	}
}

func TestLoadSkillCardNeverShowsIncompleteArguments(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{toolName: "load_skill", toolArgs: `{"name":"secret`, pending: true}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "Load skill") || strings.Contains(card, "secret") || strings.Contains(card, "receiving arguments") {
		t.Fatalf("partial load_skill card = %q", card)
	}
}

func TestRequestUserInputToolCardDefersQuestionsToInteractiveCard(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{toolName: "request_user_input", toolArgs: `{"question_1":"Which scope?","options_1":["Small","Large"]}`, toolArgsComplete: true, pending: true}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "Ask user") || strings.Contains(card, "Which scope?") || strings.Contains(card, "question_1") {
		t.Fatalf("request_user_input tool card = %q", card)
	}
}

func TestSubmitPlanToolCardDefersMarkdownToPlanCard(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{
		id: "call-submit-plan", kind: blockToolCall, toolName: "submit_plan",
		toolArgs: `{"markdown":"# Plan\n\n## Summary\n\nComplete plan"}`, toolArgsComplete: true,
		toolOutput: "Plan plan-1 version 1 submitted.", success: true,
	}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "Submit plan") || strings.Contains(card, "Complete plan") || strings.Contains(card, "Arguments") {
		t.Fatalf("submit_plan tool card = %q", card)
	}
	if detail := toolDetailContent(block); !strings.Contains(detail, "Complete plan") {
		t.Fatalf("submit_plan details lost markdown: %q", detail)
	}
}

func TestPartialSubmitPlanArgumentsRemainHidden(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{toolName: "submit_plan", toolArgs: `{"markdown":"# Secret`, pending: true}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "Submit plan") || strings.Contains(card, "Secret") || strings.Contains(card, "receiving arguments") {
		t.Fatalf("partial submit_plan tool card = %q", card)
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

func TestSpecializedFailureShowsArgumentsAndErrorWithoutShowHint(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{id: "123456789", toolName: "bash", toolArgs: `{"command":"false"}`, toolArgsComplete: true, toolOutput: "failed output"}
	card := terminalSafe(m.renderToolCard(block))
	if !strings.Contains(card, "$ false") || !strings.Contains(card, "Error") || !strings.Contains(card, "failed output") || strings.Contains(card, "/show") {
		t.Fatalf("card = %q", card)
	}
}

func TestFailedReadFileKeepsPathBeforeError(t *testing.T) {
	configureTheme(true)
	m := &model{width: 100}
	block := &chatBlock{
		toolName: "fs_read", toolArgs: `{"path":"/outside/MEMORY.md"}`, toolArgsComplete: true,
		toolOutput: "Error: invalid path: path is outside readable roots\nSuggestion: use a readable path inside the allowed workdir",
	}
	card := terminalSafe(m.renderToolCard(block))
	pathIndex := strings.Index(card, "path · /outside/MEMORY.md")
	errorIndex := strings.Index(card, "Error")
	if pathIndex < 0 || errorIndex < 0 || pathIndex > errorIndex {
		t.Fatalf("failed read card should show its path before the error: %q", card)
	}
}

func TestFailedExploreKeepsTaskBeforeError(t *testing.T) {
	configureTheme(true)
	m := &model{width: 120}
	block := &chatBlock{
		toolName: "explore", toolArgs: `{"task":"inspect the TUI tool card rendering"}`, toolArgsComplete: true,
		toolOutput: "Error: Subagent mode active: nested subagent creation is not supported.\nSuggestion: complete the assigned task directly.",
	}
	card := terminalSafe(m.renderToolCard(block))
	taskIndex := strings.Index(card, "task · inspect the TUI tool card rendering")
	errorIndex := strings.Index(card, "Error")
	if taskIndex < 0 || errorIndex < 0 || taskIndex > errorIndex {
		t.Fatalf("failed explore card should show its task before the error: %q", card)
	}
}

func TestToolFailureShowsThreeLinesThenFullDetailsHint(t *testing.T) {
	configureTheme(true)
	m := &model{width: 100}
	block := &chatBlock{id: "123456789", toolName: "bash", toolArgs: `{"command":"false"}`, toolArgsComplete: true,
		toolOutput: "Error: first\nsecond\nthird\nfourth\nfifth"}
	card := terminalSafe(m.renderToolCard(block))
	for _, want := range []string{"$ false", "first", "second", "third", "/show 12345678 · full details"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q: %q", want, card)
		}
	}
	if strings.Contains(card, "fourth") || strings.Contains(card, "error details") {
		t.Fatalf("card exposed more than three error lines: %q", card)
	}
}

func TestGenericToolFailureShowsArgumentsOnceBeforeError(t *testing.T) {
	configureTheme(true)
	m := &model{width: 80}
	block := &chatBlock{
		toolName: "mcp_search", toolArgs: `{"query":"friday"}`, toolArgsComplete: true,
		toolOutput: "Error: search unavailable",
	}
	card := terminalSafe(m.renderToolCard(block))
	for _, want := range []string{"Arguments", "query", "friday", "Error", "search unavailable"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q: %q", want, card)
		}
	}
	if strings.Contains(card, "Result") || strings.Count(card, "search unavailable") != 1 {
		t.Fatalf("failed result should only appear in the error section: %q", card)
	}
}

func TestToolErrorTextExtractsStructuredError(t *testing.T) {
	raw := `Error: {"content":[{"type":"text","text":"bad parameter\nSuggestion: use a string"}],"is_error":true}`
	if got := toolErrorText(raw); got != "bad parameter\nSuggestion: use a string" {
		t.Fatalf("toolErrorText() = %q", got)
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
