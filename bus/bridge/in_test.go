package bridge

import (
	"context"
	"strings"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/api"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type formBridgeAgent struct{ result chan *tools.Result }

func (a formBridgeAgent) Chat(ctx context.Context, req *api.Request) *api.Response {
	resp := api.NewResponse()
	go func() {
		defer resp.Close()
		for _, tool := range req.Tools {
			if tool.Name != "request_form" {
				continue
			}
			result, _ := tool.Handler(ctx, &tools.Request{Arguments: map[string]any{"schema": map[string]any{
				"fields": []any{map[string]any{"name": "answer", "type": "text"}},
			}}})
			a.result <- result
			return
		}
	}()
	return resp
}

func TestInBridgeReportsActorInboxSaturation(t *testing.T) {
	b := eventbus.NewBus()
	a := coreactor.New(nil, nil, coreactor.WithInboxBuffer(1))
	ib := NewInBridge(b, "s1", a)
	drops := make(chan bus.InboxDropped, 1)
	statusID := b.SubscribeSerial([]string{bus.TopicStatus("s1", bus.StatusInboxDropped)}, func(env bus.Envelope) {
		var drop bus.InboxDropped
		if events.DecodePayload(env.Event, &drop) == nil {
			drops <- drop
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})

	b.Publish(bus.TopicInbox("s1"), bus.NewUserInput("s1", "test", bus.UserTextInput{Text: "first", TurnID: "turn-1"}))
	b.Publish(bus.TopicInbox("s1"), bus.NewUserInput("s1", "test", bus.UserTextInput{Text: "second", TurnID: "turn-2"}))

	select {
	case drop := <-drops:
		if drop.TurnID != "turn-2" || drop.From != "test" || drop.Reason == "" {
			t.Fatalf("unexpected drop: %+v", drop)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbox saturation status")
	}

	ib.Close()
	b.Unsubscribe(statusID)
	b.Wait()
	a.Stop()
}

func TestInBridgeRejectsUnknownInputDelivery(t *testing.T) {
	b := eventbus.NewBus()
	a := coreactor.New(nil, nil)
	ib := NewInBridge(b, "s1", a)
	drops := make(chan bus.InboxDropped, 1)
	id := b.SubscribeSerial([]string{bus.TopicStatus("s1", bus.StatusInboxDropped)}, func(env bus.Envelope) {
		var drop bus.InboxDropped
		if events.DecodePayload(env.Event, &drop) == nil {
			drops <- drop
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	b.Publish(bus.TopicInbox("s1"), bus.NewUserInput("s1", "test", bus.UserTextInput{
		Text: "hello", TurnID: "turn", Delivery: bus.InputDelivery("later"),
	}))
	select {
	case drop := <-drops:
		if !strings.Contains(drop.Reason, "unknown input delivery") {
			t.Fatalf("drop = %+v", drop)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery rejection")
	}
	ib.Close()
	b.Unsubscribe(id)
	b.Wait()
	a.Stop()
}

func TestInBridgeSubmitsFormWhileTurnIsRunning(t *testing.T) {
	b := eventbus.NewBus()
	results := make(chan *tools.Result, 1)
	a := coreactor.New(formBridgeAgent{result: results}, coresession.New("s1", nil))
	sub := a.Subscribe()
	a.Start(context.Background())
	ib := NewInBridge(b, "s1", a)
	b.Publish(bus.TopicInbox("s1"), bus.NewUserInput("s1", "test", bus.UserTextInput{Text: "ask"}))
	var formID string
	deadline := time.After(2 * time.Second)
	for formID == "" {
		select {
		case evt := <-sub.Events():
			if evt.Name == events.CustomFormRequested {
				var body events.FormRequestedBody
				if err := events.DecodePayload(evt, &body); err != nil {
					t.Fatal(err)
				}
				formID = body.FormID
			}
		case <-deadline:
			t.Fatal("timed out waiting for form request")
		}
	}
	b.Publish(bus.TopicInbox("s1"), bus.NewFormSubmit("s1", "test", bus.FormSubmitInput{
		FormID: formID, Values: map[string]any{"answer": "yes"},
	}))
	select {
	case result := <-results:
		if result == nil || result.IsError {
			t.Fatalf("form result = %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("form submission did not unblock the running turn")
	}
	ib.Close()
	b.Wait()
	a.Stop()
}

func TestInBridgeFormDropCarriesFormID(t *testing.T) {
	b := eventbus.NewBus()
	a := coreactor.New(nil, nil)
	ib := NewInBridge(b, "s1", a)
	defer func() {
		ib.Close()
		b.Wait()
		a.Stop()
	}()
	drops := make(chan bus.InboxDropped, 1)
	id := b.SubscribeSerial([]string{bus.TopicStatus("s1", bus.StatusInboxDropped)}, func(env bus.Envelope) {
		var drop bus.InboxDropped
		if events.DecodePayload(env.Event, &drop) == nil {
			drops <- drop
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)
	b.Publish(bus.TopicInbox("s1"), bus.NewFormSubmit("s1", "test", bus.FormSubmitInput{FormID: "form-123"}))
	select {
	case drop := <-drops:
		if drop.FormID != "form-123" {
			t.Fatalf("drop = %+v", drop)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for form drop")
	}
}

func TestInBridgeCloseCancelsBlockedPreempt(t *testing.T) {
	b := eventbus.NewBus()
	a := coreactor.New(nil, nil)
	ib := NewInBridge(b, "s1", a)

	// The actor is not started, so its preempt buffer cannot drain. More than
	// the default capacity forces the bridge dispatcher to block in
	// SendPreempt until Close cancels its bridge-scoped context.
	for i := 0; i < 10; i++ {
		b.Publish(bus.TopicPreempt("s1"), bus.NewPreempt("s1", "test", "cancel"))
	}
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		ib.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("InBridge.Close blocked behind a saturated preempt inbox")
	}
	b.Wait()
	a.Stop()
}
