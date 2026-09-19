package tui

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/core/actor/events"
)

func registerTestAgent(m *model, name, description string) {
	m.agentRegistry.Register(&coderagents.AgentSpec{Name: name, Description: description, SystemPrompt: "test prompt"})
}

func TestSlashPopupIncludesAgentsAndAgentHidesConflictingSkill(t *testing.T) {
	m, _, _ := newTestModel(t)
	registerTestAgent(m, "writer", "Specialized writer")
	registerTestAgent(m, "status", "Hidden slash route")
	writeTestSkill(t, m, "writer", "writer", "Conflicting skill", "skill prompt")

	m.textarea.SetValue("/")
	m.refreshMenu()
	counts := make(map[string]int)
	descriptions := make(map[string]string)
	for _, item := range m.menu.items {
		counts[item.label]++
		descriptions[item.label] = item.description
	}
	if counts["/writer"] != 1 || descriptions["/writer"] != "Specialized writer" {
		t.Fatalf("agent completion missing or shadowed: %#v", m.menu.items)
	}
	if counts["/status"] != 1 || descriptions["/status"] == "Hidden slash route" {
		t.Fatalf("built-in should own /status: %#v", m.menu.items)
	}
}

func TestSlashAgentNamePrefixFiltering(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "case insensitive prefix", input: "/WRI", want: []string{"/writer"}},
		{name: "middle substring", input: "/riter", want: nil},
		{name: "description", input: "/specialized", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			registerTestAgent(m, "writer", "Specialized author")
			m.textarea.SetValue(tt.input)
			m.refreshMenu()
			if got := menuLabels(m.menu.items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("labels for %q = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSlashAgentNamePrefixRanksBeforeBuiltinAlias(t *testing.T) {
	m, _, _ := newTestModel(t)
	registerTestAgent(m, "editor", "Edit text")
	m.textarea.SetValue("/e")
	m.refreshMenu()
	agent := menuLabelIndex(m.menu.items, "/editor")
	alias := menuLabelIndex(m.menu.items, "/quit")
	if agent < 0 || alias < 0 || agent >= alias {
		t.Fatalf("agent name prefix must precede builtin alias: %v", menuLabels(m.menu.items))
	}
}

func TestSlashAgentPublishesOneTurnRouteMetadata(t *testing.T) {
	m, _, _ := newTestModel(t)
	registerTestAgent(m, "writer", "Specialized writer")

	inbox := make(chan bus.UserTextInput, 1)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name != bus.InboxUserText {
			return
		}
		var input bus.UserTextInput
		if events.DecodePayload(env.Event, &input) == nil {
			inbox <- input
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)

	m.textarea.SetValue("/writer preserve   spacing")
	got, cmd := m.submitComposer()
	m = got.(*model)
	flushDispatch(t, m, cmd)
	select {
	case input := <-inbox:
		if input.Text != "preserve   spacing" || input.DisplayText != "/writer preserve   spacing" {
			t.Fatalf("unexpected input: %#v", input)
		}
		if input.Metadata[coderagents.RouteMetadataKey] != "writer" {
			t.Fatalf("route metadata = %#v", input.Metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("agent input was not published")
	}
}

func TestRunningSlashAgentQueuesAndRoutesWhenDispatched(t *testing.T) {
	m, _, _ := newTestModel(t)
	registerTestAgent(m, "writer", "Specialized writer")
	m.running = true
	m.textarea.SetValue("/writer queued task")
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(*model)
	if len(m.queued) != 1 || m.queued[0].text != "/writer queued task" {
		t.Fatalf("queued input = %#v", m.queued)
	}

	inbox := make(chan bus.UserTextInput, 1)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name != bus.InboxUserText {
			return
		}
		var input bus.UserTextInput
		if events.DecodePayload(env.Event, &input) == nil {
			inbox <- input
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)
	m.running = false
	got, cmd := m.dispatchNextQueued()
	m = got.(*model)
	flushDispatch(t, m, cmd)
	select {
	case input := <-inbox:
		if input.Text != "queued task" || input.Metadata[coderagents.RouteMetadataKey] != "writer" {
			t.Fatalf("unexpected queued agent input: %#v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("queued agent input was not published")
	}
}

func TestSlashAgentRequiresTask(t *testing.T) {
	m, _, _ := newTestModel(t)
	registerTestAgent(m, "writer", "Specialized writer")
	got, _ := m.handleSlash("/writer")
	m = got.(*model)
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].content != "usage: /writer <task>" {
		t.Fatalf("messages = %#v", m.messages)
	}
}
