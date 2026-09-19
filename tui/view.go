package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
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
	blockPlan
	blockDivider
)

type chatBlock struct {
	id               string
	kind             blockKind
	content          string
	rendered         string
	toolName         string
	toolArgs         string
	toolOutput       string
	toolArgsComplete bool
	success          bool
	pending          bool
	interrupted      bool
	timedOut         bool
	timeoutKind      string
	card             *cardState

	// wip buffers streamed deltas so appending does not re-copy the whole
	// accumulated message on every delta. fullContent materializes it lazily.
	// Never copy a chatBlock by value after wip has been written to.
	wip strings.Builder
}

// fullContent returns the complete content, materializing any buffered
// streamed deltas exactly once.
func (b *chatBlock) fullContent() string {
	if b.wip.Len() == 0 {
		return b.content
	}
	b.content += b.wip.String()
	b.wip.Reset()
	return b.content
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
	inputBoxStyle = lipgloss.NewStyle().UnsetBackground().Border(lipgloss.RoundedBorder()).BorderForeground(themeBorder).Padding(0, 1)
	menuStyle     = lipgloss.NewStyle().Foreground(themeText).Border(lipgloss.RoundedBorder()).BorderForeground(themeBorder).Padding(0, 1)
	statusStyle   = lipgloss.NewStyle().Foreground(themeMuted)
)

func interactiveStyle(selected bool) lipgloss.Style {
	if selected {
		return accentStyle.Copy().Bold(true)
	}
	return mutedStyle
}

func primaryActionStyle() lipgloss.Style {
	return accentStyle.Copy().Bold(true)
}

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
	m.markTranscriptDirty(true)
}

// streamRebuildInterval throttles transcript rebuilds while a run is
// streaming deltas: full glamour re-renders are the most expensive part of
// a frame, and 10 fps keeps streaming output responsive without burning CPU.
const streamRebuildInterval = 100 * time.Millisecond

// markTranscriptDirty flags the transcript for rebuild on the next View.
// force bypasses the streaming rebuild throttle.
func (m *model) markTranscriptDirty(force bool) {
	m.transcriptDirty = true
	if force {
		m.forceTranscript = true
	}
}

// streamActive reports whether a run is currently streaming deltas into the
// transcript, which gates the rebuild throttle.
func (m *model) streamActive() bool {
	return m.textBlock >= 0 || m.reasonBlock >= 0 || len(m.toolCalls) > 0
}

// rebuildDue reports whether the next View must rebuild the transcript.
func (m *model) rebuildDue() bool {
	if !m.transcriptDirty {
		return false
	}
	if m.forceTranscript || !m.streamActive() {
		return true
	}
	return m.nowTime().Sub(m.lastTranscriptBuild) >= streamRebuildInterval
}

// rebuildTranscript re-renders every block, joins them, and pushes the
// result into the viewport. This is the only path that touches viewport
// content, so presentation-only frames never pay for it.
func (m *model) rebuildTranscript(atBottom bool) {
	var blocks []string
	for i := range m.messages {
		if rendered := m.renderBlock(&m.messages[i]); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	m.viewport.SetContent(joinConversationBlocks(blocks))
	if atBottom {
		m.viewport.GotoBottom()
	}
	m.transcriptDirty = false
	m.forceTranscript = false
	m.lastTranscriptBuild = m.nowTime()
	m.transcriptRebuilds++
}

// renderActivityLine is the fixed status line rendered below the transcript
// while a run or compaction is in flight. It lives outside the viewport so
// the transcript itself stays clean for dirty-flag caching.
func (m *model) renderActivityLine() string {
	if m.running {
		label := m.runActivity
		if label == "" {
			label = "working"
		}
		if m.form != nil {
			label = "waiting for input"
		}
		if m.cancelling {
			label = "cancelling"
		}
		return accentStyle.Render(m.spinner.View() + " " + label + "… · " + formatElapsed(m.currentElapsed()))
	}
	if m.planCompacting {
		return accentStyle.Render(m.spinner.View() + " compacting context for approved plan…")
	}
	if m.manualCompacting {
		return accentStyle.Render(m.spinner.View() + " compacting context…")
	}
	return ""
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
	// Blocks are soft-wrapped to the conversation width so long lines fold
	// instead of being cut off at the viewport edge. lipgloss.Wrap re-applies
	// ANSI styles across inserted line breaks, so it is safe on already
	// rendered (colored) content such as markdown output.
	wrapWidth := m.conversationWidth()
	switch b.kind {
	case blockUser:
		b.rendered = lipgloss.Wrap(userStyle.Render("› ")+strings.TrimRight(terminalSafe(b.fullContent()), "\n"), wrapWidth, "")
	case blockAssistant:
		b.rendered = lipgloss.Wrap(m.markdown(b.fullContent())+suffix, wrapWidth, "")
	case blockReasoning:
		content := lipgloss.Wrap(truncateLines(terminalSafe(b.fullContent()), 12), max(wrapWidth-2, 18), "")
		b.rendered = mutedStyle.Render("thinking") + "\n" + reasoningStyle.Render(content) + suffix
	case blockToolCall:
		b.rendered = m.renderToolCard(b)
	case blockError:
		b.rendered = lipgloss.Wrap(errorStyle.Render("✗ "+terminalSafe(b.content)), wrapWidth, "")
	case blockCard:
		b.rendered = m.renderCard(b.card)
	case blockPlan:
		header := accentStyle.Copy().Bold(true).Render(b.toolName)
		b.rendered = menuStyle.Width(max(m.width-4, 20)).Render(header + "\n" + m.markdown(b.content))
	case blockDivider:
		label := truncateWidth(terminalSafe(b.content), max(m.width-8, 4))
		lineWidth := max(m.width-lipgloss.Width(label)-5, 3)
		b.rendered = mutedStyle.Render("── " + label + " " + strings.Repeat("─", lineWidth))
	}
	return b.rendered
}

// conversationWidth is the column budget transcript blocks must fit into.
func (m *model) conversationWidth() int {
	return max(m.width, 20)
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

func (m *model) View() tea.View {
	if m.quitting {
		return m.newView("")
	}
	if m.loading {
		return m.newView(lipgloss.NewStyle().Padding(1, 2).Render(accentStyle.Render(m.spinner.View() + " Loading session…")))
	}
	wasAtBottom := m.viewport.AtBottom()
	m.layout()
	if m.rebuildDue() {
		m.rebuildTranscript(wasAtBottom)
	}

	parts := []string{m.viewport.View()}
	if activity := m.renderActivityLine(); activity != "" {
		parts = append(parts, activity)
	}
	if len(m.queued) > 0 {
		parts = append(parts, m.renderQueue())
	}
	if todo := m.renderTodoPanel(m.width); todo != "" {
		parts = append(parts, todo)
	}
	if m.form != nil {
		parts = append(parts, m.form.View(m.width))
	} else if m.planHandoff != nil {
		parts = append(parts, m.renderPlanHandoff())
	} else if m.commandConfirm != nil {
		parts = append(parts, m.commandConfirm.View(m.width))
	} else if m.selector != nil {
		parts = append(parts, m.selector.View(m.width))
	} else if m.detail != nil {
		parts = append(parts, m.detail.View(m.width))
	} else if m.confirm != nil {
		parts = append(parts, m.confirm.View(m.width))
	} else {
		if m.menu.mode != menuNone {
			parts = append(parts, m.renderMenu())
		}
		if attachments := m.renderAttachments(); attachments != "" {
			parts = append(parts, attachments)
		}
		parts = append(parts, inputBoxStyle.Width(max(m.width-2, 10)).Render(m.textarea.View()))
	}
	parts = append(parts, m.renderStatus())
	return m.newView(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m *model) renderAttachments() string {
	if len(m.attachments) == 0 {
		return ""
	}
	labels := make([]string, 0, len(m.attachments))
	for i, image := range m.attachments {
		name := image.Filename
		if name == "" {
			name = fmt.Sprintf("image-%d", i+1)
		}
		labels = append(labels, "[image: "+name+" ×]")
	}
	return strings.Join(labels, " ")
}

func (m *model) newView(content string) tea.View {
	view := tea.NewView(content)
	view.AltScreen = m.alternateScreen
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m *model) layout() {
	wasAtBottom := m.viewport.AtBottom()
	width := max(m.width, 20)
	m.textarea.SetWidth(max(width-6, 10))
	composerLines := max(m.textarea.Height(), 1)
	statusLines := max(lipgloss.Height(m.renderStatus()), 1)
	extra := composerLines + 2 + statusLines // input border + wrapped status
	if attachments := m.renderAttachments(); attachments != "" {
		extra += lipgloss.Height(attachments)
	}
	if len(m.queued) > 0 {
		extra += min(len(m.queued), 3) + 3
	}
	if todo := m.renderTodoPanel(width); todo != "" {
		extra += lipgloss.Height(todo)
	}
	if m.menu.mode != menuNone {
		extra += min(len(m.menu.items), 8) + 2
	}
	if m.form != nil {
		extra += m.form.Height(width)
		extra -= composerLines + 2
	}
	if m.planHandoff != nil {
		extra += m.planHandoffHeight()
		extra -= composerLines + 2
	}
	if m.commandConfirm != nil || m.selector != nil {
		extra += min(m.height/2, 12)
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
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *model) renderQueue() string {
	start := max(len(m.queued)-3, 0)
	lines := []string{mutedStyle.Render(fmt.Sprintf("up next (%d) · sent after the current task", len(m.queued)))}
	for i := start; i < len(m.queued); i++ {
		item := m.queued[i]
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, terminalSafe(firstLine(userInputDisplay(item.text, item.images)))))
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
		line = interactiveStyle(i == m.menu.selected).Render(line)
		lines = append(lines, line)
	}
	return menuStyle.Width(max(m.width-4, 10)).Render(strings.Join(lines, "\n"))
}

func (m *model) renderStatus() string {
	modelName := formatRuntimeModel(m.clientRuntimeInfo())
	mode := string(m.mode)
	if m.loopActive {
		mode = "loop"
	}
	parts := []string{modelName, mode, "session:" + shortID(m.sessionID)}
	if m.latestPlan != nil && m.latestPlan.Status == planning.ArtifactProposed {
		parts = append(parts, "plan ready")
	}
	if m.workdir != "" {
		parts = append(parts, filepath.Base(m.workdir))
	}
	if m.tokenCount > 0 {
		if window := m.activeModel.ContextWindow; window > 0 {
			parts = append(parts, fmt.Sprintf("%s/%s", fmtTokens(m.tokenCount), fmtTokens(int(window))))
		} else {
			parts = append(parts, fmtTokens(m.tokenCount)+" tokens")
		}
	}
	if m.running {
		parts = append(parts, "● running", "Enter/Tab send next", "Esc cancel")
	} else if m.planCompacting {
		parts = append(parts, "● compacting plan context")
	} else if m.manualCompacting {
		parts = append(parts, "● compacting context")
	}
	if !m.viewport.AtBottom() {
		parts = append(parts, "↑ history")
	}
	return statusStyle.Width(max(m.width-1, 10)).Render(terminalSafe(strings.Join(parts, " · ")))
}

func formatRuntimeModel(info providers.ClientRuntimeInfo) string {
	modelName := info.Model
	if modelName == "" {
		modelName = "model?"
	}
	if info.Effort != "" && info.Effort != providers.ReasoningEffortDefault {
		modelName += " [" + info.Effort + "]"
	}
	return modelName
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
