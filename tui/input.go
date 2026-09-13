package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/bus"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
)

func (m *model) handleSlash(text string) (tea.Model, tea.Cmd) {
	name, rawArgs, parts := parseSlash(text)
	if name == "" {
		return m, nil
	}
	cmd, found := m.cmdRegistry.Lookup(name)
	if !found {
		m.appendBlock(chatBlock{kind: blockError, content: "unknown command: /" + name + " (try /help)"})
		return m.dispatchIfIdle()
	}
	lifecycle, _ := m.registry.Lifecycle(m.sessionID)
	legacyManager := m.sessMgr
	if m.projectMgr != nil {
		legacyManager = nil
	}
	result, err := cmd.Execute(&codercmds.Context{
		Ctx: context.Background(), SessionID: m.sessionID, Args: parts, RawArgs: rawArgs,
		Session: lifecycle, SessMgr: legacyManager, Config: m.cfg, Mode: m.mode,
	})
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m.dispatchIfIdle()
	}
	return m.applyResult(result)
}

func parseSlash(text string) (name, rawArgs string, args []string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", nil
	}
	body := strings.TrimPrefix(text, "/")
	cut := strings.IndexAny(body, " \t\r\n")
	if cut < 0 {
		return strings.ToLower(body), "", nil
	}
	name = strings.ToLower(body[:cut])
	rawArgs = strings.TrimSpace(body[cut:])
	return name, rawArgs, strings.Fields(rawArgs)
}

func (m *model) applyResult(r *codercmds.Result) (tea.Model, tea.Cmd) {
	if r == nil {
		return m.dispatchIfIdle()
	}
	var cmds []tea.Cmd
	for _, action := range r.Actions {
		if cmd := m.applyCommandAction(action); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.canDispatchQueued() {
		cmds = append(cmds, func() tea.Msg { return dispatchQueuedMsg{} })
	}
	return m, tea.Batch(cmds...)
}

func (m *model) applyCommandAction(action codercmds.Action) tea.Cmd {
	for _, handler := range []func(codercmds.Action) (bool, tea.Cmd){
		m.applyLifecycleAction,
		m.applySessionAction,
		m.applyContentAction,
		m.applyTaskAction,
		m.applyModelAction,
		m.applyCollaborationAction,
	} {
		if handled, cmd := handler(action); handled {
			return cmd
		}
	}
	m.appendBlock(chatBlock{kind: blockError, content: fmt.Sprintf("unsupported command action %T", action)})
	return nil
}

func (m *model) applyLifecycleAction(action codercmds.Action) (bool, tea.Cmd) {
	switch action := action.(type) {
	case codercmds.AppendMessageAction:
		if action.Content != "" {
			m.appendBlock(chatBlock{kind: blockAssistant, content: action.Content})
		}
		return true, nil
	case codercmds.QuitAction:
		m.quitting = true
		m.loopManager.Close()
		m.closeFeed()
		m.registry.Shutdown(m.sessionID)
		return true, tea.Quit
	}
	return false, nil
}

func (m *model) applySessionAction(action codercmds.Action) (bool, tea.Cmd) {
	switch action := action.(type) {
	case codercmds.CompactSessionAction:
		lifecycle, ok := m.registry.Lifecycle(m.sessionID)
		if !ok || lifecycle.Current() == nil {
			m.appendBlock(chatBlock{kind: blockError, content: "compact: active session unavailable"})
			return true, nil
		}
		m.manualCompacting = true
		m.layout()
		return true, tea.Batch(compactManually(m.sessionID, lifecycle.Current()), m.spinner.Tick)
	case codercmds.ClearSessionAction:
		if m.projectMgr != nil {
			newID, err := m.createProjectRoot(true)
			if err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: "clear: " + err.Error()})
				return true, nil
			}
			cmd, err := m.switchSession(newID)
			if err != nil {
				_ = m.projectMgr.DeleteRoot(newID)
				m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
				return true, nil
			}
			return true, tea.Batch(tea.ClearScreen, cmd)
		}
		if action.SessionID == "" || action.SessionID == m.sessionID {
			m.appendBlock(chatBlock{kind: blockError, content: "clear: invalid new session ID"})
			return true, nil
		}
		cmd, err := m.switchSession(action.SessionID)
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return true, nil
		}
		return true, tea.Batch(tea.ClearScreen, cmd)
	case codercmds.OpenResumeAction:
		m.openResumeSelector()
		return true, nil
	case codercmds.ResumeSessionAction:
		var meta *sessions.SessionMeta
		var err error
		if m.projectMgr != nil {
			meta, err = m.projectMgr.Resolve(action.Target)
		} else {
			meta, err = m.sessMgr.ResolveActiveSession(action.Target)
		}
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return true, nil
		}
		cmd, err := m.switchSession(meta.ID)
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return true, nil
		}
		return true, cmd
	case codercmds.RenameSessionAction:
		var name string
		var err error
		if m.projectMgr != nil {
			name, err = m.projectMgr.Rename(m.sessionID, action.Name)
		} else {
			name, err = m.sessMgr.Rename(m.sessionID, action.Name)
		}
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "rename: " + err.Error()})
		} else {
			m.appendBlock(chatBlock{kind: blockDivider, content: "renamed · " + name})
		}
		return true, nil
	case codercmds.ArchiveSessionAction:
		m.requestSessionConfirmation("archive", action.Target)
		return true, nil
	case codercmds.DeleteSessionAction:
		m.requestSessionConfirmation("delete", action.Target)
		return true, nil
	}
	return false, nil
}

func (m *model) applyContentAction(action codercmds.Action) (bool, tea.Cmd) {
	switch action := action.(type) {
	case codercmds.OpenCardAction:
		_, cmd := m.handleOpenCommand([]string{action.ID})
		return true, cmd
	case codercmds.ShowToolAction:
		_, cmd := m.handleShowCommand([]string{action.ID})
		return true, cmd
	case codercmds.ShowDiffAction:
		return true, m.showDiff()
	case codercmds.CopyResponseAction:
		m.copyResponse(action.Index)
		return true, nil
	}
	return false, nil
}

func (m *model) applyTaskAction(action codercmds.Action) (bool, tea.Cmd) {
	switch action := action.(type) {
	case codercmds.ShowTasksAction:
		m.openTasksSelector()
		return true, nil
	case codercmds.StopTaskAction:
		if action.ID == "all" {
			m.commandConfirm = &commandConfirmation{action: "stop", target: "all", label: "all background tasks"}
		} else if err := m.registry.KillTask(m.sessionID, action.ID); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "stop: " + err.Error()})
		} else {
			m.appendBlock(chatBlock{kind: blockDivider, content: "stopped task " + action.ID})
		}
		return true, nil
	}
	return false, nil
}

func (m *model) applyModelAction(action codercmds.Action) (bool, tea.Cmd) {
	switch action := action.(type) {
	case codercmds.OpenModelAction:
		m.openModelSelector()
		return true, nil
	case codercmds.SetModelAction:
		model, err := m.resolveModel(action.Target)
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return true, nil
		}
		_, cmd := m.applyModel(model)
		return true, cmd
	case codercmds.ShowStatusAction:
		m.showStatus()
		return true, nil
	}
	return false, nil
}

func (m *model) applyCollaborationAction(action codercmds.Action) (bool, tea.Cmd) {
	switch action := action.(type) {
	case codercmds.RunAgentAction:
		return true, m.runAgentCmd(action.Agent, action.Input)
	case codercmds.SetModeAction:
		mode := action.Mode
		if mode == collaboration.ModePlan && m.mode == mode && action.Prompt == "" && m.latestPlan != nil && m.latestPlan.Status == planning.ArtifactProposed {
			m.planHandoff = &planHandoffState{}
			return true, nil
		}
		if err := m.runtime.SetMode(m.sessionID, mode); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "set mode: " + err.Error()})
			return true, nil
		}
		m.mode = mode
		m.appendBlock(chatBlock{kind: blockDivider, content: "mode · " + string(mode)})
		if action.Prompt != "" {
			_, cmd := m.startUserTurn(action.Prompt, bus.DeliveryNormal)
			return true, cmd
		}
		return true, nil
	case codercmds.StartLoopAction:
		if m.mode == collaboration.ModePlan {
			m.appendBlock(chatBlock{kind: blockError, content: "/loop is unavailable in Plan Mode; run /plan off first"})
			return true, nil
		}
		lifecycle, ok := m.registry.Lifecycle(m.sessionID)
		if !ok || lifecycle.Current() == nil {
			m.appendBlock(chatBlock{kind: blockError, content: "loop: active session unavailable"})
			return true, nil
		}
		if err := m.loopManager.Start(context.Background(), lifecycle.Current(), action.Task); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "loop: " + err.Error()})
			return true, nil
		}
		m.loopActive = true
		m.appendBlock(chatBlock{kind: blockDivider, content: "loop · started"})
		return true, m.spinner.Tick
	}
	return false, nil
}

func (m *model) dispatchIfIdle() (tea.Model, tea.Cmd) {
	if m.canDispatchQueued() {
		return m, func() tea.Msg { return dispatchQueuedMsg{} }
	}
	return m, nil
}

func (m *model) canDispatchQueued() bool {
	return !m.running && !m.planCompacting && !m.manualCompacting && len(m.queued) > 0 && m.form == nil && m.planHandoff == nil &&
		m.commandConfirm == nil && m.selector == nil && m.detail == nil && m.confirm == nil
}

func (m *model) switchSession(newID string) (tea.Cmd, error) {
	if newID == m.sessionID {
		return nil, nil
	}
	created := false
	var err error
	if m.projectMgr != nil {
		has, containsErr := m.projectMgr.Contains(newID)
		if containsErr != nil {
			return nil, containsErr
		}
		if !has {
			return nil, fmt.Errorf("session is not referenced by this project: %s", shortID(newID))
		}
		if _, err = m.projectMgr.GetMeta(newID); err != nil {
			return nil, fmt.Errorf("prepare session %s: %w", shortID(newID), err)
		}
	} else {
		_, created, err = m.sessMgr.GetOrCreateDetachedByID(newID)
		if err != nil {
			return nil, fmt.Errorf("prepare session %s: %w", shortID(newID), err)
		}
	}
	rollbackCreated := func() error {
		if created {
			return m.sessMgr.GetStore().Delete(newID)
		}
		return nil
	}
	if created {
		oldRuntime, runtimeErr := m.runtime.Runtime(m.sessionID)
		if runtimeErr != nil {
			return nil, sessionSwitchError(fmt.Errorf("load current session runtime: %w", runtimeErr), rollbackCreated())
		}
		oldRuntime.Mode = collaboration.ModeDefault
		if err := m.runtime.UpdateMeta(newID, sessions.SessionMetaPatch{Runtime: &oldRuntime}); err != nil {
			return nil, sessionSwitchError(fmt.Errorf("initialize session runtime: %w", err), rollbackCreated())
		}
	}
	if err := normalizeSessionModel(m.sessMgr, m.cfg, newID); err != nil {
		return nil, sessionSwitchError(fmt.Errorf("validate session model: %w", err), rollbackCreated())
	}
	latestPlan, err := m.runtime.LoadLatestPlan(newID)
	if err != nil {
		return nil, sessionSwitchError(fmt.Errorf("load session plan: %w", err), rollbackCreated())
	}
	activeModel, _ := configuredSessionModel(m.runtime, m.cfg, newID)

	newFeed := bus.SubscribeAgentFeed(m.registry.Bus(), newID)
	if _, err := m.registry.GetOrCreate(newID); err != nil {
		newFeed.Close()
		return nil, sessionSwitchError(fmt.Errorf("prepare actor %s: %w", shortID(newID), err), rollbackCreated())
	}

	projection, err := m.projectTranscript(newID)
	if err != nil {
		newFeed.Close()
		m.registry.Shutdown(newID)
		return nil, sessionSwitchError(fmt.Errorf("restore session %s: %w", shortID(newID), err), rollbackCreated())
	}
	if m.projectMgr != nil {
		err = m.projectMgr.Activate(newID)
	} else {
		err = m.sessMgr.SetCurrentID(newID)
	}
	if err != nil {
		newFeed.Close()
		m.registry.Shutdown(newID)
		return nil, sessionSwitchError(fmt.Errorf("activate session %s: %w", shortID(newID), err), rollbackCreated())
	}

	oldID, oldFeed := m.sessionID, m.feed
	m.loopManager.Detach(oldID)
	m.sessionID, m.feed = newID, newFeed
	m.mode = m.runtime.CollaborationMode(newID)
	m.latestPlan = latestPlan
	m.activeModel = activeModel
	m.subscriptionToken++
	m.tokenCount, m.iteration = 0, 0
	m.running, m.cancelling, m.steeringPending = false, false, false
	m.loopActive = false
	m.runStartedAt = time.Time{}
	m.runActivity = ""
	m.lastFinishedRun = ""
	m.planHandoff = nil
	m.planProposalRunID = ""
	m.queued = nil
	m.resetStreaming()
	m.applyProjection(projection)
	m.restorePlanHandoff()
	if lifecycle, ok := m.registry.Lifecycle(newID); ok && lifecycle.Current() != nil {
		if err := m.loopManager.Attach(context.Background(), lifecycle.Current()); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "restore loop: " + err.Error()})
		} else if err := m.refreshLoopStatus(); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "restore loop status: " + err.Error()})
		}
	}
	if oldFeed != nil {
		oldFeed.Close()
	}
	m.registry.Shutdown(oldID)
	return m.waitForActorEvent(), nil
}

func (m *model) createProjectRoot(inheritRuntime bool) (string, error) {
	lifecycle, err := m.projectMgr.CreateRoot(context.Background(), nil)
	if err != nil {
		return "", err
	}
	id := lifecycle.RootID()
	_ = lifecycle.Close()
	if !inheritRuntime || m.sessionID == "" {
		return id, nil
	}
	runtimeState, err := m.runtime.Runtime(m.sessionID)
	if err != nil {
		_ = m.projectMgr.DeleteRoot(id)
		return "", err
	}
	runtimeState.Mode = collaboration.ModeDefault
	if err := m.runtime.UpdateMeta(id, sessions.SessionMetaPatch{Runtime: &runtimeState}); err != nil {
		_ = m.projectMgr.DeleteRoot(id)
		return "", err
	}
	return id, nil
}

func sessionSwitchError(cause, cleanup error) error {
	if cleanup == nil {
		return cause
	}
	return fmt.Errorf("%w; cleanup session: %v", cause, cleanup)
}

func (m *model) runAgentCmd(agentName, input string) tea.Cmd {
	if m.running {
		m.appendBlock(chatBlock{kind: blockError, content: "a task is already running; send it after the current task with Tab"})
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
	for _, cmd := range m.cmdRegistry.List() {
		label := "/" + cmd.Name()
		search := label + " " + cmd.Description() + " " + strings.Join(cmd.Aliases(), " ")
		if query == "" || strings.Contains(strings.ToLower(search), query) {
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
	if m.menu.mode == menuCommands {
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
