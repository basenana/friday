package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/session"
)

func TestTUILoopCommandSeedsRecordsAndIdleEscCancels(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.loopManager.Close()
	m.loopManager = coderloop.NewManager(eventbus.NewBus())
	defer m.loopManager.Close()

	got, cmd := m.handleSlash("/loop implement the requested feature")
	m = got.(*model)
	if cmd == nil {
		t.Fatal("/loop returned no command")
	}
	lifecycle, ok := m.registry.Lifecycle(m.sessionID)
	if !ok || lifecycle.Current() == nil {
		t.Fatal("active lifecycle unavailable")
	}
	sess := lifecycle.Current()
	note, err := sess.ReadRecord(context.Background(), coderloop.WorkingNoteNamespace)
	if err != nil || !strings.Contains(string(note), "implement the requested feature") {
		t.Fatalf("working note = %q, err = %v", note, err)
	}
	state := readLoopState(t, sess)
	if state != string(coderloop.StateActive) {
		t.Fatalf("loop state = %q", state)
	}

	// There is a small interval before RUN_STARTED reaches the TUI. Esc must
	// still cancel an active Loop during that interval.
	m.running = false
	got, _ = m.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = got.(*model)
	if state := readLoopState(t, sess); state != string(coderloop.StateCancelled) {
		t.Fatalf("state after Esc = %q", state)
	}
}

func TestTUILoopRejectedInPlanMode(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.mode = "plan"
	before := len(m.messages)
	got, _ := m.handleSlash("/loop implement it")
	m = got.(*model)
	if len(m.messages) != before+1 || !strings.Contains(m.messages[len(m.messages)-1].content, "/plan off") {
		t.Fatalf("plan-mode response = %#v", m.messages[before:])
	}
	lifecycle, _ := m.registry.Lifecycle(m.sessionID)
	if _, err := lifecycle.Current().ReadRecord(context.Background(), coderloop.StateNamespace); err == nil {
		t.Fatal("Plan Mode unexpectedly created loop state")
	}
}

func TestTUITabDuringLoopPublishesNormalActorInput(t *testing.T) {
	m, _, _ := newTestModel(t)
	lifecycle, _ := m.registry.Lifecycle(m.sessionID)
	sess := lifecycle.Current()
	if err := sess.UpdateRecord(context.Background(), coderloop.StateNamespace, func([]byte) ([]byte, error) {
		return []byte(coderloop.StateActive), nil
	}); err != nil {
		t.Fatal(err)
	}

	inbox := make(chan bus.Envelope, 1)
	id := m.registry.Bus().SubscribeSerial([]string{bus.TopicInbox(m.sessionID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inbox <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer m.registry.Bus().Unsubscribe(id)

	m.running = true
	m.textarea.SetValue("queued user correction")
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = got.(*model)
	if len(m.queued) != 0 {
		t.Fatalf("input remained in TUI queue: %#v", m.queued)
	}
	select {
	case env := <-inbox:
		var body bus.UserTextInput
		if err := events.DecodePayload(env.Event, &body); err != nil {
			t.Fatal(err)
		}
		if body.Text != "queued user correction" || body.Delivery != bus.DeliveryNormal {
			t.Fatalf("actor input = %+v", body)
		}
	case <-time.After(time.Second):
		t.Fatal("Tab input was not published to the Actor inbox")
	}

	_, _ = m.loopManager.Cancel(context.Background(), sess)
	m.registry.Bus().Publish(bus.TopicPreempt(m.sessionID),
		bus.NewScopedPreempt(m.sessionID, "test", "cleanup", bus.PreemptCurrent))
}

func TestTUIHidesLoopDriverPrompt(t *testing.T) {
	m, _, _ := newTestModel(t)
	before := len(m.messages)
	evt := events.NewEvent(events.KindCustom, "loop-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{
			TurnID: "loop-run", Text: coderloop.BootstrapPrompt, Sources: []string{"loop"},
		})
	m.handleActorEvent(evt)
	if len(m.messages) != before {
		t.Fatalf("internal Loop prompt was rendered: %#v", m.messages[before:])
	}

	userEvt := events.NewEvent(events.KindCustom, "user-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{
			TurnID: "user-run", Text: "visible correction", Sources: []string{"user.local"},
		})
	m.handleActorEvent(userEvt)
	if len(m.messages) != before+1 || m.messages[before].content != "visible correction" {
		t.Fatalf("user input was not rendered: %#v", m.messages[before:])
	}
}

func TestTUIHidesIntermediateLoopOutputAndRevealsFinalSummary(t *testing.T) {
	m, _, _ := newTestModel(t)
	before := len(m.messages)
	m.handleActorEvent(events.NewEvent(events.KindRunStarted, "phase-run"))
	m.handleActorEvent(events.NewEvent(events.KindCustom, "phase-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{TurnID: "phase-run", Text: coderloop.SelectPrompt, Sources: []string{"loop"}}))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageStart, "phase-run"))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageContent, "phase-run").
		WithPayload(events.TextMessageContentData{Content: "internal progress"}))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageEnd, "phase-run"))
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "phase-run").
		WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	if len(m.messages) != before {
		t.Fatalf("intermediate Loop output was rendered: %#v", m.messages[before:])
	}

	m.handleActorEvent(events.NewEvent(events.KindRunStarted, "final-run"))
	m.handleActorEvent(events.NewEvent(events.KindCustom, "final-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{TurnID: "final-run", Text: coderloop.UpdatePrompt, Sources: []string{"loop"}}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallStart, "final-run").
		WithPayload(events.ToolCallStartData{ToolCallID: "finish-call", ToolName: "finish_loop"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallResult, "final-run").
		WithPayload(events.ToolCallResultData{ToolCallID: "finish-call", Success: true}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallStart, "final-run").
		WithPayload(events.ToolCallStartData{ToolCallID: "note-call", ToolName: "working_note_replace"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallArgs, "final-run").
		WithPayload(events.ToolCallArgsData{ToolCallID: "note-call", PartialJSON: `{"content":"done"}`}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallResult, "final-run").
		WithPayload(events.ToolCallResultData{ToolCallID: "note-call", Success: true, Output: "updated"}))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageStart, "final-run"))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageContent, "final-run").
		WithPayload(events.TextMessageContentData{Content: "final result"}))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageEnd, "final-run"))
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "final-run").
		WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	if len(m.messages) != before+2 {
		t.Fatalf("final Loop output = %#v", m.messages[before:])
	}
	if m.messages[before].kind != blockAssistant || m.messages[before].content != "final result" {
		t.Fatalf("final summary = %#v", m.messages[before])
	}
	if m.messages[before+1].kind != blockDivider || !strings.Contains(m.messages[before+1].content, "completed") {
		t.Fatalf("completion marker = %#v", m.messages[before+1])
	}
}

func readLoopState(t *testing.T, sess *session.Session) string {
	t.Helper()
	raw, err := sess.ReadRecord(context.Background(), coderloop.StateNamespace)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}
