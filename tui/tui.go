package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
)

const subscriptionBuffer = 256

// Run launches the interactive TUI. Blocks until the user quits.
func Run(sessMgr *sessions.Manager, cfg *config.Config, sessionID string) error {
	if sessionID == "" {
		// Reuse the current session, or create one.
		_, id, _, err := sessMgr.GetOrCreateCurrent()
		if err != nil {
			return fmt.Errorf("failed to get session: %w", err)
		}
		sessionID = id
	} else {
		if _, _, err := sessMgr.GetOrCreateByID(sessionID); err != nil {
			return fmt.Errorf("failed to activate session: %w", err)
		}
	}

	registry := actor.NewRegistry(sessMgr, cfg, actor.DefaultRegistryConfig())
	defer registry.ShutdownAll()

	m, err := initialModel(sessMgr, registry, sessionID)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}

type model struct {
	sessMgr   *sessions.Manager
	registry  *actor.Registry
	actor     *actor.Actor
	sessionID string
	events    <-chan actor.Event

	unsubscribe       func()
	subscriptionToken uint64

	messages []chatBlock

	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	running   bool
	textBuf   strings.Builder
	reasonBuf strings.Builder
	toolCalls map[string]*toolCallBlock

	tokenCount int
	iteration  int

	width, height int
	quitting      bool
}

func initialModel(sessMgr *sessions.Manager, registry *actor.Registry, sessionID string) (*model, error) {
	ta := textarea.New()
	ta.Placeholder = "Send a message... (Enter to send, Ctrl+C to cancel/quit, / for commands)"
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	vp := viewport.New(80, 20)
	vp.SetContent("")

	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("205"))),
	)

	m := &model{
		sessMgr:   sessMgr,
		registry:  registry,
		sessionID: sessionID,
		textarea:  ta,
		viewport:  vp,
		spinner:   sp,
		toolCalls: make(map[string]*toolCallBlock),
	}
	if err := m.bindSession(sessionID); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.waitForActorEvent(),
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = msg.Width
		// Reserve space: status bar (1) + input border (5) + padding
		inputH := 5
		m.viewport.Height = msg.Height - 1 - inputH
		if m.viewport.Height < 3 {
			m.viewport.Height = 3
		}
		m.textarea.SetWidth(msg.Width - 4)
		m.invalidateRendered()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.running {
				return m.cancelRun()
			}
			m.quitting = true
			m.closeSubscription()
			return m, tea.Quit

		case tea.KeyEsc:
			if m.running {
				return m.cancelRun()
			}
			m.textarea.Reset()
			return m, nil

		case tea.KeyEnter:
			if m.running {
				return m, nil
			}
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}
			m.textarea.Reset()
			if strings.HasPrefix(text, "/") {
				return m.handleSlash(text)
			}
			m.appendBlock(chatBlock{kind: blockUser, content: text})
			if !m.actor.Send(actor.Message{Content: text}) {
				m.appendBlock(chatBlock{kind: blockError, content: "inbox full, try again"})
				return m, nil
			}
			m.running = true
			m.textBuf.Reset()
			m.reasonBuf.Reset()
			m.toolCalls = make(map[string]*toolCallBlock)
			return m, m.spinner.Tick
		}

	case actorEventMsg:
		if msg.token != m.subscriptionToken {
			return m, nil
		}
		cmds := []tea.Cmd{m.waitForActorEvent()}
		m.handleActorEvent(msg.event)
		if m.running {
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(cmds...)

	case actorDoneMsg:
		if msg.token != m.subscriptionToken {
			return m, nil
		}
		// Current subscription closed — recreate for next turn.
		if !m.quitting {
			if err := m.bindSession(m.sessionID); err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
				return m, nil
			}
			return m, m.waitForActorEvent()
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	if shouldRouteToViewport(msg) {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	// Forward to textarea
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// cancelRun aborts the current agent run while keeping the TUI alive.
func (m *model) cancelRun() (tea.Model, tea.Cmd) {
	m.closeSubscription()
	m.registry.Shutdown(m.sessionID)
	m.flushStreaming()
	m.appendBlock(chatBlock{kind: blockError, content: "[cancelled]"})
	m.running = false
	if err := m.bindSession(m.sessionID); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m, nil
	}
	return m, m.waitForActorEvent()
}

// handleActorEvent maps an AG-UI event to model state mutations.
func (m *model) handleActorEvent(evt actor.Event) {
	switch evt.Type {
	case actor.EventRunStarted:
		m.running = true

	case actor.EventRunFinished:
		m.flushStreaming()
		m.running = false

	case actor.EventRunError:
		msg, _ := evt.Data["message"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		m.appendBlock(chatBlock{kind: blockError, content: msg})

	case actor.EventTextMessageStart:
		m.textBuf.Reset()

	case actor.EventTextMessageContent:
		if d, ok := evt.Data["delta"].(string); ok {
			m.textBuf.WriteString(d)
		}

	case actor.EventTextMessageEnd:
		if m.textBuf.Len() > 0 {
			m.appendBlock(chatBlock{kind: blockAssistant, content: m.textBuf.String()})
		}
		m.textBuf.Reset()

	case actor.EventReasoningStart:
		m.reasonBuf.Reset()

	case actor.EventReasoningMessageContent:
		if d, ok := evt.Data["delta"].(string); ok {
			m.reasonBuf.WriteString(d)
		}

	case actor.EventReasoningEnd:
		if m.reasonBuf.Len() > 0 {
			m.appendBlock(chatBlock{kind: blockReasoning, content: m.reasonBuf.String()})
		}
		m.reasonBuf.Reset()

	case actor.EventToolCallStart:
		name, _ := evt.Data["tool_name"].(string)
		id, _ := evt.Data["tool_call_id"].(string)
		input, _ := evt.Data["input"].(string)
		m.toolCalls[id] = &toolCallBlock{name: name, id: id, input: input}

	case actor.EventToolCallResult:
		id, _ := evt.Data["tool_call_id"].(string)
		out, _ := evt.Data["output"].(string)
		success, _ := evt.Data["success"].(bool)
		if tc, ok := m.toolCalls[id]; ok {
			tc.output = out
			tc.success = success
		}

	case actor.EventToolCallEnd:
		id, _ := evt.Data["tool_call_id"].(string)
		if tc, ok := m.toolCalls[id]; ok {
			m.appendBlock(chatBlock{
				kind:     blockToolCall,
				toolName: tc.name,
				content:  tc.input + "\n" + tc.output,
				success:  tc.success,
			})
			delete(m.toolCalls, id)
		}

	case actor.EventStepStarted:
		if name, _ := evt.Data["step_name"].(string); name == "react_loop" {
			m.iteration++
		}

	case actor.EventStepFinished:
		if v, ok := evt.Data["total_tokens"]; ok {
			if n, ok := toInt(v); ok {
				m.tokenCount = n
			}
		}
	}
}

// flushStreaming finalizes any in-progress text/reasoning/tool blocks when a
// run ends or is cancelled mid-stream.
func (m *model) flushStreaming() {
	if m.textBuf.Len() > 0 {
		m.appendBlock(chatBlock{kind: blockAssistant, content: m.textBuf.String()})
		m.textBuf.Reset()
	}
	if m.reasonBuf.Len() > 0 {
		m.appendBlock(chatBlock{kind: blockReasoning, content: m.reasonBuf.String()})
		m.reasonBuf.Reset()
	}
	for id, tc := range m.toolCalls {
		m.appendBlock(chatBlock{
			kind:     blockToolCall,
			toolName: tc.name,
			content:  tc.input + "\n" + tc.output,
			success:  tc.success,
		})
		delete(m.toolCalls, id)
	}
}

func (m *model) appendBlock(b chatBlock) {
	m.messages = append(m.messages, b)
}

func (m *model) closeSubscription() {
	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}
	m.events = nil
}

func (m *model) ensureSession(sessionID string) error {
	if _, _, err := m.sessMgr.GetOrCreateByID(sessionID); err != nil {
		return fmt.Errorf("failed to activate session %s: %w", shortID(sessionID), err)
	}
	return nil
}

func (m *model) bindSession(sessionID string) error {
	m.closeSubscription()

	m.actor = m.registry.GetOrCreate(sessionID)
	events, unsubscribe, err := m.registry.Subscribe(sessionID, subscriptionBuffer)
	if err != nil {
		return fmt.Errorf("failed to subscribe session %s: %w", shortID(sessionID), err)
	}

	m.sessionID = sessionID
	m.events = events
	m.unsubscribe = unsubscribe
	m.subscriptionToken++
	return nil
}

func (m *model) waitForActorEvent() tea.Cmd {
	if m.events == nil {
		return nil
	}
	return waitForActorEvent(m.events, m.subscriptionToken)
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
