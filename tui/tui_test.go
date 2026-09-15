package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basenana/friday/actor"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	actorsink "github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

type faultStore struct {
	sessions.Store
	metadata     sessions.MetadataStore
	plans        sessions.PlanningStore
	failNextSave bool
	failMode     bool
}

func (s *faultStore) UpdateMeta(sessionID string, patch sessions.SessionMetaPatch) error {
	if s.failMode && patch.Mode != nil {
		return fmt.Errorf("injected mode failure")
	}
	return s.metadata.UpdateMeta(sessionID, patch)
}

func (s *faultStore) ProposePlan(sessionID string, plan planning.Artifact) (*planning.Artifact, error) {
	return s.plans.ProposePlan(sessionID, plan)
}

func (s *faultStore) SavePlan(sessionID string, plan planning.Artifact) error {
	if s.failNextSave {
		s.failNextSave = false
		return fmt.Errorf("injected plan save failure")
	}
	return s.plans.SavePlan(sessionID, plan)
}

func (s *faultStore) LoadPlan(sessionID, planID string) (*planning.Artifact, error) {
	return s.plans.LoadPlan(sessionID, planID)
}

func (s *faultStore) LoadLatestPlan(sessionID string) (*planning.Artifact, error) {
	return s.plans.LoadLatestPlan(sessionID)
}

func newFaultTestModel(t *testing.T) (*model, *sessions.Manager, *sessionfile.FileSessionStore, *faultStore) {
	t.Helper()
	baseDir := t.TempDir()
	raw := sessionfile.NewFileSessionStore(filepath.Join(baseDir, "sessions"))
	currentFile := filepath.Join(baseDir, "current")
	store := &faultStore{Store: raw, metadata: raw, plans: raw}
	mgr := sessions.NewManager(store, currentFile, "test")
	const sessionID = "session-initial"
	if _, _, err := mgr.GetOrCreateByID(sessionID); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.DataDir = baseDir
	cfg.Workspace = filepath.Join(baseDir, "workspace")
	cfg.Memory.Enabled = false
	registry := actor.NewRegistry(mgr, cfg, actor.DefaultRegistryConfig())
	t.Cleanup(registry.ShutdownAll)
	commands := codercmds.NewRegistry()
	codercmds.RegisterAll(commands)
	m, err := initialModel(mgr, registry, commands, cfg, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return m, mgr, raw, store
}

type blockingEventStore struct {
	sessions.Store
	events  sessions.EventStore
	started chan struct{}
	release chan struct{}
}

func (s *blockingEventStore) OpenEventSink(ctx context.Context, id string) (actorsink.EventSink, error) {
	return s.events.OpenEventSink(ctx, id)
}

func (s *blockingEventStore) LoadEvents(ctx context.Context, id string) ([]events.Event, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return s.events.LoadEvents(ctx, id)
	}
}

func newTestModel(t *testing.T) (*model, *sessions.Manager, *sessionfile.FileSessionStore) {
	t.Helper()

	baseDir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(baseDir, "sessions"))
	mgr := sessions.NewManager(store, filepath.Join(baseDir, "current"), "test")

	sessionID := "session-initial"
	if _, _, err := mgr.GetOrCreateByID(sessionID); err != nil {
		t.Fatalf("GetOrCreateByID() failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = baseDir
	cfg.Workspace = filepath.Join(baseDir, "workspace")
	cfg.Memory.Enabled = false

	registry := actor.NewRegistry(mgr, cfg, actor.DefaultRegistryConfig())
	t.Cleanup(registry.ShutdownAll)

	cmdRegistry := codercmds.NewRegistry()
	codercmds.RegisterAll(cmdRegistry)

	m, err := initialModel(mgr, registry, cmdRegistry, cfg, sessionID)
	if err != nil {
		t.Fatalf("initialModel() failed: %v", err)
	}
	return m, mgr, store
}

func fillHistory(m *model, count int) {
	for i := 0; i < count; i++ {
		m.appendBlock(chatBlock{
			kind:    blockAssistant,
			content: fmt.Sprintf("message %02d", i),
		})
	}
}

func TestHandleSlashClearCreatesAndSwitchesCurrentSession(t *testing.T) {
	m, mgr, store := newTestModel(t)
	oldID := m.sessionID
	oldToken := m.subscriptionToken
	m.appendBlock(chatBlock{kind: blockAssistant, content: "stale"})

	gotModel, cmd := m.handleSlash("/clear")
	if cmd == nil {
		t.Fatal("handleSlash(/clear) returned nil cmd")
	}

	got, ok := gotModel.(*model)
	if !ok {
		t.Fatalf("handleSlash(/clear) returned %T, want *model", gotModel)
	}
	if got.sessionID == oldID {
		t.Fatal("session ID did not change")
	}
	if got.subscriptionToken <= oldToken {
		t.Fatalf("subscription token = %d, want > %d", got.subscriptionToken, oldToken)
	}

	currentID, err := mgr.GetCurrentID()
	if err != nil {
		t.Fatalf("GetCurrentID() failed: %v", err)
	}
	if currentID != got.sessionID {
		t.Fatalf("current session = %q, want %q", currentID, got.sessionID)
	}

	if _, err := store.GetMeta(got.sessionID); err != nil {
		t.Fatalf("GetMeta(%q) failed: %v", got.sessionID, err)
	}
	if len(got.messages) != 0 {
		t.Fatalf("messages = %#v, want cleared transcript after /clear", got.messages)
	}
}

func TestFailedSessionSwitchKeepsOldSessionAndTranscript(t *testing.T) {
	baseDir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(baseDir, "sessions"))
	if _, err := store.Create("old-session", nil); err != nil {
		t.Fatal(err)
	}
	// SetCurrentID will fail because its target is a directory. Preparation of
	// the detached session and actor still succeeds, exercising rollback after
	// all expensive setup work has completed.
	currentTarget := filepath.Join(baseDir, "current-is-directory")
	if err := os.Mkdir(currentTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := sessions.NewManager(store, currentTarget, "test")
	cfg := config.DefaultConfig()
	cfg.DataDir = baseDir
	cfg.Workspace = filepath.Join(baseDir, "workspace")
	cfg.Memory.Enabled = false
	registry := actor.NewRegistry(mgr, cfg, actor.DefaultRegistryConfig())
	t.Cleanup(registry.ShutdownAll)
	commands := codercmds.NewRegistry()
	codercmds.RegisterBuiltins(commands)
	m, err := initialModel(mgr, registry, commands, cfg, "old-session")
	if err != nil {
		t.Fatal(err)
	}
	m.appendBlock(chatBlock{kind: blockAssistant, content: "keep transcript"})
	oldFeed := m.feed

	m.applyResult(codercmds.ResultOf(codercmds.ClearSessionAction{SessionID: "new-session"}))
	if m.sessionID != "old-session" || m.feed != oldFeed {
		t.Fatalf("session changed after failed commit: id=%q", m.sessionID)
	}
	if len(m.messages) == 0 || m.messages[0].content != "keep transcript" {
		t.Fatalf("transcript was cleared: %#v", m.messages)
	}
	if _, ok := registry.Get("old-session"); !ok {
		t.Fatal("old actor was shut down during failed switch")
	}
	if _, ok := registry.Get("new-session"); ok {
		t.Fatal("prepared actor was not rolled back")
	}
	if _, err := store.GetMeta("new-session"); err == nil {
		t.Fatal("newly-created detached session was not rolled back")
	}
}

func TestUpdateIgnoresStaleSubscriptionMessages(t *testing.T) {
	m, _, _ := newTestModel(t)
	oldToken := m.subscriptionToken

	if _, cmd := m.handleSlash("/clear"); cmd == nil {
		t.Fatal("handleSlash(/clear) returned nil cmd")
	}
	newToken := m.subscriptionToken
	newSessionID := m.sessionID

	m.running = true
	gotModel, _ := m.Update(actorEventMsg{
		token: oldToken,
		event: events.NewEvent(events.KindRunFinished, "r"),
	})
	got := gotModel.(*model)
	if !got.running {
		t.Fatal("stale actor event changed running state")
	}
	if got.subscriptionToken != newToken {
		t.Fatalf("subscription token = %d, want %d", got.subscriptionToken, newToken)
	}
	if got.sessionID != newSessionID {
		t.Fatalf("session ID = %q, want %q", got.sessionID, newSessionID)
	}
}

func TestViewEnablesMouseWheelReporting(t *testing.T) {
	m, _, _ := newTestModel(t)
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode = %v, want cell motion", got)
	}
}

func TestMouseWheelScrollsConversation(t *testing.T) {
	m, _, _ := newTestModel(t)
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12}); cmd != nil {
		// no-op
	}

	fillHistory(m, 20)
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should start at bottom after initial render")
	}

	bottomOffset := m.viewport.YOffset()
	if bottomOffset == 0 {
		t.Fatal("expected scrollable content")
	}

	gotModel, _ := m.Update(tea.MouseWheelMsg{
		Button: tea.MouseWheelUp,
	})
	got := gotModel.(*model)
	if got.viewport.YOffset() >= bottomOffset {
		t.Fatalf("mouse wheel did not scroll conversation: got %d, bottom %d", got.viewport.YOffset(), bottomOffset)
	}
}

func TestMouseWheelScrollsDetailBeforeConversation(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	fillHistory(m, 20)
	_ = m.View()
	conversationOffset := m.viewport.YOffset()
	m.detail = newDetailState("details", strings.Repeat("detail line\n", 30), m.width, m.height)
	m.detail.view.GotoBottom()
	detailBottom := m.detail.view.YOffset()
	if detailBottom == 0 {
		t.Fatal("expected scrollable detail content")
	}

	gotModel, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	got := gotModel.(*model)
	if got.detail.view.YOffset() >= detailBottom {
		t.Fatalf("mouse wheel did not scroll detail: got %d, bottom %d", got.detail.view.YOffset(), detailBottom)
	}
	if got.viewport.YOffset() != conversationOffset {
		t.Fatalf("detail scroll moved conversation: got %d want %d", got.viewport.YOffset(), conversationOffset)
	}
}

func TestNonWheelMouseEventsRemainNonInteractive(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.planHandoff = &planHandoffState{selected: 1}
	gotModel, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	got := gotModel.(*model)
	if got.planHandoff == nil || got.planHandoff.selected != 1 {
		t.Fatalf("mouse click changed plan selection: %#v", got.planHandoff)
	}
}

func TestConversationBlocksUseSingleBlankLine(t *testing.T) {
	got := joinConversationBlocks([]string{"user\n\n", "\n\nassistant\n\n"})
	if got != "user\n\nassistant" {
		t.Fatalf("joined blocks = %q", got)
	}
}

func TestComposerHasTransparentBackground(t *testing.T) {
	m, _, _ := newTestModel(t)
	textareaStyles := m.textarea.Styles()
	styles := []lipgloss.Style{
		inputBoxStyle,
		textareaStyles.Focused.Base,
		textareaStyles.Focused.CursorLine,
		textareaStyles.Focused.Text,
		textareaStyles.Focused.Placeholder,
	}
	for i, style := range styles {
		if _, ok := style.GetBackground().(lipgloss.NoColor); !ok {
			t.Fatalf("style %d has a background: %#v", i, style.GetBackground())
		}
	}
}

func TestUpdateAcceptsTypingAndEnterSendsMessage(t *testing.T) {
	m, _, _ := newTestModel(t)
	for _, r := range "hello" {
		got, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = got.(*model)
	}
	if got := m.textarea.Value(); got != "hello" {
		t.Fatalf("typed value = %q", got)
	}
	got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(*model)
	if cmd == nil || !m.running {
		t.Fatalf("message was not dispatched: running=%v cmd=%v", m.running, cmd != nil)
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].kind != blockUser || m.messages[len(m.messages)-1].content != "hello" {
		t.Fatalf("messages = %#v", m.messages)
	}
}

func TestTypingRestoresLostComposerFocus(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.textarea.Blur()
	got, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = got.(*model)
	if !m.textarea.Focused() || m.textarea.Value() != "x" {
		t.Fatalf("focus=%v value=%q", m.textarea.Focused(), m.textarea.Value())
	}
}

func TestInitialSessionLoadRendersWhileStorageIsBlocked(t *testing.T) {
	baseDir := t.TempDir()
	files := sessionfile.NewFileSessionStore(filepath.Join(baseDir, "sessions"))
	if _, err := files.Create("slow-session", nil); err != nil {
		t.Fatal(err)
	}
	store := &blockingEventStore{
		Store: files, events: files, started: make(chan struct{}), release: make(chan struct{}),
	}
	mgr := sessions.NewManager(store, filepath.Join(baseDir, "current"), "test")
	if err := mgr.SetCurrentID("slow-session"); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.DataDir = baseDir
	cfg.Workspace = filepath.Join(baseDir, "workspace")
	cfg.Memory.Enabled = false
	registry := actor.NewRegistry(mgr, cfg, actor.DefaultRegistryConfig())
	t.Cleanup(registry.ShutdownAll)
	commands := codercmds.NewRegistry()
	m := loadingModel(mgr, registry, commands, cfg, "")
	result := make(chan tea.Msg, 1)
	go func() { result <- m.loadInitialSession()() }()
	<-store.started
	if got := m.View().Content; !strings.Contains(got, "Loading session") {
		t.Fatalf("loading view = %q", got)
	}
	close(store.release)
	msg := <-result
	updated, cmd := m.Update(msg)
	m = updated.(*model)
	if m.loading || m.feed == nil || m.sessionID != "slow-session" || cmd == nil {
		t.Fatalf("loaded state: loading=%v feed=%v session=%q", m.loading, m.feed != nil, m.sessionID)
	}
	m.closeFeed()
}

func TestBackgroundColorMessageUpdatesTheme(t *testing.T) {
	m, _, _ := newTestModel(t)
	darkAccent := accentStyle.GetForeground()

	updated, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	m = updated.(*model)
	if m.darkBackground {
		t.Fatal("light terminal background was not applied")
	}
	if lightAccent := accentStyle.GetForeground(); lightAccent == darkAccent {
		t.Fatalf("accent color did not change: %#v", lightAccent)
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("terminal background response leaked into composer: %q", got)
	}

	m.applyTheme(true)
}

func TestInteractiveStylesDistinguishSelectedAndInactiveRows(t *testing.T) {
	defer configureTheme(true)
	for _, dark := range []bool{false, true} {
		configureTheme(dark)
		selected := interactiveStyle(true)
		if !selected.GetBold() || selected.GetForeground() != themeAccent {
			t.Fatalf("dark=%v selected style: bold=%v foreground=%v want %v", dark, selected.GetBold(), selected.GetForeground(), themeAccent)
		}
		inactive := interactiveStyle(false)
		if inactive.GetBold() || inactive.GetForeground() != themeMuted {
			t.Fatalf("dark=%v inactive style: bold=%v foreground=%v want %v", dark, inactive.GetBold(), inactive.GetForeground(), themeMuted)
		}
		primary := primaryActionStyle()
		if !primary.GetBold() || primary.GetForeground() != themeAccent {
			t.Fatalf("dark=%v primary style: bold=%v foreground=%v want %v", dark, primary.GetBold(), primary.GetForeground(), themeAccent)
		}
	}
}

func TestViewDeclaresConfiguredAlternateScreen(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.alternateScreen = true
	if view := m.View(); !view.AltScreen {
		t.Fatal("view did not request the configured alternate screen")
	}
}

func TestViewportPageKeyScrollsHistory(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	fillHistory(m, 20)
	_ = m.View()
	bottom := m.viewport.YOffset()
	gotModel, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if gotModel.(*model).viewport.YOffset() >= bottom {
		t.Fatalf("PageUp did not scroll transcript: %d >= %d", gotModel.(*model).viewport.YOffset(), bottom)
	}
}

func TestViewAutoScrollsOnlyWhenAlreadyAtBottom(t *testing.T) {
	m, _, _ := newTestModel(t)
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12}); cmd != nil {
		// no-op
	}

	fillHistory(m, 20)
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should start at bottom after initial render")
	}

	m.appendBlock(chatBlock{kind: blockAssistant, content: "tail while following"})
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should stay pinned to bottom while following output")
	}

	m.viewport.PageUp()
	offset := m.viewport.YOffset()
	if offset == 0 {
		t.Fatal("expected viewport to move after PageUp")
	}

	m.appendBlock(chatBlock{kind: blockAssistant, content: "tail while reading history"})
	_ = m.View()
	if m.viewport.YOffset() != offset {
		t.Fatalf("YOffset = %d, want %d while reading history", m.viewport.YOffset(), offset)
	}
}

func TestViewportKeepsFollowingWhenBottomPanelChangesHeight(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	fillHistory(m, 40)
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should start at bottom")
	}

	m.latestPlan = &planning.Artifact{
		ID: "plan-follow", SessionID: m.sessionID, Version: 1, Title: "Follow",
		Markdown: "## Summary\n\nlatest plan content", Status: planning.ArtifactProposed,
	}
	m.planHandoff = &planHandoffState{}
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Fatal("opening plan handoff lost bottom-follow state")
	}

	m.planHandoff = nil
	_ = m.View()
	m.viewport.PageUp()
	offset := m.viewport.YOffset()
	m.planHandoff = &planHandoffState{}
	_ = m.View()
	if m.viewport.YOffset() != offset {
		t.Fatalf("opening handoff moved history reader: got %d want %d", m.viewport.YOffset(), offset)
	}
}
