package loop

import (
	"context"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/session"
)

func TestManagerAdvancesByInputCausalityWithoutLoopMetadata(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 4)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	first := waitInput(t, inputs)
	var firstBody bus.UserTextInput
	if err := events.DecodePayload(first.Event, &firstBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Text != BootstrapPrompt || firstBody.TurnID != "" || len(firstBody.Metadata) != 0 {
		t.Fatalf("bootstrap input = %+v", firstBody)
	}
	publishFinished(b, sess.ID, first.ID, "end_turn")
	second := waitInput(t, inputs)
	var secondBody bus.UserTextInput
	_ = events.DecodePayload(second.Event, &secondBody)
	if secondBody.Text != SelectPrompt {
		t.Fatalf("next prompt = %q", secondBody.Text)
	}

	if err := writeState(context.Background(), sess, StateCompleted); err != nil {
		t.Fatal(err)
	}
	publishFinished(b, sess.ID, second.ID, "end_turn")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, _ := readState(context.Background(), sess)
		if state == StateCompleted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("loop did not complete after finishing turn")
}

func TestManagerCancelPublishesTargetedInputCancellation(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 4)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		inputs <- env
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	prompt := waitInput(t, inputs)
	cancelled, err := m.Cancel(context.Background(), sess)
	if err != nil || !cancelled {
		t.Fatalf("Cancel() = %v, %v", cancelled, err)
	}
	cancel := waitInput(t, inputs)
	if cancel.Name != bus.InboxCancelInput {
		t.Fatalf("cancel event name = %q", cancel.Name)
	}
	var body bus.CancelInput
	if err := events.DecodePayload(cancel.Event, &body); err != nil {
		t.Fatal(err)
	}
	if body.EventID != prompt.ID {
		t.Fatalf("cancel target = %q, want %q", body.EventID, prompt.ID)
	}
	if state, err := readState(context.Background(), sess); err != nil || state != StateCancelled {
		t.Fatalf("state = %q, err = %v", state, err)
	}
}

func TestManagerAttachRecoversActiveLoop(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	if err := writeState(context.Background(), sess, StateActive); err != nil {
		t.Fatal(err)
	}
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Attach(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	assertInputPrompt(t, waitInput(t, inputs), RecoveryPrompt)
}

func TestManagerAttachDoesNotResumeLegacyFinishingLoop(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	if err := writeState(context.Background(), sess, StateFinishing); err != nil {
		t.Fatal(err)
	}
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Attach(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	assertNoInput(t, inputs)
	waitState(t, sess, StateFinishing)
}

func TestManagerAttachDoesNotResumeTerminalStates(t *testing.T) {
	for _, terminal := range []State{StateCompleted, StateCancelled, StateFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			b := eventbus.NewBus()
			m := NewManager(b)
			defer m.Close()
			sess := session.New("root", nil)
			if err := writeState(context.Background(), sess, terminal); err != nil {
				t.Fatal(err)
			}
			inputs := make(chan bus.Envelope, 1)
			id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
				if env.Name == bus.InboxUserText {
					inputs <- env
				}
			}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
			defer b.Unsubscribe(id)

			if err := m.Attach(context.Background(), sess); err != nil {
				t.Fatal(err)
			}
			assertNoInput(t, inputs)
			waitState(t, sess, terminal)
		})
	}
}

func TestManagerCancelledTurnEndsWithoutRecovery(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 2)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	bootstrap := waitInput(t, inputs)
	publishFinished(b, sess.ID, bootstrap.ID, "cancelled")
	waitState(t, sess, StateCancelled)
	assertNoInput(t, inputs)
}

func TestManagerErrorTurnEndsWithoutRecovery(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 2)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		inputs <- env
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	bootstrap := waitInput(t, inputs)
	publishFinished(b, sess.ID, bootstrap.ID, "error")
	waitState(t, sess, StateFailed)
	assertNoInput(t, inputs)
}

func TestManagerAttachResumesSuspendedLoop(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	if err := writeState(context.Background(), sess, StateSuspended); err != nil {
		t.Fatal(err)
	}
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Attach(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	assertInputPrompt(t, waitInput(t, inputs), RecoveryPrompt)
	waitState(t, sess, StateActive)
}

func TestManagerDetachSuspendsActiveLoop(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	m.Detach(sess.ID)
	waitState(t, sess, StateSuspended)
}

func assertInputPrompt(t *testing.T, env bus.Envelope, want string) {
	t.Helper()
	var body bus.UserTextInput
	if err := events.DecodePayload(env.Event, &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != want {
		t.Fatalf("input prompt = %q, want %q", body.Text, want)
	}
	if body.TurnID != "" || len(body.Metadata) != 0 {
		t.Fatalf("loop input unexpectedly carries turn metadata: %+v", body)
	}
}

func waitState(t *testing.T, sess *session.Session, want State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := readState(context.Background(), sess)
		if err != nil {
			t.Fatal(err)
		}
		if state == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	state, err := readState(context.Background(), sess)
	t.Fatalf("state = %q, want %q (err=%v)", state, want, err)
}

func waitInput(t *testing.T, ch <-chan bus.Envelope) bus.Envelope {
	t.Helper()
	select {
	case env := <-ch:
		return env
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for loop input")
		return bus.Envelope{}
	}
}

func assertNoInput(t *testing.T, ch <-chan bus.Envelope) {
	t.Helper()
	select {
	case env := <-ch:
		t.Fatalf("unexpected loop input %q", env.Name)
	case <-time.After(50 * time.Millisecond):
	}
}

func publishFinished(b *eventbus.Bus, sessionID, cause, reason string) {
	evt := events.NewEvent(events.KindRunFinished, "actor-run").WithPayload(events.RunFinishedData{StopReason: reason})
	evt.CausedBy = []string{cause}
	b.Publish(bus.TopicRun(sessionID, "finished"), bus.Envelope{Event: evt, Session: sessionID, Topic: bus.TopicRun(sessionID, "finished")})
}
