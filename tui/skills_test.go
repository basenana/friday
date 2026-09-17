package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

func writeTestSkill(t *testing.T, m *model, dirName, name, description, instructions string) {
	t.Helper()
	dir := filepath.Join(m.cfg.WorkspacePath(), "skills", dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + instructions
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSlashPopupIncludesSkillsAndHidesBuiltinConflicts(t *testing.T) {
	m, _, _ := newTestModel(t)
	writeTestSkill(t, m, "writer", "writer", "Draft release notes", "Write clearly.")
	writeTestSkill(t, m, "status", "status", "Must be hidden", "Do not run.")
	writeTestSkill(t, m, "exit", "exit", "Alias conflict", "Do not run.")

	m.textarea.SetValue("/")
	m.refreshMenu()
	counts := make(map[string]int)
	var writerDescription string
	for _, item := range m.menu.items {
		counts[item.label]++
		if item.label == "/writer" {
			writerDescription = item.description
		}
	}
	if counts["/writer"] != 1 || writerDescription != "Draft release notes" {
		t.Fatalf("writer completion missing or malformed: %#v", m.menu.items)
	}
	if counts["/status"] != 1 {
		t.Fatalf("builtin status should be the only /status item: %#v", m.menu.items)
	}
	if counts["/exit"] != 0 {
		t.Fatalf("skill conflicting with builtin alias was visible: %#v", m.menu.items)
	}

	writeTestSkill(t, m, "late", "late-skill", "Installed while TUI runs", "Late instructions.")
	m.menu = menuState{}
	m.textarea.SetValue("/late")
	m.refreshMenu()
	if len(m.menu.items) != 1 || m.menu.items[0].label != "/late-skill" {
		t.Fatalf("newly installed skill was not refreshed: %#v", m.menu.items)
	}
}

func TestSlashSkillNamePrefixFiltering(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "case insensitive prefix", input: "/WRI", want: []string{"/writer"}},
		{name: "middle substring", input: "/riter", want: nil},
		{name: "description", input: "/drafting", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			writeTestSkill(t, m, "writer", "writer", "Drafting assistant", "Write clearly.")
			m.textarea.SetValue(tt.input)
			m.refreshMenu()
			if got := menuLabels(m.menu.items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("labels for %q = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlashSkillNamePrefixRanksBeforeBuiltinAlias(t *testing.T) {
	m, _, _ := newTestModel(t)
	writeTestSkill(t, m, "editor", "editor", "Edit text", "Edit carefully.")
	m.textarea.SetValue("/e")
	m.refreshMenu()
	skill := menuLabelIndex(m.menu.items, "/editor")
	alias := menuLabelIndex(m.menu.items, "/quit")
	if skill < 0 || alias < 0 || skill >= alias {
		t.Fatalf("skill name prefix must precede builtin alias: %v", menuLabels(m.menu.items))
	}
}

func TestSlashSkillSendsInstructionsAndDisplaysInvocation(t *testing.T) {
	m, _, _ := newTestModel(t)
	writeTestSkill(t, m, "writer", "writer", "Draft text", "Follow the writing workflow.")

	inbox := make(chan bus.Envelope, 1)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inbox <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)

	m.textarea.SetValue("/writer preserve   spacing")
	got, _ := m.submitComposer()
	m = got.(*model)
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].content != "/writer preserve   spacing" {
		t.Fatalf("displayed message = %#v", m.messages)
	}

	select {
	case env := <-inbox:
		var input bus.UserTextInput
		if err := events.DecodePayload(env.Event, &input); err != nil {
			t.Fatal(err)
		}
		if input.Text != "Follow the writing workflow.\n\npreserve   spacing" {
			t.Fatalf("agent payload = %q", input.Text)
		}
		if input.DisplayText != "/writer preserve   spacing" {
			t.Fatalf("display payload = %q", input.DisplayText)
		}
	case <-time.After(time.Second):
		t.Fatal("skill input was not published")
	}
}

func TestRunningSlashSkillQueuesAndRefreshesWhenDispatched(t *testing.T) {
	m, _, _ := newTestModel(t)
	writeTestSkill(t, m, "writer", "writer", "Draft text", "Version one.")
	m.running = true
	m.textarea.SetValue("/writer task")
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(*model)
	if len(m.queued) != 1 || m.queued[0].text != "/writer task" {
		t.Fatalf("queued input = %#v", m.queued)
	}

	writeTestSkill(t, m, "writer", "writer", "Draft text", "Version two.")
	inbox := make(chan bus.Envelope, 1)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inbox <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)
	m.running = false
	got, _ = m.dispatchNextQueued()
	m = got.(*model)

	select {
	case env := <-inbox:
		var input bus.UserTextInput
		if err := events.DecodePayload(env.Event, &input); err != nil {
			t.Fatal(err)
		}
		if input.Text != "Version two.\n\ntask" {
			t.Fatalf("queued skill did not use refreshed instructions: %q", input.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("queued skill input was not published")
	}
}
