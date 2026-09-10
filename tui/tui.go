package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
)

// Run launches the interactive TUI. Blocks until the user quits.
func Run(sessMgr *sessions.Manager, cfg *config.Config, sessionID string) error {
	registry := actor.NewRegistry(sessMgr, cfg, actor.DefaultRegistryConfig())
	defer registry.ShutdownAll()

	cmdRegistry := codercmds.NewRegistry()
	codercmds.RegisterBuiltins(cmdRegistry)
	codercmds.RegisterInfoCommands(cmdRegistry)
	codercmds.RegisterAgentCommands(cmdRegistry)

	m := loadingModel(sessMgr, registry, cmdRegistry, cfg, sessionID)
	m.alternateScreen = useAlternateScreen(cfg.TUI.AlternateScreen)
	// Do not enable terminal mouse reporting: leaving it disabled preserves the
	// terminal's native click-and-drag text selection behavior.
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return err
	}
	if result, ok := final.(*model); ok && result.fatalErr != nil {
		return result.fatalErr
	}
	return nil
}

func useAlternateScreen(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "never":
		return false
	case "always":
		return true
	default:
		return os.Getenv("ZELLIJ") == ""
	}
}

type pendingInput struct {
	text string
}

type menuMode int

const (
	menuNone menuMode = iota
	menuCommands
	menuHistory
)

type menuState struct {
	mode     menuMode
	items    []menuItem
	selected int
}

type menuItem struct {
	value, label, description string
}

type model struct {
	sessMgr   *sessions.Manager
	registry  *actor.Registry
	sessionID string
	feed      *bus.Feed
	workdir   string

	cmdRegistry *codercmds.Registry
	cfg         *config.Config

	subscriptionToken uint64
	messages          []chatBlock
	seenInputs        map[string]bool
	cards             map[string]*cardState

	textarea         textarea.Model
	viewport         viewport.Model
	spinner          spinner.Model
	markdownRenderer *glamour.TermRenderer
	markdownWidth    int
	darkBackground   bool
	alternateScreen  bool
	menu             menuState
	form             *formState
	confirm          *openConfirmation
	detail           *detailState

	running         bool
	currentRunID    string
	cancelling      bool
	steeringPending bool
	textBuf         strings.Builder
	reasonBuf       strings.Builder
	toolCalls       map[string]*toolCallBlock
	toolOrder       []string
	queued          []pendingInput
	promptHistory   []string
	historyIndex    int

	tokenCount int
	iteration  int

	width, height      int
	quitting           bool
	replaying          bool
	loading            bool
	requestedSessionID string
	fatalErr           error
}

func initialModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, sessionID string) (*model, error) {
	m := baseModel(sessMgr, registry, cmdRegistry, cfg, sessionID)
	if err := m.bindSession(sessionID); err != nil {
		return nil, err
	}
	if err := m.loadTranscript(sessionID); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "restore transcript: " + err.Error()})
	}
	return m, nil
}

func loadingModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, requestedSessionID string) *model {
	m := baseModel(sessMgr, registry, cmdRegistry, cfg, requestedSessionID)
	m.loading = true
	m.requestedSessionID = requestedSessionID
	return m
}

func baseModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, sessionID string) *model {
	configureTheme(true)
	ta := textarea.New()
	ta.Placeholder = "Send a message…  / commands · Ctrl+G editor"
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Prompt = "› "
	removeTextareaBackground(&ta)

	m := &model{
		sessMgr: sessMgr, registry: registry, cmdRegistry: cmdRegistry, cfg: cfg,
		sessionID: sessionID, textarea: ta, viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		spinner:   spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(accentStyle)),
		toolCalls: make(map[string]*toolCallBlock), seenInputs: make(map[string]bool),
		cards: make(map[string]*cardState), historyIndex: -1, darkBackground: true,
	}
	// Focus after the textarea has reached its final storage location. The
	// component keeps an internal pointer to its active style, so focusing a
	// temporary value before copying it can leave that pointer attached to the
	// discarded copy.
	m.textarea.Focus()
	if wd, err := os.Getwd(); err == nil {
		m.workdir = wd
	}
	return m
}

func (m *model) Init() tea.Cmd {
	if m.loading {
		return tea.Batch(textarea.Blink, m.spinner.Tick, m.loadInitialSession(), tea.RequestBackgroundColor)
	}
	return tea.Batch(textarea.Blink, m.spinner.Tick, m.waitForActorEvent(), tea.RequestBackgroundColor)
}

type dispatchQueuedMsg struct{}

type initialSessionLoadedMsg struct {
	sessionID  string
	feed       *bus.Feed
	projection transcriptProjection
	err        error
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.invalidateRendered()
		m.markdownRenderer = nil
		m.markdownWidth = 0
		return m, nil
	case tea.BackgroundColorMsg:
		m.applyTheme(msg.IsDark())
		return m, nil
	case editorFinishedMsg:
		if msg.err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "editor: " + msg.err.Error()})
		} else {
			m.textarea.SetValue(msg.text)
			m.textarea.CursorEnd()
		}
		m.refreshMenu()
		return m, nil
	case openFinishedMsg:
		if msg.err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "open: " + msg.err.Error()})
		}
		return m, nil
	case cardSourceLoadedMsg:
		m.applyCardSourceLoaded(msg)
		return m, nil
	case initialSessionLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.fatalErr = msg.err
			m.appendBlock(chatBlock{kind: blockError, content: "load session: " + msg.err.Error()})
			return m, nil
		}
		m.sessionID = msg.sessionID
		m.feed = msg.feed
		m.subscriptionToken++
		m.applyProjection(msg.projection)
		m.layout()
		return m, m.waitForActorEvent()
	case dispatchQueuedMsg:
		return m.dispatchNextQueued()
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			m.closeFeed()
			return m, tea.Quit
		}
		if m.loading || m.fatalErr != nil {
			return m, nil
		}
		if m.detail != nil {
			return m.updateDetail(msg)
		}
		if m.confirm != nil {
			return m.updateOpenConfirmation(msg)
		}
		if m.form != nil {
			return m.updateForm(msg)
		}
		var focusCmd tea.Cmd
		if !m.textarea.Focused() {
			focusCmd = m.textarea.Focus()
		}
		next, keyCmd := m.updateKey(msg)
		return next, tea.Batch(focusCmd, keyCmd)
	case actorEventMsg:
		if msg.token != m.subscriptionToken {
			return m, nil
		}
		eventCmd := m.handleActorEvent(msg.event)
		cmds := []tea.Cmd{m.waitForActorEvent()}
		if eventCmd != nil {
			cmds = append(cmds, eventCmd)
		}
		if m.running {
			cmds = append(cmds, m.spinner.Tick)
		}
		if msg.event.Type == events.KindRunFinished && !m.running && len(m.queued) > 0 {
			cmds = append(cmds, func() tea.Msg { return dispatchQueuedMsg{} })
		}
		return m, tea.Batch(cmds...)
	case feedClosedMsg:
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.loading || m.running {
			return m, cmd
		}
		return m, nil
	case tea.MouseMsg:
		// Mouse reporting is intentionally disabled by Run so the terminal owns
		// selection. Ignore synthetic mouse messages in embedded/test programs.
		return m, nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.afterComposerEdit()
	return m, cmd
}

func (m *model) loadInitialSession() tea.Cmd {
	sessMgr, registry := m.sessMgr, m.registry
	requested := m.requestedSessionID
	cfg, workdir, width, height := m.cfg, m.workdir, m.width, m.height
	return func() tea.Msg {
		sessionID, err := prepareInitialSessionID(sessMgr, requested)
		if err != nil {
			return initialSessionLoadedMsg{err: err}
		}
		projection, err := buildTranscriptProjection(sessMgr, cfg, workdir, width, height, sessionID)
		if err != nil {
			return initialSessionLoadedMsg{sessionID: sessionID, err: err}
		}
		feed := bus.SubscribeAgentFeed(registry.Bus(), sessionID)
		if _, err := registry.GetOrCreate(sessionID); err != nil {
			feed.Close()
			return initialSessionLoadedMsg{sessionID: sessionID, err: err}
		}
		return initialSessionLoadedMsg{sessionID: sessionID, feed: feed, projection: projection}
	}
}

func prepareInitialSessionID(sessMgr *sessions.Manager, requested string) (string, error) {
	if requested != "" {
		exists, err := sessMgr.Exists(requested)
		if err != nil {
			return "", err
		}
		if !exists {
			if _, _, err := sessMgr.GetOrCreateDetachedByID(requested); err != nil {
				return "", err
			}
		}
		if err := sessMgr.SetCurrentID(requested); err != nil {
			return "", err
		}
		return requested, nil
	}
	current, err := sessMgr.GetCurrentID()
	if err != nil {
		return "", err
	}
	if current != "" {
		exists, err := sessMgr.Exists(current)
		if err != nil {
			return "", err
		}
		if exists {
			return current, nil
		}
	}
	_, sessionID, _, err := sessMgr.GetOrCreateCurrent()
	return sessionID, err
}

func removeTextareaBackground(ta *textarea.Model) {
	styles := ta.Styles()
	styles.Focused.Base = styles.Focused.Base.UnsetBackground()
	styles.Focused.CursorLine = styles.Focused.CursorLine.UnsetBackground()
	styles.Focused.Text = styles.Focused.Text.UnsetBackground().Foreground(themeText)
	styles.Focused.Placeholder = styles.Focused.Placeholder.UnsetBackground().Foreground(themeMuted)
	styles.Focused.Prompt = styles.Focused.Prompt.UnsetBackground().Foreground(themeAccent)
	styles.Blurred.Base = styles.Blurred.Base.UnsetBackground()
	styles.Blurred.CursorLine = styles.Blurred.CursorLine.UnsetBackground()
	styles.Blurred.Text = styles.Blurred.Text.UnsetBackground().Foreground(themeText)
	styles.Blurred.Placeholder = styles.Blurred.Placeholder.UnsetBackground().Foreground(themeMuted)
	styles.Blurred.Prompt = styles.Blurred.Prompt.UnsetBackground().Foreground(themeAccent)
	styles.Cursor.Color = themeAccent
	ta.SetStyles(styles)
}

func (m *model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		m.closeFeed()
		return m, tea.Quit
	}
	if msg.String() == "ctrl+g" {
		return m, m.openEditor()
	}
	if msg.String() == "ctrl+r" {
		m.openHistoryMenu()
		return m, nil
	}
	if msg.Code == tea.KeyPgUp || msg.Code == tea.KeyPgDown || msg.String() == "ctrl+u" || msg.String() == "ctrl+d" {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	if msg.String() == "ctrl+l" && !m.running {
		m.messages = nil
		m.cards = make(map[string]*cardState)
		m.invalidateRendered()
		return m, tea.ClearScreen
	}

	if m.menu.mode != menuNone {
		switch msg.Code {
		case tea.KeyUp:
			m.moveMenu(-1)
			return m, nil
		case tea.KeyDown:
			m.moveMenu(1)
			return m, nil
		case tea.KeyTab:
			if m.menu.mode == menuCommands {
				m.acceptMenuSelection(false)
			} else {
				m.acceptMenuSelection(true)
			}
			return m, nil
		case tea.KeyEsc:
			m.menu = menuState{}
			return m, nil
		case tea.KeyEnter:
			if m.menu.mode == menuHistory {
				m.acceptMenuSelection(true)
				return m, nil
			}
			m.acceptMenuSelection(true)
			return m.submitComposer()
		}
	}

	switch msg.Code {
	case tea.KeyEsc:
		if m.running {
			return m.cancelRun()
		}
		m.textarea.Reset()
		m.menu = menuState{}
		return m, nil
	case tea.KeyTab:
		if m.running {
			return m.queueComposer()
		}
	case tea.KeyEnter:
		if msg.Mod.Contains(tea.ModAlt) || msg.String() == "shift+enter" {
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
			break
		}
		return m.submitComposer()
	case tea.KeyUp:
		if m.textarea.Value() == "" && len(m.promptHistory) > 0 {
			m.restoreHistory(-1)
			return m, nil
		}
	case tea.KeyDown:
		if m.historyIndex >= 0 {
			m.restoreHistory(1)
			return m, nil
		}
	}
	if msg.String() == "ctrl+j" {
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.afterComposerEdit()
	return m, cmd
}

func (m *model) submitComposer() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	m.rememberPrompt(text)
	m.textarea.Reset()
	m.menu = menuState{}
	if m.running {
		return m.startUserTurn(text, bus.DeliverySteer)
	}
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}
	return m.startUserTurn(text, bus.DeliveryNormal)
}

func (m *model) queueComposer() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	m.rememberPrompt(text)
	m.queued = append(m.queued, pendingInput{text: text})
	m.textarea.Reset()
	m.menu = menuState{}
	m.layout()
	return m, nil
}

func (m *model) dispatchNextQueued() (tea.Model, tea.Cmd) {
	if m.running || len(m.queued) == 0 {
		return m, nil
	}
	next := m.queued[0]
	m.queued = m.queued[1:]
	if strings.HasPrefix(strings.TrimSpace(next.text), "/") {
		return m.handleSlash(next.text)
	}
	return m.startUserTurn(next.text, bus.DeliveryNormal)
}

func (m *model) startUserTurn(text string, delivery bus.InputDelivery) (tea.Model, tea.Cmd) {
	turnID := types.NewID()
	if delivery == bus.DeliverySteer {
		m.flushStreaming(true)
	}
	m.appendBlock(chatBlock{kind: blockUser, id: turnID, content: text})
	m.seenInputs[turnID] = true
	if err := m.sendUserText(text, delivery, turnID); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m, nil
	}
	if delivery == bus.DeliverySteer {
		m.steeringPending = true
	} else {
		m.running = true
	}
	m.resetStreaming()
	return m, m.spinner.Tick
}

func (m *model) cancelRun() (tea.Model, tea.Cmd) {
	m.registry.Bus().Publish(bus.TopicPreempt(m.sessionID),
		bus.NewScopedPreempt(m.sessionID, "user.local", "user cancelled", bus.PreemptCurrent))
	m.cancelling = true
	return m, nil
}

func (m *model) sendUserText(text string, delivery bus.InputDelivery, turnID string) error {
	if _, err := m.registry.GetOrCreate(m.sessionID); err != nil {
		return err
	}
	m.registry.Bus().Publish(bus.TopicInbox(m.sessionID), bus.NewUserInput(m.sessionID, "user.local", bus.UserTextInput{
		Text: text, TurnID: turnID, Delivery: delivery,
	}))
	return nil
}

func (m *model) handleActorEvent(evt events.Event) tea.Cmd {
	var cmd tea.Cmd
	switch evt.Type {
	case events.KindRunStarted:
		m.running = true
		m.currentRunID = evt.RunID
		m.cancelling = false
		m.steeringPending = false
	case events.KindRunFinished:
		var d events.RunFinishedData
		_ = events.DecodePayload(evt, &d)
		m.flushStreaming(d.StopReason == "cancelled")
		if evt.RunID == m.currentRunID || m.currentRunID == "" {
			m.running = false
			m.currentRunID = ""
		}
		m.cancelling = false
		if !m.replaying && len(m.queued) > 0 {
			// Defer dispatch until after this event update completes.
			m.running = false
		}
	case events.KindRunError:
		var d events.RunErrorData
		if events.DecodePayload(evt, &d) == nil && d.Message != "" &&
			!strings.Contains(strings.ToLower(d.Message), "context canceled") {
			m.appendBlock(chatBlock{kind: blockError, content: d.Message})
		}
	case events.KindTextMessageStart:
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
			m.toolOrder = append(m.toolOrder, d.ToolCallID)
		}
	case events.KindToolCallArgs:
		var d events.ToolCallArgsData
		if events.DecodePayload(evt, &d) == nil {
			if tc := m.toolCalls[d.ToolCallID]; tc != nil {
				tc.input += d.PartialJSON
			}
		}
	case events.KindToolCallResult:
		var d events.ToolCallResultData
		if events.DecodePayload(evt, &d) == nil {
			if tc := m.toolCalls[d.ToolCallID]; tc != nil {
				m.appendBlock(chatBlock{kind: blockToolCall, id: tc.id, toolName: tc.name,
					content: joinToolContent(tc.input, d.Output), success: d.Success})
				delete(m.toolCalls, d.ToolCallID)
				m.removeToolOrder(d.ToolCallID)
			}
		}
	case events.KindStepFinished:
		var d events.StepFinishedData
		if events.DecodePayload(evt, &d) == nil {
			if v, ok := d.Data["total_tokens"]; ok {
				m.tokenCount, _ = strconv.Atoi(v)
			} else {
				prompt, _ := strconv.Atoi(d.Data["prompt_tokens"])
				completion, _ := strconv.Atoi(d.Data["completion_tokens"])
				if prompt+completion > 0 {
					m.tokenCount = prompt + completion
				}
			}
		}
	case events.KindCustom:
		cmd = m.handleCustomEvent(evt)
	}
	return cmd
}

func (m *model) handleCustomEvent(evt events.Event) tea.Cmd {
	switch evt.Name {
	case events.CustomInputAccepted:
		var d events.InputAcceptedBody
		if events.DecodePayload(evt, &d) == nil && !m.seenInputs[d.TurnID] {
			m.appendBlock(chatBlock{kind: blockUser, id: d.TurnID, content: d.Text})
			m.seenInputs[d.TurnID] = true
			m.rememberPrompt(d.Text)
		}
	case events.CustomReasoningDelta:
		var d events.ReasoningDeltaBody
		if events.DecodePayload(evt, &d) == nil {
			m.reasonBuf.WriteString(d.Content)
		}
	case events.CustomLoopStart:
		m.iteration++
	case events.CustomCardEmitted, events.CustomCardUpdated, events.CustomCardDismissed:
		return m.handleCardEvent(evt)
	case events.CustomFormRequested:
		var d events.FormRequestedBody
		if events.DecodePayload(evt, &d) == nil {
			form, err := newFormState(d.FormID, d.Schema, m.width)
			if err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: "form: " + err.Error()})
			} else {
				m.form = form
			}
		}
	case events.CustomFormSubmitted:
		var d events.FormSubmittedBody
		if events.DecodePayload(evt, &d) == nil && (m.form == nil || m.form.id == d.FormID) {
			m.form = nil
			m.appendBlock(chatBlock{kind: blockDivider, content: "form submitted"})
		}
	case events.CustomFormCancelled:
		var d events.FormCancelledBody
		if events.DecodePayload(evt, &d) == nil && (m.form == nil || m.form.id == d.FormID) {
			m.form = nil
			m.appendBlock(chatBlock{kind: blockDivider, content: "form cancelled"})
		}
	case "status." + bus.StatusInboxDropped:
		var d bus.InboxDropped
		if events.DecodePayload(evt, &d) == nil {
			if m.form != nil && d.FormID == m.form.id {
				m.form.submitting = false
				m.form.err = "submission failed: " + d.Reason
			}
			m.appendBlock(chatBlock{kind: blockError, content: "message dropped: " + d.Reason})
		}
	}
	return nil
}

func (m *model) resetStreaming() {
	m.textBuf.Reset()
	m.reasonBuf.Reset()
	m.toolCalls = make(map[string]*toolCallBlock)
	m.toolOrder = nil
}

func (m *model) flushStreaming(interrupted bool) {
	if m.textBuf.Len() > 0 {
		m.appendBlock(chatBlock{kind: blockAssistant, content: m.textBuf.String(), interrupted: interrupted})
	}
	if m.reasonBuf.Len() > 0 {
		m.appendBlock(chatBlock{kind: blockReasoning, content: m.reasonBuf.String(), interrupted: interrupted})
	}
	for _, id := range m.toolOrder {
		if tc := m.toolCalls[id]; tc != nil {
			m.appendBlock(chatBlock{kind: blockToolCall, id: id, toolName: tc.name,
				content: joinToolContent(tc.input, tc.output), success: tc.success, interrupted: interrupted})
		}
	}
	m.resetStreaming()
}

func joinToolContent(input, output string) string {
	if input == "" {
		return output
	}
	if output == "" {
		return input
	}
	return input + "\n" + output
}

func (m *model) removeToolOrder(id string) {
	for i, candidate := range m.toolOrder {
		if candidate == id {
			m.toolOrder = append(m.toolOrder[:i], m.toolOrder[i+1:]...)
			return
		}
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

func (m *model) bindSession(sessionID string) error {
	m.closeFeed()
	feed := bus.SubscribeAgentFeed(m.registry.Bus(), sessionID)
	if _, err := m.registry.GetOrCreate(sessionID); err != nil {
		feed.Close()
		return fmt.Errorf("failed to bind session %s: %w", shortID(sessionID), err)
	}
	m.sessionID, m.feed = sessionID, feed
	m.subscriptionToken++
	return nil
}

func (m *model) waitForActorEvent() tea.Cmd {
	if m.feed == nil {
		return nil
	}
	return waitForActorEvent(m.feed, m.subscriptionToken)
}
