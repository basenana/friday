package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sessions"
)

func TestAlternateScreenMode(t *testing.T) {
	t.Setenv("ZELLIJ", "")
	if !useAlternateScreen("auto") || !useAlternateScreen("always") || useAlternateScreen("never") {
		t.Fatal("unexpected alternate-screen mode selection")
	}
	t.Setenv("ZELLIJ", "1")
	if useAlternateScreen("auto") {
		t.Fatal("auto should preserve Zellij scrollback")
	}
}

func TestTerminalSafeStripsControlSequences(t *testing.T) {
	got := terminalSafe("ok\x1b[31mred\x1b[0m\x00")
	if got != "okred" {
		t.Fatalf("terminalSafe = %q", got)
	}
}

func TestSlashPopupFiltersAndCompletes(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.textarea.SetValue("/rev")
	m.refreshMenu()
	if m.menu.mode != menuCommands || len(m.menu.items) != 1 || m.menu.items[0].label != "/review" {
		t.Fatalf("unexpected menu: %#v", m.menu)
	}
	m.acceptMenuSelection(false)
	if got := m.textarea.Value(); got != "/review " {
		t.Fatalf("completion = %q", got)
	}
}

func TestTabQueuesWhileRunning(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.running = true
	m.textarea.SetValue("follow up")
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyTab})
	model := got.(*model)
	if len(model.queued) != 1 || model.queued[0].text != "follow up" {
		t.Fatalf("queue = %#v", model.queued)
	}
	if model.textarea.Value() != "" {
		t.Fatal("composer was not cleared")
	}
}

func TestCtrlJInsertsComposerNewline(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.textarea.SetValue("first")
	m.textarea.CursorEnd()
	got, _ := m.updateKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if value := got.(*model).textarea.Value(); value != "first\n" {
		t.Fatalf("composer value = %q", value)
	}
}

func TestCardPatchAndDismiss(t *testing.T) {
	m, _, _ := newTestModel(t)
	emit := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardEmitted).WithPayload(events.CardEmittedBody{
		CardID: "card-123456", Kind: "plan", Title: "Plan",
		Component: map[string]any{"steps": []any{map[string]any{"title": "one", "status": "pending"}}},
	})
	m.handleActorEvent(emit)
	update := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardUpdated).WithPayload(events.CardUpdatedBody{
		CardID: "card-123456", Patch: []interface{}{map[string]any{
			"op": "replace", "path": "/component/steps/0/status", "value": "done",
		}},
	})
	m.handleActorEvent(update)
	steps := m.cards["card-123456"].document["component"].(map[string]any)["steps"].([]any)
	if got := steps[0].(map[string]any)["status"]; got != "done" {
		t.Fatalf("patched status = %v", got)
	}
	dismiss := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardDismissed).WithPayload(events.CardDismissedBody{CardID: "card-123456"})
	m.handleActorEvent(dismiss)
	if !m.cards["card-123456"].dismissed || m.renderCard(m.cards["card-123456"]) != "" {
		t.Fatal("dismissed card remains visible")
	}
}

func TestCardPathRejectsTraversalAndEscapingSymlink(t *testing.T) {
	m, _, _ := newTestModel(t)
	root := t.TempDir()
	outside := t.TempDir()
	m.workdir = root
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.safePath("../secret.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.safePath("link"); err == nil {
		t.Fatal("expected escaping symlink rejection")
	}
}

func TestFormAllValueFamilies(t *testing.T) {
	m, _, _ := newTestModel(t)
	raw := map[string]any{"fields": []any{
		map[string]any{"name": "name", "type": "text", "required": true},
		map[string]any{"name": "count", "type": "number", "min": 1},
		map[string]any{"name": "enabled", "type": "boolean"},
		map[string]any{"name": "choice", "type": "select", "options": []any{map[string]any{"label": "A", "value": "a"}}},
		map[string]any{"name": "many", "type": "multiselect", "options": []any{map[string]any{"label": "A", "value": "a"}}},
		map[string]any{"name": "when", "type": "date"},
		map[string]any{"name": "items", "type": "list"},
		map[string]any{"name": "object", "type": "object"},
	}}
	f, err := newFormState("form", raw, 80)
	if err != nil {
		t.Fatal(err)
	}
	f.fields[0].text = "Friday"
	f.fields[1].text = "2"
	f.fields[2].boolean = true
	f.fields[4].multi[0] = true
	f.fields[5].text = "2026-09-10"
	f.fields[6].text = `[1,2]`
	f.fields[7].text = `{"ok":true}`
	values := map[string]any{}
	for i := range f.fields {
		value, err := m.formValue(&f.fields[i])
		if err != nil {
			t.Fatalf("field %d: %v", i, err)
		}
		values[f.fields[i].schema.Name] = value
	}
	if !reflect.DeepEqual(values["items"], []any{float64(1), float64(2)}) || values["choice"] != "a" {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestEventLogRestoresTranscript(t *testing.T) {
	m, _, _ := newTestModel(t)
	store := m.sessMgr.GetStore().(sessions.EventStore)
	sink, err := store.OpenEventSink(context.Background(), m.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	eventsToWrite := []events.Event{
		events.NewEvent(events.KindRunStarted, "turn"),
		events.NewEvent(events.KindCustom, "turn").WithName(events.CustomInputAccepted).WithPayload(events.InputAcceptedBody{TurnID: "turn", Text: "hello"}),
		events.NewEvent(events.KindTextMessageStart, "turn"),
		events.NewEvent(events.KindTextMessageContent, "turn").WithPayload(events.TextMessageContentData{Content: "world"}),
		events.NewEvent(events.KindTextMessageEnd, "turn"),
		events.NewEvent(events.KindRunFinished, "turn").WithPayload(events.RunFinishedData{StopReason: "end_turn"}),
	}
	for _, evt := range eventsToWrite {
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
	if len(m.messages) != 2 || m.messages[0].kind != blockUser || m.messages[1].content != "world" {
		t.Fatalf("restored messages = %#v", m.messages)
	}
}

func TestJSONGridSourceReturnsDecodedRows(t *testing.T) {
	m, _, _ := newTestModel(t)
	root := t.TempDir()
	m.workdir = root
	path := filepath.Join(root, "rows.json")
	if err := os.WriteFile(path, []byte(`[{"name":"Ada"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := m.loadGridSource(map[string]any{"path": path, "format": "json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].(map[string]any)["name"] != "Ada" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestCardSourceLoadsOutsideView(t *testing.T) {
	m, _, _ := newTestModel(t)
	root := t.TempDir()
	m.workdir = root
	path := filepath.Join(root, "rows.json")
	if err := os.WriteFile(path, []byte(`[{"name":"Ada"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	evt := events.NewEvent(events.KindCustom, "run").WithName(events.CustomCardEmitted).WithPayload(events.CardEmittedBody{
		CardID: "table-1", Kind: "table", Component: map[string]any{
			"columns": []any{map[string]any{"key": "name", "label": "Name"}},
			"source":  map[string]any{"path": path, "format": "json"},
		},
	})
	cmd := m.handleActorEvent(evt)
	if cmd == nil || !m.cards["table-1"].sourceLoading {
		t.Fatal("source load was not scheduled")
	}
	if got := m.renderCard(m.cards["table-1"]); !strings.Contains(got, "loading source") {
		t.Fatalf("render before load = %q", got)
	}
	msg := cmd()
	m.Update(msg)
	if got := m.renderCard(m.cards["table-1"]); !strings.Contains(got, "Ada") {
		t.Fatalf("render after load = %q", got)
	}
}

func TestUntrustedPopupAndStreamingTextIsTerminalSafe(t *testing.T) {
	m, _, _ := newTestModel(t)
	osc := "\x1b]52;c;UE9XTkVE\x07"
	m.reasonBuf.WriteString("reason" + osc)
	m.toolOrder = []string{"tool"}
	m.toolCalls["tool"] = &toolCallBlock{name: "name" + osc, input: "args" + osc}
	if got := m.renderStreaming(); strings.Contains(got, "UE9XTkVE") || strings.ContainsRune(got, '\a') {
		t.Fatalf("unsafe streaming output = %q", got)
	}
	f, err := newFormState("form", map[string]any{
		"title": osc, "description": osc,
		"fields": []any{map[string]any{"name": "field", "label": osc, "type": "text", "placeholder": osc, "default": osc}},
	}, 80)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.View(80); strings.Contains(got, "UE9XTkVE") || strings.ContainsRune(got, '\a') {
		t.Fatalf("unsafe form output = %q", got)
	}
	if got := (&openConfirmation{target: osc}).View(80); strings.Contains(got, "UE9XTkVE") || strings.ContainsRune(got, '\a') {
		t.Fatalf("unsafe confirmation output = %q", got)
	}
}

func TestFormSubmissionFailureRetainsValuesForRetry(t *testing.T) {
	m, _, _ := newTestModel(t)
	f, err := newFormState("form-1", map[string]any{
		"fields": []any{map[string]any{"name": "answer", "type": "text"}},
	}, 80)
	if err != nil {
		t.Fatal(err)
	}
	f.editor.SetValue("keep me")
	m.form = f
	m.submitForm()
	if m.form == nil || !m.form.submitting {
		t.Fatal("form was removed before submission acknowledgement")
	}
	drop := events.NewEvent(events.KindCustom, "").WithName("status.inbox_dropped").WithPayload(map[string]any{
		"form_id": "form-1", "reason": "actor stopped",
	})
	m.handleActorEvent(drop)
	if m.form == nil || m.form.submitting || m.form.fields[0].text != "keep me" {
		t.Fatalf("form after failure = %#v", m.form)
	}
}

func TestNestedObjectFormValidation(t *testing.T) {
	m, _, _ := newTestModel(t)
	f, err := newFormState("form", map[string]any{"fields": []any{map[string]any{
		"name": "sample", "type": "object", "fields": []any{map[string]any{
			"name": "count", "type": "number", "required": true, "min": 1,
		}},
	}}}, 80)
	if err != nil {
		t.Fatal(err)
	}
	f.fields[0].text = `{"count":0}`
	if _, err := m.formValue(&f.fields[0]); err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("nested validation error = %v", err)
	}
}

func TestMarkdownRendererIsReusedAtSameWidth(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width = 80
	_ = m.markdown("one")
	first := m.markdownRenderer
	_ = m.markdown("two")
	if first == nil || m.markdownRenderer != first {
		t.Fatal("markdown renderer was rebuilt without a width change")
	}
}
