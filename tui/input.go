package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/bus"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/core/types"
)

func (m *model) handleSlash(text string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return m, nil
	}
	name := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if name == "open" {
		return m.handleOpenCommand(parts[1:])
	}
	if name == "show" {
		return m.handleShowCommand(parts[1:])
	}
	cmd, found := m.cmdRegistry.Lookup(name)
	if !found {
		m.appendBlock(chatBlock{kind: blockError, content: "unknown command: " + parts[0] + " (try /help)"})
		return m.dispatchIfIdle()
	}
	result, err := cmd.Execute(&codercmds.Context{
		Ctx: context.Background(), SessionID: m.sessionID, Args: parts[1:],
		SessMgr: m.sessMgr, ActorReg: m.registry, Config: m.cfg,
	})
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m.dispatchIfIdle()
	}
	return m.applyResult(result)
}

func (m *model) applyResult(r *codercmds.Result) (tea.Model, tea.Cmd) {
	if r == nil {
		return m.dispatchIfIdle()
	}
	var cmds []tea.Cmd
	if r.SwitchSession != "" && r.SwitchSession != m.sessionID {
		if cmd, err := m.switchSession(r.SwitchSession, r.PreserveTranscript); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		} else if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if r.ClearMessages && (r.SwitchSession == "" || r.SwitchSession == m.sessionID) {
		m.messages = nil
		m.cards = make(map[string]*cardState)
		m.seenInputs = make(map[string]bool)
		m.invalidateRendered()
	}
	if r.Quit {
		m.quitting = true
		m.closeFeed()
		m.registry.Shutdown(m.sessionID)
		cmds = append(cmds, tea.Quit)
	}
	if r.RunAgent != "" {
		if cmd := m.runAgentCmd(r.RunAgent, r.AgentInput); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if r.Message != "" {
		m.appendBlock(chatBlock{kind: blockAssistant, content: r.Message})
	}
	if !m.running && len(m.queued) > 0 {
		cmds = append(cmds, func() tea.Msg { return dispatchQueuedMsg{} })
	}
	return m, tea.Batch(cmds...)
}

func (m *model) dispatchIfIdle() (tea.Model, tea.Cmd) {
	if !m.running && len(m.queued) > 0 {
		return m, func() tea.Msg { return dispatchQueuedMsg{} }
	}
	return m, nil
}

func (m *model) switchSession(newID string, preserveTranscript bool) (tea.Cmd, error) {
	_, created, err := m.sessMgr.GetOrCreateDetachedByID(newID)
	if err != nil {
		return nil, fmt.Errorf("prepare session %s: %w", shortID(newID), err)
	}
	rollbackCreated := func() {
		if created {
			_ = m.sessMgr.GetStore().Delete(newID)
		}
	}

	newFeed := bus.SubscribeAgentFeed(m.registry.Bus(), newID)
	if _, err := m.registry.GetOrCreate(newID); err != nil {
		newFeed.Close()
		rollbackCreated()
		return nil, fmt.Errorf("prepare actor %s: %w", shortID(newID), err)
	}

	var projection transcriptProjection
	if !preserveTranscript {
		projection, err = m.projectTranscript(newID)
		if err != nil {
			newFeed.Close()
			m.registry.Shutdown(newID)
			rollbackCreated()
			return nil, fmt.Errorf("restore session %s: %w", shortID(newID), err)
		}
	}
	if err := m.sessMgr.SetCurrentID(newID); err != nil {
		newFeed.Close()
		m.registry.Shutdown(newID)
		rollbackCreated()
		return nil, fmt.Errorf("activate session %s: %w", shortID(newID), err)
	}

	oldID, oldFeed := m.sessionID, m.feed
	m.sessionID, m.feed = newID, newFeed
	m.subscriptionToken++
	m.tokenCount, m.iteration = 0, 0
	m.running, m.cancelling, m.steeringPending = false, false, false
	m.queued = nil
	m.resetStreaming()
	if preserveTranscript {
		m.appendBlock(chatBlock{kind: blockDivider, content: "new session · " + shortID(newID)})
	} else {
		m.applyProjection(projection)
	}
	if oldFeed != nil {
		oldFeed.Close()
	}
	m.registry.Shutdown(oldID)
	return m.waitForActorEvent(), nil
}

func (m *model) runAgentCmd(agentName, input string) tea.Cmd {
	if m.running {
		m.appendBlock(chatBlock{kind: blockError, content: "a task is already running; queue it with Tab"})
		return nil
	}
	wrapped := fmt.Sprintf("[/%s] %s\n\nDelegate this to the %q subagent via the run_task tool. Return the subagent's final report verbatim as your answer.", agentName, input, agentName)
	turnID := types.NewID()
	m.appendBlock(chatBlock{kind: blockUser, id: turnID, content: fmt.Sprintf("[/%s] %s", agentName, input)})
	m.seenInputs[turnID] = true
	if err := m.sendUserText(wrapped, bus.DeliveryNormal, turnID); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return nil
	}
	m.running = true
	m.resetStreaming()
	return m.spinner.Tick
}

func (m *model) afterComposerEdit() {
	m.historyIndex = -1
	if m.menu.mode == menuHistory {
		m.openHistoryMenu()
	} else {
		m.refreshMenu()
	}
	m.layout()
}

func (m *model) refreshMenu() {
	value := strings.TrimLeft(m.textarea.Value(), " \t")
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\n") {
		if m.menu.mode == menuCommands {
			m.menu = menuState{}
		}
		return
	}
	query := strings.ToLower(strings.TrimPrefix(value, "/"))
	var items []menuItem
	if query == "" || strings.Contains("/open open a rich-card artifact", query) {
		items = append(items, menuItem{value: "/open ", label: "/open", description: "Open a rich-card artifact"})
	}
	if query == "" || strings.Contains("/show inspect complete tool output", query) {
		items = append(items, menuItem{value: "/show ", label: "/show", description: "Inspect complete tool output"})
	}
	for _, cmd := range m.cmdRegistry.List() {
		label := "/" + cmd.Name()
		if query == "" || strings.Contains(strings.ToLower(label+" "+cmd.Description()), query) {
			items = append(items, menuItem{value: label, label: label, description: cmd.Description()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].label < items[j].label })
	selected := m.menu.selected
	if selected >= len(items) {
		selected = max(len(items)-1, 0)
	}
	m.menu = menuState{mode: menuCommands, items: items, selected: selected}
}

func (m *model) openHistoryMenu() {
	query := strings.ToLower(strings.TrimSpace(m.textarea.Value()))
	var items []menuItem
	for i := len(m.promptHistory) - 1; i >= 0; i-- {
		value := m.promptHistory[i]
		if query == "" || strings.Contains(strings.ToLower(value), query) {
			items = append(items, menuItem{value: value, label: firstLine(value)})
		}
	}
	m.menu = menuState{mode: menuHistory, items: items}
}

func (m *model) moveMenu(delta int) {
	if len(m.menu.items) == 0 {
		return
	}
	m.menu.selected = (m.menu.selected + delta + len(m.menu.items)) % len(m.menu.items)
}

func (m *model) acceptMenuSelection(closeMenu bool) {
	if len(m.menu.items) == 0 {
		return
	}
	item := m.menu.items[m.menu.selected]
	value := item.value
	if m.menu.mode == menuCommands && item.label != "/open" && item.label != "/show" {
		value += " "
	}
	m.textarea.SetValue(value)
	m.textarea.CursorEnd()
	if closeMenu {
		m.menu = menuState{}
	} else {
		m.refreshMenu()
	}
}

func (m *model) rememberPrompt(text string) {
	text = strings.TrimSpace(text)
	if text == "" || (len(m.promptHistory) > 0 && m.promptHistory[len(m.promptHistory)-1] == text) {
		return
	}
	m.promptHistory = append(m.promptHistory, text)
	if len(m.promptHistory) > 200 {
		m.promptHistory = m.promptHistory[len(m.promptHistory)-200:]
	}
	m.historyIndex = -1
}

func (m *model) restoreHistory(delta int) {
	if len(m.promptHistory) == 0 {
		return
	}
	if m.historyIndex < 0 {
		m.historyIndex = len(m.promptHistory)
	}
	m.historyIndex += delta
	if m.historyIndex < 0 {
		m.historyIndex = 0
	}
	if m.historyIndex >= len(m.promptHistory) {
		m.historyIndex = -1
		m.textarea.Reset()
		return
	}
	m.textarea.SetValue(m.promptHistory[m.historyIndex])
	m.textarea.CursorEnd()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	runes := []rune(s)
	if len(runes) > 72 {
		return string(runes[:72]) + "…"
	}
	return s
}
