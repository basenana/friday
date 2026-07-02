package tui

import (
	"fmt"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func newTestModel(t *testing.T) (*model, *sessions.Manager, *sessionfile.FileSessionStore) {
	t.Helper()

	baseDir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(baseDir, "sessions"))
	mgr := sessions.NewManager(store, filepath.Join(baseDir, "current"), "test")

	sessionID := "session-initial"
	if _, _, err := mgr.GetOrCreateByID(sessionID); err != nil {
		t.Fatalf("GetOrCreateByID() failed: %v", err)
	}

	registry := actor.NewRegistry(mgr, nil, actor.DefaultRegistryConfig())
	t.Cleanup(registry.ShutdownAll)

	m, err := initialModel(mgr, registry, sessionID)
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

func TestHandleSlashNewCreatesAndSwitchesCurrentSession(t *testing.T) {
	m, mgr, store := newTestModel(t)
	oldID := m.sessionID
	oldToken := m.subscriptionToken

	gotModel, cmd := m.handleSlash("/new")
	if cmd == nil {
		t.Fatal("handleSlash(/new) returned nil cmd")
	}

	got, ok := gotModel.(*model)
	if !ok {
		t.Fatalf("handleSlash(/new) returned %T, want *model", gotModel)
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
}

func TestUpdateIgnoresStaleSubscriptionMessages(t *testing.T) {
	m, _, _ := newTestModel(t)
	oldToken := m.subscriptionToken

	if _, cmd := m.handleSlash("/new"); cmd == nil {
		t.Fatal("handleSlash(/new) returned nil cmd")
	}
	newToken := m.subscriptionToken
	newSessionID := m.sessionID

	m.running = true
	gotModel, _ := m.Update(actorEventMsg{
		token: oldToken,
		event: actor.Event{Type: actor.EventRunFinished},
	})
	got := gotModel.(*model)
	if !got.running {
		t.Fatal("stale actor event changed running state")
	}

	gotModel, _ = got.Update(actorDoneMsg{token: oldToken})
	got = gotModel.(*model)
	if got.subscriptionToken != newToken {
		t.Fatalf("subscription token = %d, want %d", got.subscriptionToken, newToken)
	}
	if got.sessionID != newSessionID {
		t.Fatalf("session ID = %q, want %q", got.sessionID, newSessionID)
	}
}

func TestViewportMouseWheelScrollsHistory(t *testing.T) {
	m, _, _ := newTestModel(t)
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12}); cmd != nil {
		// no-op
	}

	fillHistory(m, 20)
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should start at bottom after initial render")
	}

	bottomOffset := m.viewport.YOffset
	if bottomOffset == 0 {
		t.Fatal("expected scrollable content")
	}

	gotModel, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	got := gotModel.(*model)
	if got.viewport.YOffset >= bottomOffset {
		t.Fatalf("YOffset = %d, want < %d after wheel-up", got.viewport.YOffset, bottomOffset)
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
	offset := m.viewport.YOffset
	if offset == 0 {
		t.Fatal("expected viewport to move after PageUp")
	}

	m.appendBlock(chatBlock{kind: blockAssistant, content: "tail while reading history"})
	_ = m.View()
	if m.viewport.YOffset != offset {
		t.Fatalf("YOffset = %d, want %d while reading history", m.viewport.YOffset, offset)
	}
}
