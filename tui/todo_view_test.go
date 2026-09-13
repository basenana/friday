package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sessions"
)

func makeTodoUpdate(raw string) events.Event {
	return events.NewEvent(events.KindCustom, "run-todo").WithName(events.CustomTodoUpdate).WithPayload(events.CustomData{
		Name: events.CustomTodoUpdate,
		Body: map[string]any{"todo_list": raw},
	})
}

func TestTodoUpdateReplacesAndClearsCurrentList(t *testing.T) {
	m := &model{}
	m.handleTodoUpdate(makeTodoUpdate(`{"todos":[{"description":"first","status":"in_progress"},{"description":"second","status":"pending"}]}`))
	if len(m.todos) != 2 || m.todos[0].Description != "first" {
		t.Fatalf("todos = %#v", m.todos)
	}
	m.handleTodoUpdate(makeTodoUpdate(`{"todos":[{"description":"done","status":"completed"}]}`))
	if len(m.todos) != 1 || m.todos[0].Description != "done" {
		t.Fatalf("replaced todos = %#v", m.todos)
	}
	m.handleTodoUpdate(makeTodoUpdate(`{"todos":[]}`))
	if len(m.todos) != 0 {
		t.Fatalf("cleared todos = %#v", m.todos)
	}
}

func TestInvalidTodoUpdateKeepsLastGoodState(t *testing.T) {
	m := &model{replaying: true, todos: []todoItem{{Description: "keep", Status: "pending"}}}
	m.handleTodoUpdate(makeTodoUpdate(`{"todos":[{"description":"bad","status":"unknown"}]}`))
	if len(m.todos) != 1 || m.todos[0].Description != "keep" {
		t.Fatalf("todos = %#v", m.todos)
	}
}

func TestTodoPanelShowsAllStatusesInInputOrder(t *testing.T) {
	configureTheme(true)
	m := &model{todos: []todoItem{
		{Description: "running", Status: "in_progress"},
		{Description: "waiting", Status: "pending"},
		{Description: "finished", Status: "completed"},
		{Description: "blocked", Status: "blocked"},
	}}
	panel := terminalSafe(m.renderTodoPanel(80))
	for _, want := range []string{"Todos · 1/4", "◐ running", "○ waiting", "✓ finished", "! blocked"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel missing %q: %q", want, panel)
		}
	}
	last := -1
	for _, item := range []string{"running", "waiting", "finished", "blocked"} {
		index := strings.Index(panel, item)
		if index <= last {
			t.Fatalf("todo order changed: %q", panel)
		}
		last = index
	}
}

func TestTodoPanelWrapsWithoutTruncating(t *testing.T) {
	configureTheme(true)
	description := "a todo description that must remain completely visible"
	m := &model{todos: []todoItem{{Description: description, Status: "pending"}}}
	panel := terminalSafe(m.renderTodoPanel(20))
	for _, word := range strings.Fields(description) {
		if !strings.Contains(panel, word) {
			t.Fatalf("wrapped panel omitted %q: %q", word, panel)
		}
	}
	if strings.Contains(panel, "…") || lipgloss.Height(panel) < 3 {
		t.Fatalf("wrapped panel = %q", panel)
	}
}

func TestTodoPanelConsumesFixedLayoutHeight(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 30
	m.layout()
	before := m.viewport.Height()
	m.todos = []todoItem{{Description: "one", Status: "pending"}, {Description: "two", Status: "completed"}}
	wantDelta := lipgloss.Height(m.renderTodoPanel(m.width))
	m.layout()
	if got := before - m.viewport.Height(); got != wantDelta {
		t.Fatalf("viewport height delta = %d, want %d", got, wantDelta)
	}
}

func TestTodoPanelRendersBetweenHistoryAndComposer(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 30
	m.messages = []chatBlock{{kind: blockAssistant, content: "history sentinel"}}
	m.todos = []todoItem{{Description: "todo sentinel", Status: "in_progress"}}
	m.textarea.SetValue("composer sentinel")
	view := terminalSafe(m.View().Content)
	history := strings.Index(view, "history sentinel")
	todo := strings.Index(view, "todo sentinel")
	composer := strings.Index(view, "composer sentinel")
	if history < 0 || todo <= history || composer <= todo {
		t.Fatalf("unexpected layout order: history=%d todo=%d composer=%d\n%s", history, todo, composer, view)
	}
}

func TestProjectionReplacesTodoStateBetweenSessions(t *testing.T) {
	m := &model{todos: []todoItem{{Description: "old session", Status: "pending"}}}
	m.applyProjection(transcriptProjection{todos: []todoItem{{Description: "new session", Status: "in_progress"}}})
	if len(m.todos) != 1 || m.todos[0].Description != "new session" {
		t.Fatalf("projected todos = %#v", m.todos)
	}
	m.applyProjection(transcriptProjection{})
	if len(m.todos) != 0 {
		t.Fatalf("empty projection retained todos = %#v", m.todos)
	}
}

func TestTodoPanelIsTerminalSafe(t *testing.T) {
	configureTheme(true)
	osc := "\x1b]52;c;UE9XTkVE\x07"
	m := &model{todos: []todoItem{{Description: "safe" + osc, Status: "pending"}}}
	panel := terminalSafe(m.renderTodoPanel(80))
	if strings.Contains(panel, "UE9XTkVE") || strings.ContainsRune(panel, '\a') {
		t.Fatalf("unsafe todo panel = %q", panel)
	}
}

func TestEventLogRestoresLatestTodoSnapshot(t *testing.T) {
	m, _, _ := newTestModel(t)
	store := m.sessMgr.GetStore().(sessions.EventStore)
	sink, err := store.OpenEventSink(context.Background(), m.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, evt := range []events.Event{
		makeTodoUpdate(`{"todos":[{"description":"old","status":"pending"}]}`),
		makeTodoUpdate(`{"todos":[{"description":"current","status":"in_progress"},{"description":"done","status":"completed"}]}`),
	} {
		if err := sink.Append(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadTranscript(m.sessionID); err != nil {
		t.Fatal(err)
	}
	if len(m.todos) != 2 || m.todos[0].Description != "current" || m.todos[1].Description != "done" {
		t.Fatalf("restored todos = %#v", m.todos)
	}
}
