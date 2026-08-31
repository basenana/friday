package actor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/types"
)

func TestEventStream_CloseWhilePublishingDoesNotPanic(t *testing.T) {
	stream := NewEventStream(nil)
	subs := make([]*Subscription, 0, 8)
	for i := 0; i < 8; i++ {
		sub := stream.Subscribe(1)
		subs = append(subs, sub)
		go func(ch <-chan events.Event) {
			for range ch {
			}
		}(sub.Events())
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 500; j++ {
				stream.Publish(events.NewEvent(events.KindCustom, "run"))
			}
		}()
	}
	for _, sub := range subs {
		sub := sub
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			sub.Close()
		}()
	}

	close(start)
	wg.Wait()
	stream.Close()
}

func TestInbox_CloseStopsSendsWithoutPanic(t *testing.T) {
	in := NewInbox(1, 1)
	in.Close()

	if err := in.Send(context.Background(), UserTextMessage{Text: "hello"}); !errors.Is(err, errInboxClosed) {
		t.Fatalf("Send err = %v, want errInboxClosed", err)
	}
	if in.TrySend(UserTextMessage{Text: "hello"}) {
		t.Fatalf("TrySend should fail after Close")
	}
	if err := in.SendPreempt(context.Background(), PreemptMessage{Reason: "stop"}); !errors.Is(err, errInboxClosed) {
		t.Fatalf("SendPreempt err = %v, want errInboxClosed", err)
	}
	if msg, ok := in.Wait(context.Background()); ok || msg != nil {
		t.Fatalf("Wait = (%v, %v), want (nil, false)", msg, ok)
	}
}

func TestActor_FormSubmitMessageAsFirstInboxMessage(t *testing.T) {
	a, _ := newTestActor(newMockAgent())
	a.Start(context.Background())
	defer a.Stop()

	waiter := a.prepareFormWait("form-1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	outcomeCh := make(chan FormOutcome, 1)
	errCh := make(chan error, 1)
	go func() {
		outcome, err := a.waitForRegisteredForm(ctx, "form-1", waiter)
		if err != nil {
			errCh <- err
			return
		}
		outcomeCh <- outcome
	}()

	if err := a.Send(context.Background(), FormSubmitMessage{
		FormID: "form-1",
		Values: map[string]any{"organism": "hsapiens"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("waitForRegisteredForm: %v", err)
	case outcome := <-outcomeCh:
		if got, _ := outcome.Values["organism"].(string); got != "hsapiens" {
			t.Fatalf("organism = %q, want hsapiens", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for first form message to route")
	}
}

func TestActor_RequestForm_PreemptCancelsTurnAndForm(t *testing.T) {
	mock := newMockAgent(
		chatScript{
			toolCall: &scriptedToolCall{
				id:   "tool-call-1",
				name: "request_form",
				arguments: map[string]any{
					"schema": map[string]any{
						"title": "Pick one",
						"fields": []map[string]any{
							{
								"name": "organism",
								"type": "select",
								"options": []map[string]any{
									{"label": "Human", "value": "hsapiens"},
								},
							},
						},
					},
				},
			},
			deltas: []types.Delta{{Content: "should not arrive"}},
		},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "ask"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	formID := waitForFormRequested(t, sub)
	if formID == "" {
		t.Fatalf("expected form.requested event")
	}
	if err := a.SendPreempt(context.Background(), "cancel form"); err != nil {
		t.Fatalf("SendPreempt: %v", err)
	}

	seen := collectEvents(t, sub, hasRunFinished)
	var cancelled bool
	var contentChunks int
	for _, e := range seen {
		if e.Type == events.KindCustom && e.Name == events.CustomFormCancelled {
			cancelled = true
		}
		if e.Type == events.KindTextMessageContent {
			contentChunks++
		}
	}
	if !cancelled {
		t.Fatalf("expected form.cancelled after preempt")
	}
	if contentChunks != 0 {
		t.Fatalf("expected no text deltas after preempt, got %d", contentChunks)
	}
}

func TestActor_ToolEventsUseStableIDs(t *testing.T) {
	mock := newMockAgent(
		chatScript{
			toolCall: &scriptedToolCall{
				id:   "tool-call-42",
				name: "emit_card",
				arguments: map[string]any{
					"kind": "file",
					"component": map[string]any{
						"path": "/sandbox/out.txt",
					},
				},
			},
		},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "run"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	seen := collectEvents(t, sub, hasRunFinished)
	var startID, argsID, endID, resultID string
	for _, e := range seen {
		switch e.Type {
		case events.KindToolCallStart:
			startID = e.MessageID
		case events.KindToolCallArgs:
			argsID = e.MessageID
		case events.KindToolCallEnd:
			endID = e.MessageID
		case events.KindToolCallResult:
			resultID = e.MessageID
		}
	}

	for label, got := range map[string]string{
		"start":  startID,
		"args":   argsID,
		"end":    endID,
		"result": resultID,
	} {
		if got != "tool-call-42" {
			t.Fatalf("%s MessageID = %q, want tool-call-42", label, got)
		}
	}
}

func TestActor_ToolsExposeInputSchemas(t *testing.T) {
	a, _ := newTestActor(newMockAgent())

	requiredByTool := map[string][]string{
		"emit_card":    {"kind", "component"},
		"request_form": {"schema"},
		"update_card":  {"card_id", "patch"},
	}

	for _, tool := range a.Tools() {
		want, ok := requiredByTool[tool.Name]
		if !ok {
			continue
		}
		params := tool.GetParameters()
		required, _ := params["required"].([]string)
		for _, field := range want {
			if !containsString(required, field) {
				t.Fatalf("tool %s missing required field %q in schema %#v", tool.Name, field, params)
			}
		}
	}
}

func TestActor_EmitCardRejectsUnsafeFilePath(t *testing.T) {
	a, _ := newTestActor(newMockAgent())
	if _, err := a.EmitCard("file", "bad", map[string]any{"path": "/tmp/out.txt"}); err == nil {
		t.Fatalf("expected unsafe file card path to be rejected")
	}
}

func TestJSONLUsesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	s, err := sink.NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %#o, want 0600", got)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
