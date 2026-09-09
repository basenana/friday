package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sessions"
)

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

	cmdRegistry := codercmds.NewRegistry()
	codercmds.RegisterBuiltins(cmdRegistry)
	codercmds.RegisterInfoCommands(cmdRegistry)
	codercmds.RegisterAgentCommands(cmdRegistry)

	m, err := initialModel(sessMgr, registry, cmdRegistry, cfg, sessionID)
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
	sessionID string
	feed      *bus.Feed

	cmdRegistry *codercmds.Registry
	cfg         *config.Config

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

func initialModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, sessionID string) (*model, error) {
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
		sessMgr:     sessMgr,
		registry:    registry,
		cmdRegistry: cmdRegistry,
		cfg:         cfg,
		sessionID:   sessionID,
		textarea:    ta,
		viewport:    vp,
		spinner:     sp,
		toolCalls:   make(map[string]*toolCallBlock),
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
			m.closeFeed()
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
			if err := m.sendUserText(text); err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
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

	case feedClosedMsg:
		// The previous feed was closed on session switch or quit; nothing
		// to do — the new binding issued its own waitForActorEvent.
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

// cancelRun aborts the current agent run while keeping the TUI (and the
// actor) alive. The feed stays subscribed; a late RUN_FINISHED from the
// cancelled turn is idempotent because running is already false.
func (m *model) cancelRun() (tea.Model, tea.Cmd) {
	sid := m.sessionID
	m.registry.Bus().Publish(bus.TopicPreempt(sid), bus.NewPreempt(sid, "user.local", "user cancelled"))
	m.flushStreaming()
	m.appendBlock(chatBlock{kind: blockError, content: "[cancelled]"})
	m.running = false
	return m, nil
}

// sendUserText ensures the session's actor (and its bus bridges) exist and
// publishes the text on the inbox topic. Delivery failures are reported
// asynchronously via status.inbox_dropped envelopes.
func (m *model) sendUserText(text string) error {
	if _, err := m.registry.GetOrCreate(m.sessionID); err != nil {
		return err
	}
	m.registry.Bus().Publish(bus.TopicInbox(m.sessionID),
		bus.NewUserInput(m.sessionID, "user.local", bus.UserTextInput{Text: text}))
	return nil
}

// handleActorEvent maps an AG-UI event to model state mutations.
func (m *model) handleActorEvent(evt events.Event) {
	switch evt.Type {
	case events.KindRunStarted:
		m.running = true

	case events.KindRunFinished:
		m.flushStreaming()
		m.running = false

	case events.KindRunError:
		msg := "unknown error"
		var d events.RunErrorData
		if events.DecodePayload(evt, &d) == nil && d.Message != "" {
			msg = d.Message
		}
		m.appendBlock(chatBlock{kind: blockError, content: msg})

	case events.KindTextMessageStart:
		// Reasoning → text transition: flush pending reasoning first
		// (reasoning.delta has no explicit END marker).
		if m.reasonBuf.Len() > 0 {
			m.appendBlock(chatBlock{kind: blockReasoning, content: m.reasonBuf.String()})
			m.reasonBuf.Reset()
		}
		m.textBuf.Reset()

	case events.KindTextMessageContent:
		var d events.TextMessageContentData
		if events.DecodePayload(evt, &d) == nil {
			m.textBuf.WriteString(d.Content)
		}

	case events.KindTextMessageEnd:
		if m.textBuf.Len() > 0 {
			m.appendBlock(chatBlock{kind: blockAssistant, content: m.textBuf.String()})
		}
		m.textBuf.Reset()

	case events.KindToolCallStart:
		var d events.ToolCallStartData
		if events.DecodePayload(evt, &d) == nil {
			m.toolCalls[d.ToolCallID] = &toolCallBlock{name: d.ToolName, id: d.ToolCallID}
		}

	case events.KindToolCallArgs:
		var d events.ToolCallArgsData
		if events.DecodePayload(evt, &d) == nil {
			if tc, ok := m.toolCalls[d.ToolCallID]; ok {
				tc.input += d.PartialJSON
			}
		}

	case events.KindToolCallEnd:
		// no-op: the block is finalized on TOOL_CALL_RESULT.

	case events.KindToolCallResult:
		var d events.ToolCallResultData
		if events.DecodePayload(evt, &d) != nil {
			return
		}
		if tc, ok := m.toolCalls[d.ToolCallID]; ok {
			m.appendBlock(chatBlock{
				kind:     blockToolCall,
				toolName: tc.name,
				content:  tc.input + "\n" + d.Output,
				success:  d.Success,
			})
			delete(m.toolCalls, d.ToolCallID)
		}

	case events.KindStepFinished:
		var d events.StepFinishedData
		if events.DecodePayload(evt, &d) == nil {
			if v, ok := d.Data["total_tokens"]; ok {
				if n, err := strconv.Atoi(v); err == nil {
					m.tokenCount = n
				}
			}
		}

	case events.KindCustom:
		m.handleCustomEvent(evt)
	}
}

// handleCustomEvent renders CUSTOM events (differentiated by Name).
func (m *model) handleCustomEvent(evt events.Event) {
	switch evt.Name {
	case events.CustomReasoningDelta:
		var d events.ReasoningDeltaBody
		if events.DecodePayload(evt, &d) == nil {
			m.reasonBuf.WriteString(d.Content)
		}

	case events.CustomLoopStart:
		m.iteration++

	case events.CustomCardEmitted:
		var d events.CardEmittedBody
		if events.DecodePayload(evt, &d) == nil {
			title := d.Title
			if title == "" {
				title = d.Kind
			}
			m.appendBlock(chatBlock{kind: blockAssistant, content: fmt.Sprintf("[card] %s (TUI card rendering not supported)", title)})
		}

	case events.CustomFormRequested:
		var d events.FormRequestedBody
		if events.DecodePayload(evt, &d) == nil {
			m.appendBlock(chatBlock{kind: blockAssistant, content: fmt.Sprintf("[form] %s — interactive forms are not supported in TUI yet", d.FormID)})
		}

	case "status." + bus.StatusInboxDropped:
		var d bus.InboxDropped
		if events.DecodePayload(evt, &d) == nil {
			m.appendBlock(chatBlock{kind: blockError, content: "message dropped: " + d.Reason})
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

func (m *model) closeFeed() {
	if m.feed != nil {
		m.feed.Close()
		m.feed = nil
	}
}

func (m *model) ensureSession(sessionID string) error {
	if _, _, err := m.sessMgr.GetOrCreateByID(sessionID); err != nil {
		return fmt.Errorf("failed to activate session %s: %w", shortID(sessionID), err)
	}
	return nil
}

// bindSession subscribes the model to every outbound topic of sessionID on
// the registry bus and (re)creates the session's actor and bridges. The
// feed persists across turns; it is only replaced on session switch.
func (m *model) bindSession(sessionID string) error {
	m.closeFeed()

	// Subscribe before actor creation so status.created is retained as the
	// epoch boundary and no startup event can race this binding.
	feed := bus.SubscribeAgentFeed(m.registry.Bus(), sessionID)
	if _, err := m.registry.GetOrCreate(sessionID); err != nil {
		feed.Close()
		return fmt.Errorf("failed to bind session %s: %w", shortID(sessionID), err)
	}

	m.sessionID = sessionID
	m.feed = feed
	m.subscriptionToken++
	return nil
}

func (m *model) waitForActorEvent() tea.Cmd {
	if m.feed == nil {
		return nil
	}
	return waitForActorEvent(m.feed, m.subscriptionToken)
}
