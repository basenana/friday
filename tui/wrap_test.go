package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestConversationBlocksWrapLongLinesWithinTerminalWidth(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	m.appendBlock(chatBlock{kind: blockUser, content: strings.Repeat("a", 160) + "USER_TAIL_MARK"})
	m.appendBlock(chatBlock{kind: blockAssistant, content: "链接：https://example.com/" + strings.Repeat("path/", 40) + "URL_TAIL_MARK"})
	m.appendBlock(chatBlock{kind: blockAssistant, content: "长文本：" + strings.Repeat("无空格连续中文", 30) + "中文尾部标记"})
	m.appendBlock(chatBlock{kind: blockError, content: strings.Repeat("E", 150) + "ERR_TAIL_MARK"})
	m.appendStreamContent(blockReasoning, strings.Repeat("r", 150)+"REASON_TAIL_MARK")

	view := ansi.Strip(m.View().Content)
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Errorf("line %d width = %d, want <= 80: %q", i, w, truncateForLog(line))
		}
	}
	// Markers may be split across wrapped lines; collapse whitespace to check
	// that their characters survived (i.e. lines folded instead of being cut).
	condensed := strings.Join(strings.Fields(view), "")
	for _, marker := range []string{"USER_TAIL_MARK", "URL_TAIL_MARK", "中文尾部标记", "ERR_TAIL_MARK"} {
		if !strings.Contains(condensed, marker) {
			t.Errorf("tail marker %q missing from view (content cut instead of wrapped)", marker)
		}
	}
}

func TestRenderedBlockWidthStaysWithinViewport(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	m.appendBlock(chatBlock{kind: blockUser, content: strings.Repeat("a", 200)})
	m.appendBlock(chatBlock{kind: blockAssistant, content: strings.Repeat("无空格中文长文本", 40)})
	m.appendBlock(chatBlock{kind: blockError, content: strings.Repeat("E", 150)})
	m.appendBlock(chatBlock{kind: blockReasoning, content: strings.Repeat("r", 200)})
	m.appendBlock(chatBlock{kind: blockDivider, content: strings.Repeat("divider-", 30)})

	for i := range m.messages {
		rendered := m.renderBlock(&m.messages[i])
		if w := maxLineWidth(rendered); w > 80 {
			t.Errorf("block[%d] kind=%d rendered width = %d, want <= 80", i, m.messages[i].kind, w)
		}
	}
}

func maxLineWidth(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		if lw := ansi.StringWidth(line); lw > w {
			w = lw
		}
	}
	return w
}

// TestAllBlockKindsFitConversationWidth is the safety net for keeping the
// viewport's SoftWrap off: every block kind must render within the terminal
// width on its own, because the viewport no longer folds wide lines.
func TestAllBlockKindsFitConversationWidth(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	gridCard := &cardState{id: "card-grid", kind: "table", title: strings.Repeat("grid title ", 12),
		document: map[string]any{
			"title": strings.Repeat("grid title ", 12),
			"component": map[string]any{
				"columns": []any{
					map[string]any{"key": "name", "label": strings.Repeat("NAME", 30)},
					map[string]any{"key": "value"},
				},
				"rows": []any{
					map[string]any{"name": strings.Repeat("n", 200), "value": strings.Repeat("v", 90)},
					map[string]any{"name": "plain", "value": 42},
				},
			},
		}}
	diffCard := &cardState{id: "card-diff", kind: "diff", title: "diff card",
		document: map[string]any{
			"title": "diff card",
			"component": map[string]any{
				"unified_diff": "@@ -1 +1 @@\n" + strings.Repeat("+added long line ", 30) + "\n" + strings.Repeat("-removed ", 40),
			},
		}}

	blocks := []chatBlock{
		{kind: blockToolCall, id: "tc1", toolName: "generic-tool",
			toolArgs:         `{"path":"` + strings.Repeat("p", 200) + `"}`,
			toolOutput:       strings.Repeat("x", 500),
			toolArgsComplete: true, success: true},
		{kind: blockToolCall, id: "tc2", toolName: "bash",
			toolArgs:         `{"command":"ls"}`,
			toolOutput:       strings.Repeat("err\n", 40),
			toolArgsComplete: true, success: false},
		{kind: blockToolCall, id: "tc3", toolName: "generic", pending: true,
			toolArgs: strings.Repeat(`{"k":"v",`, 30)},
		{kind: blockPlan, toolName: "plan title " + strings.Repeat("L", 60),
			content: strings.Repeat("# heading\n\n- item\n", 20)},
		{kind: blockDivider, content: strings.Repeat("d", 100)},
		{kind: blockCard, id: "card-grid", card: gridCard},
		{kind: blockCard, id: "card-diff", card: diffCard},
	}
	for i := range blocks {
		rendered := m.renderBlock(&blocks[i])
		for li, line := range strings.Split(rendered, "\n") {
			if w := lipgloss.Width(line); w > 80 {
				t.Errorf("blocks[%d] kind=%d line %d width=%d want <=80: %q", i, blocks[i].kind, li, w, truncateForLog(line))
			}
		}
	}
}

func truncateForLog(s string) string {
	s = ansi.Strip(s)
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
