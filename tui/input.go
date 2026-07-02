package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basenana/friday/actor"
	codercmds "github.com/basenana/friday/coder/commands"
)

// handleSlash dispatches slash commands via the registry.
func (m *model) handleSlash(text string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return m, nil
	}
	name := strings.TrimPrefix(parts[0], "/")
	cmd, found := m.cmdRegistry.Lookup(name)
	if !found {
		m.appendBlock(chatBlock{kind: blockError, content: "unknown command: " + parts[0] + " (try /help)"})
		return m, nil
	}

	result, err := cmd.Execute(&codercmds.Context{
		Ctx:       context.Background(),
		SessionID: m.sessionID,
		Args:      parts[1:],
		SessMgr:   m.sessMgr,
		ActorReg:  m.registry,
		Config:    m.cfg,
	})
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m, nil
	}
	return m.applyResult(result)
}

// applyResult translates a codercmds.Result into TUI state changes and commands.
func (m *model) applyResult(r *codercmds.Result) (tea.Model, tea.Cmd) {
	if r == nil {
		return m, nil
	}
	var cmds []tea.Cmd

	if r.ClearMessages {
		m.messages = nil
		m.invalidateRendered()
	}
	if r.SwitchSession != "" && r.SwitchSession != m.sessionID {
		if cmd, err := m.switchSession(r.SwitchSession); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		} else if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if r.Quit {
		m.quitting = true
		m.closeSubscription()
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
	return m, tea.Batch(cmds...)
}

// switchSession synchronously tears down the current actor binding and binds
// to newID. Returns a tea.Cmd (waitForActorEvent) for the new subscription.
func (m *model) switchSession(newID string) (tea.Cmd, error) {
	if err := m.ensureSession(newID); err != nil {
		return nil, err
	}
	m.closeSubscription()
	m.registry.Shutdown(m.sessionID)
	m.sessionID = newID
	m.messages = nil
	m.tokenCount = 0
	m.iteration = 0
	m.running = false
	m.textBuf.Reset()
	m.reasonBuf.Reset()
	m.toolCalls = make(map[string]*toolCallBlock)
	if err := m.bindSession(newID); err != nil {
		return nil, err
	}
	return m.waitForActorEvent(), nil
}

// runAgentCmd routes an agent-backed command (/plan, /review, /advisor) through
// the main actor. The main agent's subagent hook exposes a run_task tool that
// delegates to the named expert. We wrap the user input with an instruction
// telling the main agent to delegate, then send as a normal message. The
// agent's streamed response (which relays the expert's report) appears in the
// chat view via the standard actorEventMsg path.
func (m *model) runAgentCmd(agentName, input string) tea.Cmd {
	if m.running {
		m.appendBlock(chatBlock{kind: blockError, content: "a task is already running; wait or cancel first"})
		return nil
	}
	wrapped := fmt.Sprintf("[/%s] %s\n\nDelegate this to the %q subagent via the run_task tool. Return the subagent's final report verbatim as your answer.",
		agentName, input, agentName)

	m.appendBlock(chatBlock{kind: blockUser, content: fmt.Sprintf("[/%s] %s", agentName, input)})
	if !m.actor.Send(actor.MessageFromText(wrapped)) {
		m.appendBlock(chatBlock{kind: blockError, content: "inbox full, try again"})
		return nil
	}
	m.running = true
	m.textBuf.Reset()
	m.reasonBuf.Reset()
	m.toolCalls = make(map[string]*toolCallBlock)
	return m.spinner.Tick
}
