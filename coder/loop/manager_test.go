package loop

import (
	"context"
	"errors"
	"sync"
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
	assertPhase(t, sess, phaseBootstrap)
	publishFinished(b, sess.ID, first.ID, "end_turn")
	second := waitInput(t, inputs)
	var secondBody bus.UserTextInput
	_ = events.DecodePayload(second.Event, &secondBody)
	if secondBody.Text != SelectPrompt {
		t.Fatalf("next prompt = %q", secondBody.Text)
	}
	assertPhase(t, sess, phaseSelect)

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
	assertPhase(t, sess, phaseRecovery)
}

func TestManagerPersistsEveryPhaseBeforeDispatch(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 5)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		phase  phase
		prompt string
	}{
		{phaseBootstrap, BootstrapPrompt},
		{phaseSelect, SelectPrompt},
		{phaseDevelop, DevelopPrompt},
		{phaseReview, ReviewPrompt},
		{phaseUpdate, UpdatePrompt},
	} {
		env := waitInput(t, inputs)
		assertInputPrompt(t, env, want.prompt)
		assertPhase(t, sess, want.phase)
		if i < 4 {
			publishFinished(b, sess.ID, env.ID, "end_turn")
		}
	}
}

func TestManagerIdempotentResumeDoesNotOverwriteRunningPhase(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	assertInputPrompt(t, waitInput(t, inputs), BootstrapPrompt)
	assertPhase(t, sess, phaseBootstrap)
	if err := m.Resume(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, sess, phaseBootstrap)
	assertNoInput(t, inputs)
}

type phaseFailingRecordStore struct {
	mu     sync.Mutex
	values map[string][]byte
	err    error
}

func (s *phaseFailingRecordStore) ReadSessionRecord(_ context.Context, sessionID, namespace string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[sessionID+"/"+namespace]
	if !ok {
		return nil, session.ErrRecordNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *phaseFailingRecordStore) UpdateSessionRecord(_ context.Context, sessionID, namespace string, update func([]byte) ([]byte, error)) error {
	if namespace == phaseNamespace {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionID + "/" + namespace
	next, err := update(append([]byte(nil), s.values[key]...))
	if err == nil {
		s.values[key] = append([]byte(nil), next...)
	}
	return err
}

func TestManagerDoesNotDispatchWhenRecoveryPhaseCannotBePersisted(t *testing.T) {
	wantErr := errors.New("phase disk unavailable")
	store := &phaseFailingRecordStore{values: make(map[string][]byte), err: wantErr}
	sess := session.New("root", nil, session.WithRecordStore(store))
	if err := writeState(context.Background(), sess, StateActive); err != nil {
		t.Fatal(err)
	}
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	err := m.Resume(context.Background(), sess)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Resume() error = %v, want %v", err, wantErr)
	}
	assertNoInput(t, inputs)
	waitState(t, sess, StateSuspended)
}

func TestManagerAttachResumesWithoutReplacingWorkingNote(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	ctx := context.Background()
	wantNote := "# Working Note\n\n## Plan\n\n- preserve completed work\n- continue remaining work"
	if err := sess.UpdateRecord(ctx, WorkingNoteNamespace, func([]byte) ([]byte, error) {
		return []byte(wantNote), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeState(ctx, sess, StateSuspended); err != nil {
		t.Fatal(err)
	}
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Attach(ctx, sess); err != nil {
		t.Fatal(err)
	}
	assertInputPrompt(t, waitInput(t, inputs), RecoveryPrompt)
	assertPhase(t, sess, phaseRecovery)
	got, err := sess.ReadRecord(ctx, WorkingNoteNamespace)
	if err != nil || string(got) != wantNote {
		t.Fatalf("working note after attach = %q, %v; want unchanged", got, err)
	}
}

func TestManagerExplicitStartReplacesSuspendedLoop(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	ctx := context.Background()
	if err := sess.UpdateRecord(ctx, WorkingNoteNamespace, func([]byte) ([]byte, error) {
		return []byte("old recoverable note"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeState(ctx, sess, StateSuspended); err != nil {
		t.Fatal(err)
	}
	inputs := make(chan bus.Envelope, 1)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(ctx, sess, "new explicit task"); err != nil {
		t.Fatal(err)
	}
	assertInputPrompt(t, waitInput(t, inputs), BootstrapPrompt)
	got, err := sess.ReadRecord(ctx, WorkingNoteNamespace)
	want := "# Working Note\n\n## Original Request\n\nnew explicit task"
	if err != nil || string(got) != want {
		t.Fatalf("working note after explicit start = %q, %v; want %q", got, err, want)
	}
}

func TestManagerExplicitStartReplacesRunningLoopWithoutOldControllerInterference(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	ctx := context.Background()
	inputs := make(chan bus.Envelope, 4)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		inputs <- env
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(ctx, sess, "old task"); err != nil {
		t.Fatal(err)
	}
	oldPrompt := waitInput(t, inputs)
	assertInputPrompt(t, oldPrompt, BootstrapPrompt)

	if err := m.Start(ctx, sess, "replacement task"); err != nil {
		t.Fatal(err)
	}
	cancel := waitInput(t, inputs)
	if cancel.Name != bus.InboxCancelInput {
		t.Fatalf("replacement event = %q, want cancellation", cancel.Name)
	}
	var cancelBody bus.CancelInput
	if err := events.DecodePayload(cancel.Event, &cancelBody); err != nil {
		t.Fatal(err)
	}
	if cancelBody.EventID != oldPrompt.ID {
		t.Fatalf("cancel target = %q, want %q", cancelBody.EventID, oldPrompt.ID)
	}
	assertInputPrompt(t, waitInput(t, inputs), BootstrapPrompt)
	waitState(t, sess, StateActive)
	got, err := sess.ReadRecord(ctx, WorkingNoteNamespace)
	want := "# Working Note\n\n## Original Request\n\nreplacement task"
	if err != nil || string(got) != want {
		t.Fatalf("replacement working note = %q, %v; want %q", got, err, want)
	}
}

func TestManagerAttachRepairsAndResumesUnknownLoopState(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	if err := writeState(context.Background(), sess, State("finishing")); err != nil {
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

func TestManagerAttachDoesNotResumeTerminalStates(t *testing.T) {
	for _, terminal := range []State{StateCompleted, StateCancelled} {
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

func TestManagerErrorTurnSuspendsWithoutRecovery(t *testing.T) {
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
	waitState(t, sess, StateSuspended)
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

func TestManagerResumeWaitsForPreviousControllerToExit(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 3)
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
	if err := writeState(context.Background(), sess, StateSuspended); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	// The old controller still owns the bootstrap turn, so Resume must not
	// publish recovery until that controller observes the terminal event.
	assertNoInput(t, inputs)
	publishFinished(b, sess.ID, bootstrap.ID, "error")
	assertInputPrompt(t, waitInput(t, inputs), RecoveryPrompt)
	waitState(t, sess, StateActive)
}

func TestManagerResumeDoesNotReviveTerminalLoop(t *testing.T) {
	for _, terminal := range []State{StateCompleted, StateCancelled} {
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

			if err := m.Resume(context.Background(), sess); err != nil {
				t.Fatal(err)
			}
			assertNoInput(t, inputs)
			waitState(t, sess, terminal)
		})
	}
}

func TestManagerConcurrentResumeLaunchesOneController(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	if err := writeState(context.Background(), sess, StateSuspended); err != nil {
		t.Fatal(err)
	}
	inputs := make(chan bus.Envelope, 8)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Resume(context.Background(), sess); err != nil {
				t.Errorf("Resume() error = %v", err)
			}
		}()
	}
	wg.Wait()
	assertInputPrompt(t, waitInput(t, inputs), RecoveryPrompt)
	assertNoInput(t, inputs)
}

func TestManagerPendingResumeDoesNotReviveCancelledLoop(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 3)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		if env.Name == bus.InboxUserText {
			inputs <- env
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	_ = waitInput(t, inputs)
	if err := writeState(context.Background(), sess, StateSuspended); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := m.Cancel(context.Background(), sess); err != nil || !cancelled {
		t.Fatalf("Cancel() = %v, %v", cancelled, err)
	}
	waitState(t, sess, StateCancelled)
	assertNoInput(t, inputs)
}

func TestManagerExternalTurnErrorInterruptsLoopInput(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 3)
	id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
		inputs <- env
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
	defer b.Unsubscribe(id)

	if err := m.Start(context.Background(), sess, "implement it"); err != nil {
		t.Fatal(err)
	}
	prompt := waitInput(t, inputs)
	if err := m.RecordRunFinished(context.Background(), sess, "error"); err != nil {
		t.Fatal(err)
	}
	cancel := waitInput(t, inputs)
	if cancel.Name != bus.InboxCancelInput {
		t.Fatalf("interrupt event = %q, want %q", cancel.Name, bus.InboxCancelInput)
	}
	var body bus.CancelInput
	if err := events.DecodePayload(cancel.Event, &body); err != nil {
		t.Fatal(err)
	}
	if body.EventID != prompt.ID {
		t.Fatalf("cancel target = %q, want %q", body.EventID, prompt.ID)
	}
	waitState(t, sess, StateSuspended)
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

func assertPhase(t *testing.T, sess *session.Session, want phase) {
	t.Helper()
	got, err := readPhase(context.Background(), sess)
	if err != nil || got != want {
		t.Fatalf("phase = %q, want %q (err=%v)", got, want, err)
	}
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
