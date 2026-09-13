package tui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	codercmds "github.com/basenana/friday/coder/commands"
	coderloop "github.com/basenana/friday/coder/loop"
	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
)

// Run launches the interactive TUI. Blocks until the user quits.
func Run(sessMgr *sessions.Manager, cfg *config.Config, sessionID string) error {
	registryConfig := actor.DefaultRegistryConfig()
	registryConfig.AgentPlanEntry = true
	registry := actor.NewRegistry(sessMgr, cfg, registryConfig)
	defer registry.ShutdownAll()

	cmdRegistry := codercmds.NewRegistry()
	codercmds.RegisterAll(cmdRegistry)

	m := loadingModel(sessMgr, registry, cmdRegistry, cfg, sessionID)
	defer m.loopManager.Close()
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

// RunProject launches the TUI with project-scoped root-session selection.
func RunProject(projectMgr *projectpkg.Manager, cfg *config.Config, sessionID string) error {
	registryConfig := actor.DefaultRegistryConfig()
	registryConfig.AgentPlanEntry = true
	registryConfig.Catalog = projectMgr
	registryConfig.Workdir = projectMgr.Project().Root()
	registry := actor.NewRegistry(projectMgr.Base(), cfg, registryConfig)
	defer registry.ShutdownAll()

	cmdRegistry := codercmds.NewRegistry()
	codercmds.RegisterAll(cmdRegistry)
	m := loadingModelAt(projectMgr.Base(), registry, cmdRegistry, cfg, sessionID, projectMgr.Project().Root())
	defer m.loopManager.Close()
	m.projectMgr = projectMgr
	m.runtime = projectMgr
	m.alternateScreen = useAlternateScreen(cfg.TUI.AlternateScreen)
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

// sessionRuntime is the narrow persisted capability used by the TUI after a
// root has been selected. Project mode supplies project.Manager; legacy mode
// supplies sessions.Manager.
type sessionRuntime interface {
	GetStore() sessions.Store
	CollaborationMode(string) collaboration.Mode
	Runtime(string) (sessions.SessionRuntime, error)
	UpdateMeta(string, sessions.SessionMetaPatch) error
	SetMode(string, collaboration.Mode) error
	SetModel(string, sessions.ModelSelection) error
	ClearModel(string) error
	LoadLatestPlan(string) (*planning.Artifact, error)
	SavePlan(string, planning.Artifact) error
}

type model struct {
	sessMgr     *sessions.Manager
	runtime     sessionRuntime
	projectMgr  *projectpkg.Manager
	registry    *actor.Registry
	sessionID   string
	feed        *bus.Feed
	loopManager *coderloop.Manager
	workdir     string

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
	selector         *selectorState
	commandConfirm   *commandConfirmation
	planHandoff      *planHandoffState
	planCompacting   bool
	manualCompacting bool

	running         bool
	currentRunID    string
	lastFinishedRun string
	runStartedAt    time.Time
	runActivity     string
	cancelling      bool
	steeringPending bool
	textBuf         strings.Builder
	reasonBuf       strings.Builder
	toolCalls       map[string]*toolCallBlock
	toolOrder       []string
	loopRuns        map[string]bool
	loopFinalRuns   map[string]bool
	finishLoopCalls map[string]string
	queued          []pendingInput
	promptHistory   []string
	historyIndex    int

	tokenCount  int
	iteration   int
	mode        collaboration.Mode
	latestPlan  *planning.Artifact
	activeModel config.ModelConfig

	width, height      int
	quitting           bool
	replaying          bool
	loading            bool
	requestedSessionID string
	fatalErr           error
	now                func() time.Time
}

func initialModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, sessionID string) (*model, error) {
	m := baseModel(sessMgr, registry, cmdRegistry, cfg, sessionID)
	if err := m.bindSession(sessionID); err != nil {
		return nil, err
	}
	if err := m.loadTranscript(sessionID); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "restore transcript: " + err.Error()})
	}
	if plan, err := sessMgr.LoadLatestPlan(sessionID); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "restore plan: " + err.Error()})
	} else {
		m.latestPlan = plan
		m.restorePlanHandoff()
	}
	return m, nil
}

func loadingModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, requestedSessionID string) *model {
	return loadingModelAt(sessMgr, registry, cmdRegistry, cfg, requestedSessionID, "")
}

func loadingModelAt(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, requestedSessionID, workdir string) *model {
	m := baseModelAt(sessMgr, registry, cmdRegistry, cfg, requestedSessionID, workdir)
	m.loading = true
	m.requestedSessionID = requestedSessionID
	return m
}

func baseModel(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, sessionID string) *model {
	return baseModelAt(sessMgr, registry, cmdRegistry, cfg, sessionID, "")
}

func baseModelAt(sessMgr *sessions.Manager, registry *actor.Registry, cmdRegistry *codercmds.Registry, cfg *config.Config, sessionID, workdir string) *model {
	configureTheme(true)
	ta := textarea.New()
	ta.Placeholder = "Send a message…  / commands · Ctrl+G editor"
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Prompt = "› "
	removeTextareaBackground(&ta)

	m := &model{
		sessMgr: sessMgr, runtime: sessMgr, registry: registry, cmdRegistry: cmdRegistry, cfg: cfg,
		sessionID: sessionID, textarea: ta, viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		spinner:   spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(accentStyle)),
		toolCalls: make(map[string]*toolCallBlock), seenInputs: make(map[string]bool),
		cards: make(map[string]*cardState), loopRuns: make(map[string]bool),
		loopFinalRuns: make(map[string]bool), finishLoopCalls: make(map[string]string),
		historyIndex: -1, darkBackground: true,
		now: time.Now,
	}
	m.loopManager = coderloop.NewManager(registry.Bus())
	m.mode = m.runtime.CollaborationMode(sessionID)
	m.activeModel, _ = configuredSessionModel(m.runtime, cfg, sessionID)
	// Focus after the textarea has reached its final storage location. The
	// component keeps an internal pointer to its active style, so focusing a
	// temporary value before copying it can leave that pointer attached to the
	// discarded copy.
	m.textarea.Focus()
	m.workdir = workdir
	if m.workdir == "" {
		if wd, err := os.Getwd(); err == nil {
			m.workdir = wd
		}
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
		return m.dispatchIfIdle()
	case cardSourceLoadedMsg:
		m.applyCardSourceLoaded(msg)
		return m, nil
	case diffLoadedMsg:
		if msg.err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "diff: " + msg.err.Error()})
		} else {
			m.detail = newDetailState("working tree diff", msg.content, m.width, m.height)
			m.layout()
		}
		return m, nil
	case initialSessionLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.fatalErr = msg.err
			m.appendBlock(chatBlock{kind: blockError, content: "load session: " + msg.err.Error()})
			return m, nil
		}
		m.sessionID = msg.sessionID
		m.mode = m.runtime.CollaborationMode(msg.sessionID)
		m.activeModel, _ = configuredSessionModel(m.runtime, m.cfg, msg.sessionID)
		m.feed = msg.feed
		if lifecycle, ok := m.registry.Lifecycle(msg.sessionID); ok && lifecycle.Current() != nil {
			if err := m.loopManager.Attach(context.Background(), lifecycle.Current()); err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: "restore loop: " + err.Error()})
			}
		}
		m.subscriptionToken++
		m.applyProjection(msg.projection)
		if plan, err := m.runtime.LoadLatestPlan(msg.sessionID); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "restore plan: " + err.Error()})
		} else {
			m.latestPlan = plan
			m.restorePlanHandoff()
		}
		m.layout()
		return m, m.waitForActorEvent()
	case planCompactFinishedMsg:
		return m.finishPlanApproval(msg)
	case manualCompactFinishedMsg:
		return m.finishManualCompact(msg)
	case dispatchQueuedMsg:
		return m.dispatchNextQueued()
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			m.loopManager.Close()
			m.closeFeed()
			return m, tea.Quit
		}
		if m.loading || m.fatalErr != nil || m.planCompacting || m.manualCompacting {
			return m, nil
		}
		if m.detail != nil {
			return m.updateDetail(msg)
		}
		if m.planHandoff != nil {
			return m.updatePlanHandoff(msg)
		}
		if m.commandConfirm != nil {
			return m.updateCommandConfirmation(msg)
		}
		if m.selector != nil {
			return m.updateSelector(msg)
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
		if msg.event.Type == events.KindRunFinished && m.canDispatchQueued() {
			cmds = append(cmds, func() tea.Msg { return dispatchQueuedMsg{} })
		}
		return m, tea.Batch(cmds...)
	case feedClosedMsg:
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.loading || m.running || m.planCompacting || m.manualCompacting {
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
	sessMgr, runtime, registry, projectMgr := m.sessMgr, m.runtime, m.registry, m.projectMgr
	requested := m.requestedSessionID
	cfg, workdir, width, height := m.cfg, m.workdir, m.width, m.height
	return func() tea.Msg {
		if projectMgr != nil {
			sessionID, created, err := prepareInitialProjectSession(projectMgr, requested)
			if err != nil {
				return initialSessionLoadedMsg{err: err}
			}
			cleanup := func() {
				if created {
					_ = projectMgr.DeleteRoot(sessionID)
				}
			}
			projection, err := buildTranscriptProjection(runtime, cfg, workdir, width, height, sessionID)
			if err != nil {
				cleanup()
				return initialSessionLoadedMsg{sessionID: sessionID, err: err}
			}
			if err := normalizeSessionModel(runtime, cfg, sessionID); err != nil {
				cleanup()
				return initialSessionLoadedMsg{sessionID: sessionID, err: err}
			}
			feed := bus.SubscribeAgentFeed(registry.Bus(), sessionID)
			if _, err := registry.GetOrCreate(sessionID); err != nil {
				feed.Close()
				cleanup()
				return initialSessionLoadedMsg{sessionID: sessionID, err: err}
			}
			if err := projectMgr.Activate(sessionID); err != nil {
				feed.Close()
				registry.Shutdown(sessionID)
				cleanup()
				return initialSessionLoadedMsg{sessionID: sessionID, err: err}
			}
			return initialSessionLoadedMsg{sessionID: sessionID, feed: feed, projection: projection}
		}
		sessionID, err := prepareInitialSessionID(sessMgr, requested)
		if err != nil {
			return initialSessionLoadedMsg{err: err}
		}
		projection, err := buildTranscriptProjection(runtime, cfg, workdir, width, height, sessionID)
		if err != nil {
			return initialSessionLoadedMsg{sessionID: sessionID, err: err}
		}
		if err := normalizeSessionModel(runtime, cfg, sessionID); err != nil {
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

func prepareInitialProjectSession(manager *projectpkg.Manager, requested string) (string, bool, error) {
	if requested != "" {
		has, err := manager.Contains(requested)
		if err != nil {
			return "", false, err
		}
		if !has {
			return "", false, fmt.Errorf("session is not referenced by this project: %s", requested)
		}
		if _, err := manager.GetMeta(requested); err != nil {
			return "", false, err
		}
		return requested, false, nil
	}
	current, err := manager.CurrentID()
	if err != nil {
		return "", false, err
	}
	if current != "" {
		return current, false, nil
	}
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		return "", false, err
	}
	id := lifecycle.RootID()
	_ = lifecycle.Close()
	return id, true, nil
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
	if msg.String() == "shift+tab" && !m.running && strings.TrimSpace(m.textarea.Value()) == "" {
		next := collaboration.ModePlan
		if m.mode == collaboration.ModePlan {
			next = collaboration.ModeDefault
		}
		return m.applyResult(codercmds.ResultOf(codercmds.SetModeAction{Mode: next}))
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
		if m.cancelActiveLoop() {
			return m, nil
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
	if strings.HasPrefix(text, "/") {
		name, _, _ := parseSlash(text)
		if cmd, ok := m.cmdRegistry.Lookup(name); ok && m.running {
			if codercmds.CommandMetadata(cmd).Policy == codercmds.PolicyDeferred {
				m.queued = append(m.queued, pendingInput{text: text})
				m.layout()
				return m, nil
			}
		}
		return m.handleSlash(text)
	}
	if m.running {
		return m.startUserTurn(text, bus.DeliverySteer)
	}
	return m.startUserTurn(text, bus.DeliveryNormal)
}

func (m *model) queueComposer() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	m.rememberPrompt(text)
	if lifecycle, ok := m.registry.Lifecycle(m.sessionID); ok && lifecycle.Current() != nil {
		active, err := m.loopManager.IsActive(context.Background(), lifecycle.Current())
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "queue input: " + err.Error()})
		} else if active {
			turnID := types.NewID()
			m.appendBlock(chatBlock{kind: blockUser, id: turnID, content: text})
			m.seenInputs[turnID] = true
			m.textarea.Reset()
			m.menu = menuState{}
			if err := m.sendUserText(text, bus.DeliveryNormal, turnID); err != nil {
				m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
				return m, nil
			}
			m.layout()
			return m, m.spinner.Tick
		}
	}
	m.queued = append(m.queued, pendingInput{text: text})
	m.textarea.Reset()
	m.menu = menuState{}
	m.layout()
	return m, nil
}

func (m *model) dispatchNextQueued() (tea.Model, tea.Cmd) {
	if !m.canDispatchQueued() {
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
		m.runStartedAt = m.nowTime()
		m.runActivity = "working"
	}
	m.resetStreaming()
	return m, m.spinner.Tick
}

func (m *model) cancelRun() (tea.Model, tea.Cmd) {
	m.cancelActiveLoop()
	m.registry.Bus().Publish(bus.TopicPreempt(m.sessionID),
		bus.NewScopedPreempt(m.sessionID, "user.local", "user cancelled", bus.PreemptCurrent))
	m.cancelling = true
	return m, nil
}

func (m *model) cancelActiveLoop() bool {
	if m.loopManager == nil {
		return false
	}
	if lifecycle, ok := m.registry.Lifecycle(m.sessionID); ok && lifecycle.Current() != nil {
		if cancelled, err := m.loopManager.Cancel(context.Background(), lifecycle.Current()); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "cancel loop: " + err.Error()})
		} else if cancelled {
			m.appendBlock(chatBlock{kind: blockDivider, content: "loop · cancelled"})
			return true
		}
	}
	return false
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
		if m.currentRunID != evt.RunID || m.runStartedAt.IsZero() {
			m.runStartedAt = evt.Timestamp
			if m.runStartedAt.IsZero() {
				m.runStartedAt = m.nowTime()
			}
		}
		m.running = true
		m.currentRunID = evt.RunID
		m.runActivity = "working"
		m.cancelling = false
		m.steeringPending = false
	case events.KindRunFinished:
		var d events.RunFinishedData
		_ = events.DecodePayload(evt, &d)
		m.flushStreaming(d.StopReason == "cancelled")
		hiddenLoopRun := m.isHiddenLoopRun(evt.RunID)
		finishedCurrent := evt.RunID != m.lastFinishedRun && (evt.RunID == m.currentRunID || m.currentRunID == "")
		if finishedCurrent {
			if !hiddenLoopRun {
				m.appendRunFinished(d, evt.Timestamp)
			}
			m.lastFinishedRun = evt.RunID
			m.running = false
			m.currentRunID = ""
			m.runStartedAt = time.Time{}
			m.runActivity = ""
			if m.form != nil {
				m.appendBlock(chatBlock{kind: blockDivider, content: "unfinished form expired · ask the agent again"})
				m.form = nil
			}
		}
		m.cancelling = false
		if finishedCurrent && !m.replaying && d.StopReason == "plan_completed" && m.mode == collaboration.ModePlan && m.latestPlan != nil && m.latestPlan.Status == planning.ArtifactProposed {
			m.planHandoff = &planHandoffState{}
		}
	case events.KindRunError:
		var d events.RunErrorData
		if events.DecodePayload(evt, &d) == nil && d.Message != "" &&
			!strings.Contains(strings.ToLower(d.Message), "context canceled") {
			m.appendBlock(chatBlock{kind: blockError, content: d.Message})
		}
	case events.KindTextMessageStart:
		if m.isHiddenLoopRun(evt.RunID) {
			return nil
		}
		m.runActivity = "responding"
		if m.reasonBuf.Len() > 0 {
			m.appendBlock(chatBlock{kind: blockReasoning, content: m.reasonBuf.String()})
			m.reasonBuf.Reset()
		}
		m.textBuf.Reset()
	case events.KindTextMessageContent:
		if m.isHiddenLoopRun(evt.RunID) {
			return nil
		}
		var d events.TextMessageContentData
		if events.DecodePayload(evt, &d) == nil {
			m.textBuf.WriteString(d.Content)
		}
	case events.KindTextMessageEnd:
		if m.isHiddenLoopRun(evt.RunID) {
			return nil
		}
		if m.textBuf.Len() > 0 {
			m.appendBlock(chatBlock{kind: blockAssistant, content: m.textBuf.String()})
		}
		m.textBuf.Reset()
	case events.KindToolCallStart:
		var d events.ToolCallStartData
		if events.DecodePayload(evt, &d) == nil {
			m.runActivity = "running " + d.ToolName
			if m.isLoopRun(evt.RunID) {
				if d.ToolName == "finish_loop" {
					m.finishLoopCalls[d.ToolCallID] = evt.RunID
				}
				return nil
			}
			m.toolCalls[d.ToolCallID] = &toolCallBlock{name: d.ToolName, id: d.ToolCallID}
			m.toolOrder = append(m.toolOrder, d.ToolCallID)
		}
	case events.KindToolCallArgs:
		if m.isLoopRun(evt.RunID) {
			return nil
		}
		var d events.ToolCallArgsData
		if events.DecodePayload(evt, &d) == nil {
			if tc := m.toolCalls[d.ToolCallID]; tc != nil {
				tc.input += d.PartialJSON
			}
		}
	case events.KindToolCallResult:
		var d events.ToolCallResultData
		if events.DecodePayload(evt, &d) == nil {
			m.runActivity = "working"
			if runID, ok := m.finishLoopCalls[d.ToolCallID]; ok {
				if d.Success {
					m.loopFinalRuns[runID] = true
				}
				delete(m.finishLoopCalls, d.ToolCallID)
				return nil
			}
			if m.isLoopRun(evt.RunID) {
				return nil
			}
			if tc := m.toolCalls[d.ToolCallID]; tc != nil {
				m.appendBlock(chatBlock{kind: blockToolCall, id: tc.id, toolName: tc.name,
					content: joinToolContent(tc.input, d.Output), success: d.Success})
				delete(m.toolCalls, d.ToolCallID)
				m.removeToolOrder(d.ToolCallID)
			}
		}
	case events.KindStepStarted:
		var d events.StepStartedData
		if events.DecodePayload(evt, &d) == nil && d.Kind == "model" {
			m.runActivity = "thinking"
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
		if events.DecodePayload(evt, &d) != nil {
			break
		}
		if slices.Contains(d.Sources, "loop") {
			m.ensureLoopRunMaps()
			m.loopRuns[evt.RunID] = true
			return nil
		}
		if !m.seenInputs[d.TurnID] {
			m.appendBlock(chatBlock{kind: blockUser, id: d.TurnID, content: d.Text})
			m.seenInputs[d.TurnID] = true
			m.rememberPrompt(d.Text)
		}
	case events.CustomReasoningDelta:
		if m.isLoopRun(evt.RunID) {
			return nil
		}
		var d events.ReasoningDeltaBody
		if events.DecodePayload(evt, &d) == nil {
			m.reasonBuf.WriteString(d.Content)
		}
	case events.CustomLoopStart:
		m.iteration++
		m.runActivity = "thinking"
	case events.CustomCompactStart:
		m.runActivity = "compacting context"
	case events.CustomCompactFinish, events.CustomCompactSkip:
		m.runActivity = "working"
	case events.CustomSubagentStart:
		m.runActivity = customActivity(evt, "working with subagent")
	case events.CustomSubagentFinish:
		m.runActivity = "working"
	case events.CustomModelTimeout:
		m.runActivity = "retrying model"
		m.appendBlock(chatBlock{kind: blockDivider, content: "model timed out · retrying"})
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
				m.runActivity = "waiting for input"
			}
		}
	case events.CustomFormSubmitted:
		var d events.FormSubmittedBody
		if events.DecodePayload(evt, &d) == nil && (m.form == nil || m.form.id == d.FormID) {
			m.form = nil
			m.runActivity = "working"
			m.appendBlock(chatBlock{kind: blockDivider, content: "form submitted"})
		}
	case events.CustomFormCancelled:
		var d events.FormCancelledBody
		if events.DecodePayload(evt, &d) == nil && (m.form == nil || m.form.id == d.FormID) {
			m.form = nil
			m.runActivity = "working"
			m.appendBlock(chatBlock{kind: blockDivider, content: "form cancelled"})
		}
	case events.CustomPlanProposed:
		var d events.PlanProposedBody
		if events.DecodePayload(evt, &d) == nil {
			m.latestPlan = &planning.Artifact{ID: d.PlanID, SessionID: m.sessionID, Version: d.Version, Title: d.Title, Markdown: d.Markdown, Status: planning.ArtifactProposed, CreatedAt: evt.Timestamp}
			m.appendBlock(chatBlock{kind: blockAssistant, content: "# " + d.Title + "\n\n" + d.Markdown})
		}
	case events.CustomModeChanged:
		var d events.ModeChangedBody
		if events.DecodePayload(evt, &d) == nil {
			if mode, err := collaboration.ParseMode(d.Mode); err == nil {
				m.mode = mode
				if !m.replaying {
					label := "mode · " + string(mode)
					if d.Source != "" {
						label += " · " + d.Source
					}
					m.appendBlock(chatBlock{kind: blockDivider, content: label})
				}
			}
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

func (m *model) ensureLoopRunMaps() {
	if m.loopRuns == nil {
		m.loopRuns = make(map[string]bool)
	}
	if m.loopFinalRuns == nil {
		m.loopFinalRuns = make(map[string]bool)
	}
	if m.finishLoopCalls == nil {
		m.finishLoopCalls = make(map[string]string)
	}
}

func (m *model) isLoopRun(runID string) bool {
	return runID != "" && m.loopRuns[runID]
}

func (m *model) isHiddenLoopRun(runID string) bool {
	return m.isLoopRun(runID) && !m.loopFinalRuns[runID]
}

func (m *model) nowTime() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *model) appendRunFinished(data events.RunFinishedData, finishedAt time.Time) {
	duration := time.Duration(data.DurationMs) * time.Millisecond
	if data.DurationMs <= 0 && !m.runStartedAt.IsZero() && !finishedAt.IsZero() {
		duration = finishedAt.Sub(m.runStartedAt)
	}
	if duration < 0 {
		duration = 0
	}
	label := "completed in " + formatElapsed(duration)
	switch data.StopReason {
	case "plan_completed":
		label = "plan ready in " + formatElapsed(duration)
	case "cancelled":
		label = "cancelled after " + formatElapsed(duration)
	case "error":
		label = "failed after " + formatElapsed(duration)
	}
	m.appendBlock(chatBlock{kind: blockDivider, content: label})
}

func (m *model) currentElapsed() time.Duration {
	if m.runStartedAt.IsZero() {
		return 0
	}
	elapsed := m.nowTime().Sub(m.runStartedAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func customActivity(evt events.Event, fallback string) string {
	var data events.CustomData
	if events.DecodePayload(evt, &data) == nil {
		if agent, _ := data.Body["agent"].(string); strings.TrimSpace(agent) != "" {
			return "working with " + strings.TrimSpace(agent)
		}
	}
	return fallback
}

func (m *model) restorePlanHandoff() {
	if m.mode == collaboration.ModePlan && m.latestPlan != nil && m.latestPlan.Status == planning.ArtifactProposed && !m.running {
		m.planHandoff = &planHandoffState{}
	}
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
	if err := normalizeSessionModel(m.runtime, m.cfg, sessionID); err != nil {
		return fmt.Errorf("validate session model: %w", err)
	}
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
