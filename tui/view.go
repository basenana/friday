package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
)

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockReasoning
	blockToolCall
	blockError
	blockCard
	blockDivider
)

type toolCallBlock struct {
	id, name, input, output string
	success                 bool
}

type chatBlock struct {
	id          string
	kind        blockKind
	content     string
	rendered    string
	toolName    string
	success     bool
	interrupted bool
	card        *cardState
}

var (
	themeAccent  = lipgloss.Color("#C4A7FF")
	themeMuted   = lipgloss.Color("#B4BCCB")
	themeText    = lipgloss.Color("#EDF0F5")
	themeUser    = lipgloss.Color("#75BEFF")
	themeError   = lipgloss.Color("#FF8A80")
	themeBorder  = lipgloss.Color("#788293")
	themeAdded   = lipgloss.Color("#7EE787")
	themeRemoved = lipgloss.Color("#FF7B72")

	accentStyle = lipgloss.NewStyle().Foreground(themeAccent)
	mutedStyle  = lipgloss.NewStyle().Foreground(themeMuted)
	userStyle   = lipgloss.NewStyle().Foreground(themeUser).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(themeError)

	reasoningStyle = lipgloss.NewStyle().Foreground(themeText).Italic(true).
			BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(themeBorder).PaddingLeft(1)
	toolBoxStyle = lipgloss.NewStyle().Foreground(themeText).
			BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(themeBorder).PaddingLeft(1)
	toolFailStyle = toolBoxStyle.Copy().BorderForeground(themeError)
	inputBoxStyle = lipgloss.NewStyle().UnsetBackground().Border(lipgloss.RoundedBorder()).BorderForeground(themeBorder).Padding(0, 1)
	menuStyle     = lipgloss.NewStyle().Foreground(themeText).Border(lipgloss.RoundedBorder()).BorderForeground(themeBorder).Padding(0, 1)
	statusStyle   = lipgloss.NewStyle().Foreground(themeMuted)
)

func configureTheme(dark bool) {
	lightDark := lipgloss.LightDark(dark)
	themeAccent = lightDark(lipgloss.Color("#5F3DC4"), lipgloss.Color("#C4A7FF"))
	themeMuted = lightDark(lipgloss.Color("#5C6370"), lipgloss.Color("#B4BCCB"))
	themeText = lightDark(lipgloss.Color("#20242B"), lipgloss.Color("#EDF0F5"))
	themeUser = lightDark(lipgloss.Color("#005A9C"), lipgloss.Color("#75BEFF"))
	themeError = lightDark(lipgloss.Color("#B42318"), lipgloss.Color("#FF8A80"))
	themeBorder = lightDark(lipgloss.Color("#8B95A5"), lipgloss.Color("#788293"))
	themeAdded = lightDark(lipgloss.Color("#18794E"), lipgloss.Color("#7EE787"))
	themeRemoved = lightDark(lipgloss.Color("#CF222E"), lipgloss.Color("#FF7B72"))

	accentStyle = lipgloss.NewStyle().Foreground(themeAccent)
	mutedStyle = lipgloss.NewStyle().Foreground(themeMuted)
	userStyle = lipgloss.NewStyle().Foreground(themeUser).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(themeError)
	reasoningStyle = lipgloss.NewStyle().Foreground(themeText).Italic(true).
		BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(themeBorder).PaddingLeft(1)
	toolBoxStyle = lipgloss.NewStyle().Foreground(themeText).
		BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(themeBorder).PaddingLeft(1)
	toolFailStyle = toolBoxStyle.Copy().BorderForeground(themeError)
	inputBoxStyle = lipgloss.NewStyle().UnsetBackground().Border(lipgloss.RoundedBorder()).BorderForeground(themeBorder).Padding(0, 1)
	menuStyle = lipgloss.NewStyle().Foreground(themeText).Border(lipgloss.RoundedBorder()).BorderForeground(themeBorder).Padding(0, 1)
	statusStyle = lipgloss.NewStyle().Foreground(themeMuted)
}

func (m *model) applyTheme(dark bool) {
	if m.darkBackground == dark {
		return
	}
	m.darkBackground = dark
	configureTheme(dark)
	m.spinner.Style = accentStyle
	removeTextareaBackground(&m.textarea)
	m.markdownRenderer = nil
	m.markdownWidth = 0
	m.invalidateRendered()
}

func (m *model) invalidateRendered() {
	for i := range m.messages {
		m.messages[i].rendered = ""
	}
}

func (m *model) markdown(content string) string {
	content = terminalSafe(content)
	width := max(m.width-4, 20)
	if m.markdownRenderer == nil || m.markdownWidth != width {
		markdownStyle := "light"
		if m.darkBackground {
			markdownStyle = "dark"
		}
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(markdownStyle), glamour.WithWordWrap(width))
		if err != nil {
			return content
		}
		m.markdownRenderer = r
		m.markdownWidth = width
	}
	out, err := m.markdownRenderer.Render(content)
	if err != nil {
		return content
	}
	return trimVerticalSpace(out)
}

func (m *model) renderBlock(b *chatBlock) string {
	if b.rendered != "" {
		return b.rendered
	}
	suffix := ""
	if b.interrupted {
		suffix = "\n" + mutedStyle.Render("↳ interrupted")
	}
	switch b.kind {
	case blockUser:
		b.rendered = userStyle.Render("› ") + strings.TrimRight(terminalSafe(b.content), "\n")
	case blockAssistant:
		b.rendered = m.markdown(b.content) + suffix
	case blockReasoning:
		b.rendered = mutedStyle.Render("thinking") + "\n" + reasoningStyle.Render(truncateLines(terminalSafe(b.content), 12)) + suffix
	case blockToolCall:
		style, icon := toolBoxStyle, "✓"
		if !b.success {
			style, icon = toolFailStyle, "✗"
		}
		body := truncateLines(terminalSafe(b.content), 10)
		if len(strings.Split(strings.TrimRight(b.content, "\n"), "\n")) > 10 && b.id != "" {
			body += "\n" + mutedStyle.Render("/show "+shortID(b.id)+" · full output")
		}
		b.rendered = style.Render(fmt.Sprintf("%s %s\n%s", icon, terminalSafe(b.toolName), body)) + suffix
	case blockError:
		b.rendered = errorStyle.Render("✗ " + terminalSafe(b.content))
	case blockCard:
		b.rendered = m.renderCard(b.card)
	case blockDivider:
		lineWidth := max(m.width-lipgloss.Width(b.content)-5, 3)
		b.rendered = mutedStyle.Render("── " + terminalSafe(b.content) + " " + strings.Repeat("─", lineWidth))
	}
	return b.rendered
}

func terminalSafe(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (!unicode.IsControl(r) && r != '\u007f') {
			return r
		}
		return -1
	}, s)
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= maxLines {
		return strings.TrimRight(s, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n… (%d more lines)", len(lines)-maxLines)
}

func (m *model) renderStreaming() string {
	var parts []string
	if m.reasonBuf.Len() > 0 {
		parts = append(parts, mutedStyle.Render("thinking…")+"\n"+reasoningStyle.Render(truncateLines(terminalSafe(m.reasonBuf.String()), 12)))
	}
	if m.textBuf.Len() > 0 {
		parts = append(parts, m.markdown(m.textBuf.String()))
	}
	for _, id := range m.toolOrder {
		if tc := m.toolCalls[id]; tc != nil {
			parts = append(parts, toolBoxStyle.Render("… "+terminalSafe(tc.name)+"\n"+truncateLines(terminalSafe(tc.input), 5)))
		}
	}
	return strings.Join(parts, "\n\n")
}

func (m *model) View() tea.View {
	if m.quitting {
		return m.newView("")
	}
	if m.loading {
		return m.newView(lipgloss.NewStyle().Padding(1, 2).Render(accentStyle.Render(m.spinner.View() + " Loading session…")))
	}
	m.layout()
	var blocks []string
	for i := range m.messages {
		if rendered := m.renderBlock(&m.messages[i]); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	if m.running {
		if preview := m.renderStreaming(); preview != "" {
			blocks = append(blocks, preview)
		}
		label := "working"
		if m.steeringPending {
			label = "steering"
		} else if m.cancelling {
			label = "cancelling"
		}
		blocks = append(blocks, accentStyle.Render(m.spinner.View()+" "+label+"…"))
	}
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(joinConversationBlocks(blocks))
	if wasAtBottom {
		m.viewport.GotoBottom()
	}

	parts := []string{m.viewport.View()}
	if len(m.queued) > 0 {
		parts = append(parts, m.renderQueue())
	}
	if m.form != nil {
		parts = append(parts, m.form.View(m.width))
	} else if m.detail != nil {
		parts = append(parts, m.detail.View(m.width))
	} else if m.confirm != nil {
		parts = append(parts, m.confirm.View(m.width))
	} else {
		if m.menu.mode != menuNone {
			parts = append(parts, m.renderMenu())
		}
		parts = append(parts, inputBoxStyle.Width(max(m.width-2, 10)).Render(m.textarea.View()))
	}
	parts = append(parts, m.renderStatus())
	return m.newView(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m *model) newView(content string) tea.View {
	view := tea.NewView(content)
	view.AltScreen = m.alternateScreen
	return view
}

func (m *model) layout() {
	width := max(m.width, 20)
	composerLines := min(max(m.textarea.LineCount(), 1), 8)
	m.textarea.SetHeight(composerLines)
	m.textarea.SetWidth(max(width-6, 10))
	extra := composerLines + 3 // input border + status
	if len(m.queued) > 0 {
		extra += min(len(m.queued), 3) + 2
	}
	if m.menu.mode != menuNone {
		extra += min(len(m.menu.items), 8) + 2
	}
	if m.form != nil {
		extra += m.form.Height(width)
		extra -= composerLines + 2
	}
	if m.detail != nil {
		m.detail.view.SetHeight(max(min(m.height/2, 14), 4))
		extra += min(m.height/2, 16)
		extra -= composerLines + 2
	}
	if m.confirm != nil {
		extra += 4
		extra -= composerLines + 2
	}
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(max(m.height-extra, 3))
}

func (m *model) renderQueue() string {
	start := max(len(m.queued)-3, 0)
	lines := []string{mutedStyle.Render(fmt.Sprintf("queued (%d) · Tab while running", len(m.queued)))}
	for i := start; i < len(m.queued); i++ {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, terminalSafe(firstLine(m.queued[i].text))))
	}
	return menuStyle.Width(max(m.width-4, 10)).Render(strings.Join(lines, "\n"))
}

func (m *model) renderMenu() string {
	if len(m.menu.items) == 0 {
		return menuStyle.Width(max(m.width-4, 10)).Render(mutedStyle.Render("no matches"))
	}
	start := 0
	if m.menu.selected >= 8 {
		start = m.menu.selected - 7
	}
	end := min(start+8, len(m.menu.items))
	var lines []string
	for i := start; i < end; i++ {
		item := m.menu.items[i]
		prefix := "  "
		if i == m.menu.selected {
			prefix = "› "
		}
		line := prefix + item.label
		if item.description != "" {
			line += "  " + item.description
		}
		line = truncateWidth(line, max(m.width-6, 10))
		if i == m.menu.selected {
			line = accentStyle.Copy().Bold(true).Render(line)
		} else {
			line = mutedStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return menuStyle.Width(max(m.width-4, 10)).Render(strings.Join(lines, "\n"))
}

func (m *model) renderStatus() string {
	modelName := m.cfg.PrimaryModel().Model
	if modelName == "" {
		modelName = "model?"
	}
	parts := []string{"friday", modelName, "session:" + shortID(m.sessionID)}
	if m.workdir != "" {
		parts = append(parts, filepath.Base(m.workdir))
	}
	if m.tokenCount > 0 {
		if window := m.cfg.PrimaryModel().ContextWindow; window > 0 {
			parts = append(parts, fmt.Sprintf("%s/%s", fmtTokens(m.tokenCount), fmtTokens(int(window))))
		} else {
			parts = append(parts, fmtTokens(m.tokenCount)+" tokens")
		}
	}
	if m.running {
		parts = append(parts, "● running")
	}
	if !m.viewport.AtBottom() {
		parts = append(parts, "↑ history")
	}
	return statusStyle.Width(max(m.width-1, 10)).Render(terminalSafe(strings.Join(parts, " · ")))
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func fmtTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n%1000 < 100 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func truncateWidth(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)+"…") > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func joinConversationBlocks(blocks []string) string {
	compact := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block = trimVerticalSpace(block); block != "" {
			compact = append(compact, block)
		}
	}
	return strings.Join(compact, "\n\n")
}

func trimVerticalSpace(s string) string {
	lines := strings.Split(s, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}
