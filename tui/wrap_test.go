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

func truncateForLog(s string) string {
	s = ansi.Strip(s)
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
