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

func TestTUIEnterDuringLoopPublishesSteeringInput(t *testing.T) {
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
	m.textarea.SetValue("stop changing the API; preserve compatibility")
	got, _ := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = got.(*model)
	if !m.steeringPending {
		t.Fatal("Enter did not mark immediate steering as pending")
	}
	if state := readLoopState(t, sess); state != string(coderloop.StateCancelled) {
		t.Fatalf("loop state after steer = %q", state)
	}
	select {
	case env := <-inbox:
		var body bus.UserTextInput
		if err := events.DecodePayload(env.Event, &body); err != nil {
			t.Fatal(err)
		}
		if body.Text != "stop changing the API; preserve compatibility" || body.Delivery != bus.DeliverySteer {
			t.Fatalf("steering input = %+v", body)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter input was not published to the Actor inbox")
	}

	_, _ = m.loopManager.Cancel(context.Background(), sess)
}

func TestTUIShowsLoopPhaseButNotDriverPrompt(t *testing.T) {
	m, _, _ := newTestModel(t)
	before := len(m.messages)
	evt := events.NewEvent(events.KindCustom, "loop-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{
			TurnID: "loop-run", Text: coderloop.BootstrapPrompt, Sources: []string{"loop"},
		})
	m.handleActorEvent(evt)
	if len(m.messages) != before+1 || m.messages[before].kind != blockDivider || m.messages[before].content != "loop · bootstrap" {
		t.Fatalf("Loop phase marker = %#v", m.messages[before:])
	}
	if strings.Contains(m.messages[before].content, "Read the Working Note") {
		t.Fatalf("internal Loop prompt was rendered: %#v", m.messages[before:])
	}

	userEvt := events.NewEvent(events.KindCustom, "user-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{
			TurnID: "user-run", Text: "visible correction", Sources: []string{"user.local"},
		})
	m.handleActorEvent(userEvt)
	if len(m.messages) != before+2 || m.messages[before+1].content != "visible correction" {
		t.Fatalf("user input was not rendered: %#v", m.messages[before:])
	}
}

func TestTUIShowsIntermediateLoopReasoningToolsAndOutput(t *testing.T) {
	m, _, _ := newTestModel(t)
	before := len(m.messages)
	m.handleActorEvent(events.NewEvent(events.KindRunStarted, "phase-run"))
	m.handleActorEvent(events.NewEvent(events.KindCustom, "phase-run").
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{TurnID: "phase-run", Text: coderloop.SelectPrompt, Sources: []string{"loop"}}))
	m.handleActorEvent(events.NewEvent(events.KindCustom, "phase-run").
		WithName(events.CustomReasoningDelta).
		WithPayload(events.ReasoningDeltaBody{Content: "inspect the repository"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallStart, "phase-run").
		WithPayload(events.ToolCallStartData{ToolCallID: "read-call", ToolName: "read_file"}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallArgs, "phase-run").
		WithPayload(events.ToolCallArgsData{ToolCallID: "read-call", PartialJSON: `{"path":"main.go"}`}))
	m.handleActorEvent(events.NewEvent(events.KindToolCallResult, "phase-run").
		WithPayload(events.ToolCallResultData{ToolCallID: "read-call", Success: true, Output: "package main"}))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageStart, "phase-run"))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageContent, "phase-run").
		WithPayload(events.TextMessageContentData{Content: "internal progress"}))
	m.handleActorEvent(events.NewEvent(events.KindTextMessageEnd, "phase-run"))
	m.handleActorEvent(events.NewEvent(events.KindRunFinished, "phase-run").
		WithPayload(events.RunFinishedData{StopReason: "end_turn"}))
	got := m.messages[before:]
	if len(got) != 5 {
		t.Fatalf("intermediate Loop output = %#v", got)
	}
	if got[0].kind != blockDivider || got[0].content != "loop · select" {
		t.Fatalf("phase marker = %#v", got[0])
	}
	if got[1].kind != blockReasoning || got[1].content != "inspect the repository" {
		t.Fatalf("reasoning = %#v", got[1])
	}
	if got[2].kind != blockToolCall || got[2].toolName != "read_file" || !strings.Contains(got[2].toolArgs, "main.go") || !strings.Contains(got[2].toolOutput, "package main") {
		t.Fatalf("tool call = %#v", got[2])
	}
	if got[3].kind != blockAssistant || got[3].content != "internal progress" {
		t.Fatalf("assistant output = %#v", got[3])
	}
	if got[4].kind != blockDivider || !strings.Contains(got[4].content, "completed") {
		t.Fatalf("completion marker = %#v", got[4])
	}
}

func TestLoopPhaseLabels(t *testing.T) {
	for _, tt := range []struct {
		prompt string
		want   string
	}{
		{coderloop.BootstrapPrompt, "bootstrap"},
		{coderloop.SelectPrompt, "select"},
		{coderloop.DevelopPrompt, "develop"},
		{coderloop.ReviewPrompt, "review"},
		{coderloop.UpdatePrompt, "update"},
		{coderloop.RecoveryPrompt, "recover"},
		{coderloop.FinalizePrompt, "finalize"},
		{"unknown controller prompt", "turn"},
	} {
		if got := loopPhase(tt.prompt); got != tt.want {
			t.Errorf("loopPhase(%q) = %q, want %q", firstLine(tt.prompt), got, tt.want)
		}
	}
}

func TestEventLogRestoresVisibleLoopActivity(t *testing.T) {
	m, _, store := newTestModel(t)
	sink, err := store.OpenEventSink(context.Background(), m.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	eventsToWrite := []events.Event{
		events.NewEvent(events.KindRunStarted, "loop-replay"),
		events.NewEvent(events.KindCustom, "loop-replay").WithName(events.CustomInputAccepted).
			WithPayload(events.InputAcceptedBody{TurnID: "loop-replay", Text: coderloop.DevelopPrompt, Sources: []string{"loop"}}),
		events.NewEvent(events.KindCustom, "loop-replay").WithName(events.CustomReasoningDelta).
			WithPayload(events.ReasoningDeltaBody{Content: "replayed reasoning"}),
		events.NewEvent(events.KindToolCallStart, "loop-replay").
			WithPayload(events.ToolCallStartData{ToolCallID: "replay-tool", ToolName: "edit_file"}),
		events.NewEvent(events.KindToolCallArgs, "loop-replay").
			WithPayload(events.ToolCallArgsData{ToolCallID: "replay-tool", PartialJSON: `{"path":"main.go"}`}),
		events.NewEvent(events.KindToolCallResult, "loop-replay").
			WithPayload(events.ToolCallResultData{ToolCallID: "replay-tool", Success: true, Output: "updated"}),
		events.NewEvent(events.KindRunFinished, "loop-replay").
			WithPayload(events.RunFinishedData{StopReason: "end_turn"}),
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
	if len(m.messages) != 4 {
		t.Fatalf("restored Loop activity = %#v", m.messages)
	}
	if m.messages[0].content != "loop · develop" || m.messages[1].kind != blockReasoning ||
		m.messages[2].kind != blockToolCall || m.messages[3].kind != blockDivider {
		t.Fatalf("restored Loop activity = %#v", m.messages)
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
