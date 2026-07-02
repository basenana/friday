package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockReasoning
	blockToolCall
	blockError
)

type toolCallBlock struct {
	id, name, input, output string
	success                 bool
}

// chatBlock is a finalized, immutable conversation element rendered in the
// viewport. `rendered` caches the styled string (invalidated on resize).
type chatBlock struct {
	kind     blockKind
	content  string
	rendered string
	toolName string
	success  bool
}

var (
	// glamourRenderer is the lazily-built markdown renderer; glamourWidth
	// tracks its wrap width so we rebuild only on terminal resize.
	glamourRenderer *glamour.TermRenderer
	glamourWidth    int

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	reasoningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	toolBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")).
			Padding(0, 1)

	toolFailStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("203")).
			Padding(0, 1)

	toolHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("213"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Italic(true)

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)
)

// initGlamour lazily creates the markdown renderer with the current width.
func initGlamour(width int) {
	w := max(width-4, 20)
	if glamourRenderer != nil && glamourWidth == w {
		return
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(w),
	)
	if err != nil {
		return
	}
	glamourRenderer = r
	glamourWidth = w
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// invalidateRendered drops cached styled output, forcing re-render on next View.
func (m *model) invalidateRendered() {
	for i := range m.messages {
		m.messages[i].rendered = ""
	}
}

// renderBlock renders a finalized chatBlock to a styled string.
func (m *model) renderBlock(b *chatBlock) string {
	if b.rendered != "" {
		return b.rendered
	}
	switch b.kind {
	case blockUser:
		b.rendered = userStyle.Render("> ") + strings.TrimRight(b.content, "\n")
	case blockAssistant:
		initGlamour(m.width)
		if glamourRenderer == nil {
			b.rendered = b.content
			break
		}
		out, err := glamourRenderer.Render(b.content)
		if err != nil {
			b.rendered = b.content
		} else {
			b.rendered = strings.TrimRight(out, "\n")
		}
	case blockReasoning:
		header := lipgloss.NewStyle().Italic(true).Faint(true).Render("thinking")
		body := reasoningStyle.Render(strings.TrimRight(b.content, "\n"))
		b.rendered = header + "\n" + body
	case blockToolCall:
		style := toolBoxStyle
		if !b.success {
			style = toolFailStyle
		}
		header := toolHeaderStyle.Render("✦ " + b.toolName)
		body := truncateLines(b.content, 10)
		b.rendered = style.Render(header + "\n" + body)
	case blockError:
		b.rendered = errorStyle.Render("✗ " + b.content)
	}
	return b.rendered
}

// truncateLines caps the number of lines shown, appending an indicator.
func truncateLines(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + "\n… (" + itoa(len(lines)-maxLines) + " more lines)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// renderStreaming produces the in-progress preview appended below finalized blocks.
func (m *model) renderStreaming() string {
	var parts []string
	if m.reasonBuf.Len() > 0 {
		header := lipgloss.NewStyle().Italic(true).Faint(true).Render("thinking…")
		body := reasoningStyle.Render(strings.TrimRight(m.reasonBuf.String(), "\n"))
		parts = append(parts, header+"\n"+body)
	}
	if m.textBuf.Len() > 0 {
		initGlamour(m.width)
		if glamourRenderer == nil {
			parts = append(parts, m.textBuf.String())
		} else if out, err := glamourRenderer.Render(m.textBuf.String()); err == nil {
			parts = append(parts, strings.TrimRight(out, "\n"))
		} else {
			parts = append(parts, m.textBuf.String())
		}
	}
	for _, tc := range m.toolCalls {
		header := toolHeaderStyle.Render("✦ " + tc.name + " …")
		body := truncateLines(tc.input, 5)
		parts = append(parts, toolBoxStyle.Render(header+"\n"+body))
	}
	return strings.Join(parts, "\n\n")
}

// View renders the full screen.
func (m *model) View() string {
	if m.quitting {
		return ""
	}

	var blocks []string
	for i := range m.messages {
		blocks = append(blocks, m.renderBlock(&m.messages[i]))
	}
	if m.running {
		if preview := m.renderStreaming(); preview != "" {
			blocks = append(blocks, preview)
		}
		blocks = append(blocks, m.spinner.View()+" working…")
	}

	content := strings.Join(blocks, "\n\n")
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}

	status := statusBarStyle.Render(m.renderStatus())
	input := inputBoxStyle.Render(m.textarea.View())

	return lipgloss.JoinVertical(lipgloss.Left,
		status,
		m.viewport.View(),
		input,
	)
}

func (m *model) renderStatus() string {
	parts := []string{
		"friday",
		"session:" + shortID(m.sessionID),
	}
	if m.tokenCount > 0 {
		parts = append(parts, fmtTokens(m.tokenCount)+" tokens")
	}
	if m.iteration > 0 {
		parts = append(parts, "loop:"+itoa(m.iteration))
	}
	if m.running {
		parts = append(parts, "● running")
	}
	return strings.Join(parts, " · ")
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func fmtTokens(n int) string {
	if n < 1000 {
		return itoa(n)
	}
	whole := n / 1000
	frac := (n % 1000) / 100
	if frac == 0 {
		return itoa(whole) + "k"
	}
	return itoa(whole) + "." + itoa(frac) + "k"
}

func shouldRouteToViewport(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		return true
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyPgUp, tea.KeyPgDown:
			return true
		}
		switch msg.String() {
		case "ctrl+u", "ctrl+d":
			return true
		}
	}
	return false
}
