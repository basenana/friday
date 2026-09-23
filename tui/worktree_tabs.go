package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

type worktreeTabStatus string

const (
	worktreeTabIdle        worktreeTabStatus = "idle"
	worktreeTabRunning     worktreeTabStatus = "running"
	worktreeTabWaiting     worktreeTabStatus = "waiting"
	worktreeTabCompleted   worktreeTabStatus = "completed"
	worktreeTabFailed      worktreeTabStatus = "failed"
	worktreeTabInterrupted worktreeTabStatus = "interrupted"
)

type worktreeTab struct {
	ID      string
	Label   string
	Status  worktreeTabStatus
	Path    string
	Session string
}

type worktreeTabHit struct {
	ID         string
	Start, End int
}

func nextWorktreeTabStatus(current worktreeTabStatus, event events.Event) worktreeTabStatus {
	switch event.Type {
	case events.KindRunStarted:
		return worktreeTabRunning
	case events.KindRunError:
		return worktreeTabFailed
	case events.KindRunFinished:
		var data events.RunFinishedData
		if events.DecodePayload(event, &data) == nil {
			if len(data.Interrupts) > 0 {
				return worktreeTabWaiting
			}
			switch data.StopReason {
			case "cancelled":
				return worktreeTabInterrupted
			case "error":
				return worktreeTabFailed
			}
		}
		return worktreeTabCompleted
	case events.KindCustom:
		if event.Name == events.CustomFormRequested {
			return worktreeTabWaiting
		}
		if event.Name == events.CustomFormSubmitted || event.Name == events.CustomFormCancelled {
			return worktreeTabRunning
		}
	}
	return current
}

func worktreeTabMarker(status worktreeTabStatus) string {
	switch status {
	case worktreeTabRunning:
		return "●"
	case worktreeTabWaiting:
		return "◆"
	case worktreeTabCompleted:
		return "✓"
	case worktreeTabFailed, worktreeTabInterrupted:
		return "!"
	default:
		return "○"
	}
}

func renderWorktreeTabs(tabs []worktreeTab, activeID string, width int) (string, []worktreeTabHit) {
	if len(tabs) == 0 || width <= 0 {
		return "", nil
	}
	active := 0
	for i := range tabs {
		if tabs[i].ID == activeID {
			active = i
			break
		}
	}
	labels := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	for i, tab := range tabs {
		label := strings.TrimSpace(tab.Label)
		if label == "" {
			label = tab.ID
		}
		labels[i] = " " + worktreeTabMarker(tab.Status) + " " + label + " "
		widths[i] = lipgloss.Width(labels[i]) + 2
	}
	overflow := totalTabWidth(widths) > width
	reserved := 0
	if overflow {
		reserved = 2 // space + ellipsis
	}
	if widths[active]+reserved > width {
		labels[active] = truncateWidth(labels[active], max(width-reserved-2, 1))
		widths[active] = lipgloss.Width(labels[active]) + 2
	}
	start, end := active, active+1
	used := widths[active]
	for {
		changed := false
		if start > 0 && used+widths[start-1]+reserved <= width {
			start--
			used += widths[start]
			changed = true
		}
		if end < len(labels) && used+widths[end]+reserved <= width {
			used += widths[end]
			end++
			changed = true
		}
		if !changed {
			break
		}
	}
	pieces := make([]string, 0, end-start+1)
	hits := make([]worktreeTabHit, 0, end-start)
	cell := 0
	for i := start; i < end; i++ {
		text := labels[i]
		selected := tabs[i].ID == activeID
		var content string
		if strings.HasSuffix(text, "…") {
			content = worktreeTabLabelStyle(selected).Render(text)
		} else {
			label := strings.TrimSpace(tabs[i].Label)
			if label == "" {
				label = tabs[i].ID
			}
			content = worktreeTabLabelStyle(selected).Render(" ") +
				worktreeTabStatusStyle(tabs[i].Status, selected).Render(worktreeTabMarker(tabs[i].Status)) +
				worktreeTabLabelStyle(selected).Render(" "+label+" ")
		}
		border := lipgloss.RoundedBorder()
		borderColor := themeBorder
		if selected {
			border = lipgloss.DoubleBorder()
			borderColor = themeAccent
		}
		piece := lipgloss.NewStyle().Border(border).BorderForeground(borderColor).Render(content)
		pieces = append(pieces, piece)
		length := lipgloss.Width(piece)
		hits = append(hits, worktreeTabHit{ID: tabs[i].ID, Start: cell, End: cell + length})
		cell += length
	}
	if start > 0 || end < len(tabs) {
		pieces = append(pieces, mutedStyle.Render("  \n …\n  "))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, pieces...), hits
}

func worktreeTabLabelStyle(selected bool) lipgloss.Style {
	if selected {
		return accentStyle.Copy().Bold(true)
	}
	return mutedStyle
}

func worktreeTabStatusStyle(status worktreeTabStatus, selected bool) lipgloss.Style {
	style := mutedStyle
	switch status {
	case worktreeTabRunning:
		style = accentStyle
	case worktreeTabWaiting:
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B"))
	case worktreeTabCompleted:
		style = lipgloss.NewStyle().Foreground(themeAdded)
	case worktreeTabFailed, worktreeTabInterrupted:
		style = errorStyle
	}
	if selected {
		style = style.Copy().Bold(true)
	}
	return style
}

func totalTabWidth(widths []int) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	return total
}

func worktreeTabAt(hits []worktreeTabHit, x int) string {
	for _, hit := range hits {
		if x >= hit.Start && x < hit.End {
			return hit.ID
		}
	}
	return ""
}

func (m *model) refreshWorktreeTabs() {
	if !m.worktreeMode || m.worktreeService == nil || m.worktreeSupervisor == nil {
		return
	}
	items, err := m.worktreeService.List(context.Background())
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "list worktree tabs: " + err.Error()})
		return
	}
	old := make(map[string]worktreeTabStatus, len(m.worktreeTabs))
	for _, tab := range m.worktreeTabs {
		old[tab.ID] = tab.Status
	}
	runtimes := m.worktreeSupervisor.runtimeSnapshot()
	tabs := make([]worktreeTab, 0, len(items))
	for _, item := range items {
		if !item.Registered || item.Stale {
			continue
		}
		status := old[item.ID]
		if status == "" {
			status = worktreeTabIdle
			if runtime := runtimes[item.ID]; runtime != nil && runtime.registry.SessionRunning(runtime.sessionID) {
				status = worktreeTabRunning
			}
		}
		label := item.Branch
		if label == "" {
			label = item.Name
		}
		tabs = append(tabs, worktreeTab{ID: item.ID, Label: label, Path: item.Path, Session: item.SessionID, Status: status})
	}
	m.worktreeTabs = tabs
}

func (m *model) resetWorktreeStatusFeeds() []tea.Cmd {
	if m.worktreeStatusFeeds == nil {
		m.worktreeStatusToken++
		m.worktreeStatusFeeds = make(map[string]*bus.Feed, len(m.worktreeTabs))
		m.worktreeStatusSession = make(map[string]string, len(m.worktreeTabs))
	}
	if m.worktreeSupervisor == nil {
		return nil
	}
	runtimes := m.worktreeSupervisor.runtimeSnapshot()
	wanted := make(map[string]*worktreeRuntime, len(m.worktreeTabs))
	for _, tab := range m.worktreeTabs {
		if runtime := runtimes[tab.ID]; runtime != nil {
			wanted[tab.ID] = runtime
		}
	}
	for id, feed := range m.worktreeStatusFeeds {
		runtime := wanted[id]
		if runtime == nil || m.worktreeStatusSession[id] != runtime.sessionID {
			feed.Close()
			delete(m.worktreeStatusFeeds, id)
			delete(m.worktreeStatusSession, id)
		}
	}
	added := make([]tea.Cmd, 0)
	for id, runtime := range wanted {
		if m.worktreeStatusFeeds[id] == nil {
			m.worktreeStatusFeeds[id] = subscribeWorktreeStatusFeed(runtime.bus, runtime.sessionID)
			m.worktreeStatusSession[id] = runtime.sessionID
			added = append(added, m.waitForWorktreeStatus(id))
		}
	}
	return added
}

func subscribeWorktreeStatusFeed(b *eventbus.Bus, sessionID string) *bus.Feed {
	return bus.NewFeed(b, eventbus.SerialConfig{Buffer: 64, Overflow: eventbus.OverflowDropOldest},
		bus.TopicRun(sessionID, "*"), bus.TopicForm(sessionID, "*"))
}

func (m *model) closeWorktreeStatusFeeds() {
	m.worktreeStatusToken++
	for _, feed := range m.worktreeStatusFeeds {
		feed.Close()
	}
	m.worktreeStatusFeeds = nil
	m.worktreeStatusSession = nil
}

func (m *model) waitForWorktreeStatus(id string) tea.Cmd {
	feed := m.worktreeStatusFeeds[id]
	if feed == nil {
		return nil
	}
	token := m.worktreeStatusToken
	sessionID := m.worktreeStatusSession[id]
	var registry interface {
		SessionWaitingForInput(string) bool
	}
	if m.worktreeSupervisor != nil {
		if runtime := m.worktreeSupervisor.runtimeSnapshot()[id]; runtime != nil && runtime.sessionID == sessionID {
			registry = runtime.registry
		}
	}
	return func() tea.Msg {
		select {
		case event := <-feed.Events():
			waiting := registry != nil && registry.SessionWaitingForInput(sessionID)
			return worktreeStatusMsg{token: token, worktreeID: id, sessionID: sessionID, event: event, waiting: waiting}
		case <-feed.Done():
			return worktreeStatusFeedClosedMsg{token: token, worktreeID: id, sessionID: sessionID}
		}
	}
}

func (m *model) advanceDispatchForeground() {
	m.dispatchToken++
	m.dispatching = false
	m.pendingDispatch = nil
}

func (m *model) waitForAllWorktreeStatuses() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.worktreeStatusFeeds))
	for id := range m.worktreeStatusFeeds {
		cmds = append(cmds, m.waitForWorktreeStatus(id))
	}
	return cmds
}

func (m *model) initialWaitCommands() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2+len(m.worktreeStatusFeeds))
	if cmd := m.waitForActorEvent(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.codebaseFeed != nil {
		cmds = append(cmds, waitForCodebaseActivity(m.codebaseFeed, m.codebaseToken))
	}
	cmds = append(cmds, m.waitForAllWorktreeStatuses()...)
	return cmds
}

func (m *model) renderWorktreeTabBar() string {
	active := ""
	if m.worktreeRuntime != nil {
		active = m.worktreeRuntime.id
	}
	rendered, hits := renderWorktreeTabs(m.worktreeTabs, active, m.width)
	m.worktreeTabHits = hits
	return rendered
}

func (m *model) switchWorktreeTab(delta int) tea.Cmd {
	if len(m.worktreeTabs) < 2 || m.worktreeRuntime == nil {
		return nil
	}
	current := 0
	for i := range m.worktreeTabs {
		if m.worktreeTabs[i].ID == m.worktreeRuntime.id {
			current = i
			break
		}
	}
	target := (current + delta + len(m.worktreeTabs)) % len(m.worktreeTabs)
	return m.selectWorktree(m.worktreeTabs[target].ID)
}
