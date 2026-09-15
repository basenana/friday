package loop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
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
	if secondBody.Text != DevelopPrompt {
		t.Fatalf("next prompt = %q", secondBody.Text)
	}
	assertPhase(t, sess, phaseDevelop)

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
	assertInputPrompt(t, waitInput(t, inputs), DevelopRecoveryPrompt)
	assertPhase(t, sess, phaseRecoveryDevelop)
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
		{phaseDevelop, DevelopPrompt},
		{phaseUpdate, UpdatePrompt},
		{phaseDevelop, DevelopPrompt},
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

func TestShouldCompactAfterUpdateThreshold(t *testing.T) {
	for _, tc := range []struct {
		tokens int64
		want   bool
	}{
		{tokens: loopCompactThreshold - 1, want: false},
		{tokens: loopCompactThreshold, want: true},
		{tokens: loopCompactThreshold + 1, want: true},
	} {
		if got := shouldCompactAfterUpdate(tc.tokens); got != tc.want {
			t.Errorf("shouldCompactAfterUpdate(%d) = %v, want %v", tc.tokens, got, tc.want)
		}
	}
}

func TestManagerCompactsLargeHistoryAfterUpdate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		handoff    bool
		wantPhase  phase
		wantPrompt string
	}{
		{name: "continue development", wantPhase: phaseDevelop, wantPrompt: DevelopPrompt},
		{name: "handoff to review", handoff: true, wantPhase: phaseReview, wantPrompt: ReviewPrompt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := eventbus.NewBus()
			m := NewManager(b)
			defer m.Close()
			sess := session.New("root", nil)
			appendLargeLoopHistory(sess)
			if tokens := sess.Tokens(); tokens < loopCompactThreshold {
				t.Fatalf("test history has %d tokens, want at least %d", tokens, loopCompactThreshold)
			}

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
			bootstrap := waitInput(t, inputs)
			publishFinished(b, sess.ID, bootstrap.ID, "end_turn")
			develop := waitInput(t, inputs)
			publishFinished(b, sess.ID, develop.ID, "end_turn")
			update := waitInput(t, inputs)
			if tc.handoff {
				result, err := finishDevLoop(context.Background(), &tools.Request{SessionID: sess.ID, SessionRecords: sess})
				if err != nil || result.IsError {
					t.Fatalf("finishDevLoop() = %#v, %v", result, err)
				}
			}

			publishFinished(b, sess.ID, update.ID, "end_turn")
			assertInputPrompt(t, waitInput(t, inputs), tc.wantPrompt)
			assertPhase(t, sess, tc.wantPhase)
			if got := sess.HistoryLen(); got != session.CompactFallbackKeepMessages() {
				t.Fatalf("history length after update compact = %d, want %d", got, session.CompactFallbackKeepMessages())
			}
		})
	}
}

func TestManagerSkipsCompactBelowUpdateThreshold(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	for range session.CompactFallbackKeepMessages() + 1 {
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "small history entry"})
	}
	wantHistoryLen := sess.HistoryLen()

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
	bootstrap := waitInput(t, inputs)
	publishFinished(b, sess.ID, bootstrap.ID, "end_turn")
	develop := waitInput(t, inputs)
	publishFinished(b, sess.ID, develop.ID, "end_turn")
	update := waitInput(t, inputs)
	publishFinished(b, sess.ID, update.ID, "end_turn")
	assertInputPrompt(t, waitInput(t, inputs), DevelopPrompt)
	if got := sess.HistoryLen(); got != wantHistoryLen {
		t.Fatalf("history length below compact threshold = %d, want unchanged %d", got, wantHistoryLen)
	}
}

func TestManagerContinuesWhenUpdateCompactFails(t *testing.T) {
	wantErr := errors.New("compact provider unavailable")
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", failingLoopCompactClient{err: wantErr})
	appendLargeLoopHistory(sess)

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
	bootstrap := waitInput(t, inputs)
	publishFinished(b, sess.ID, bootstrap.ID, "end_turn")
	develop := waitInput(t, inputs)
	publishFinished(b, sess.ID, develop.ID, "end_turn")
	update := waitInput(t, inputs)
	publishFinished(b, sess.ID, update.ID, "end_turn")
	assertInputPrompt(t, waitInput(t, inputs), DevelopPrompt)
	assertPhase(t, sess, phaseDevelop)
}

func TestManagerDetachDuringUpdateCompactDoesNotDispatch(t *testing.T) {
	client := &blockingLoopCompactClient{started: make(chan struct{})}
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", client)
	appendLargeLoopHistory(sess)

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
	bootstrap := waitInput(t, inputs)
	publishFinished(b, sess.ID, bootstrap.ID, "end_turn")
	develop := waitInput(t, inputs)
	publishFinished(b, sess.ID, develop.ID, "end_turn")
	update := waitInput(t, inputs)
	publishFinished(b, sess.ID, update.ID, "end_turn")
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for update compaction")
	}

	m.Detach(sess.ID)
	waitState(t, sess, StateSuspended)
	assertNoInput(t, inputs)
}

func TestManagerRunsDevelopmentAndReviewCycles(t *testing.T) {
	b := eventbus.NewBus()
	m := NewManager(b)
	defer m.Close()
	sess := session.New("root", nil)
	inputs := make(chan bus.Envelope, 8)
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
	publishFinished(b, sess.ID, bootstrap.ID, "end_turn")
	develop := waitInput(t, inputs)
	assertInputPrompt(t, develop, DevelopPrompt)
	publishFinished(b, sess.ID, develop.ID, "end_turn")
	update := waitInput(t, inputs)
	assertInputPrompt(t, update, UpdatePrompt)

	result, err := finishDevLoop(context.Background(), &tools.Request{SessionID: sess.ID, SessionRecords: sess})
	if err != nil || result.IsError {
		t.Fatalf("finishDevLoop() = %#v, %v", result, err)
	}
	if state, _ := readState(context.Background(), sess); state != StateActive {
		t.Fatalf("state after development handoff = %q; want active", state)
	}
	publishFinished(b, sess.ID, update.ID, "end_turn")
	review := waitInput(t, inputs)
	assertInputPrompt(t, review, ReviewPrompt)
	publishFinished(b, sess.ID, review.ID, "end_turn")
	revise := waitInput(t, inputs)
	assertInputPrompt(t, revise, RevisePrompt)
	publishFinished(b, sess.ID, revise.ID, "end_turn")
	review = waitInput(t, inputs)
	assertInputPrompt(t, review, ReviewPrompt)

	result, err = finishReviewLoop(context.Background(), &tools.Request{SessionID: sess.ID, SessionRecords: sess})
	if err != nil || result.IsError {
		t.Fatalf("finishReviewLoop() = %#v, %v", result, err)
	}
	publishFinished(b, sess.ID, review.ID, "end_turn")
	waitState(t, sess, StateCompleted)
	assertNoInput(t, inputs)
}

func TestManagerRecoveryReturnsToInterruptedCycle(t *testing.T) {
	for _, tc := range []struct {
		name           string
		interrupted    phase
		recovery       phase
		recoveryPrompt string
		nextPrompt     string
	}{
		{name: "development", interrupted: phaseUpdate, recovery: phaseRecoveryDevelop, recoveryPrompt: DevelopRecoveryPrompt, nextPrompt: DevelopPrompt},
		{name: "review", interrupted: phaseReview, recovery: phaseRecoveryReview, recoveryPrompt: ReviewRecoveryPrompt, nextPrompt: ReviewPrompt},
		{name: "revise", interrupted: phaseRevise, recovery: phaseRecoveryReview, recoveryPrompt: ReviewRecoveryPrompt, nextPrompt: ReviewPrompt},
		{name: "repeated review recovery", interrupted: phaseRecoveryReview, recovery: phaseRecoveryReview, recoveryPrompt: ReviewRecoveryPrompt, nextPrompt: ReviewPrompt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := eventbus.NewBus()
			m := NewManager(b)
			defer m.Close()
			sess := session.New("root", nil)
			if err := writeState(context.Background(), sess, StateSuspended); err != nil {
				t.Fatal(err)
			}
			if err := writePhase(context.Background(), sess, tc.interrupted); err != nil {
				t.Fatal(err)
			}
			inputs := make(chan bus.Envelope, 2)
			id := b.SubscribeSerial([]string{bus.TopicInbox(sess.ID)}, func(env bus.Envelope) {
				if env.Name == bus.InboxUserText {
					inputs <- env
				}
			}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})
			defer b.Unsubscribe(id)

			if err := m.Resume(context.Background(), sess); err != nil {
				t.Fatal(err)
			}
			recovery := waitInput(t, inputs)
			assertInputPrompt(t, recovery, tc.recoveryPrompt)
			assertPhase(t, sess, tc.recovery)
			publishFinished(b, sess.ID, recovery.ID, "end_turn")
			assertInputPrompt(t, waitInput(t, inputs), tc.nextPrompt)
		})
	}
}

func TestManagerSuspendsOnUnexpectedPhaseChange(t *testing.T) {
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
	if err := writePhase(context.Background(), sess, phaseReview); err != nil {
		t.Fatal(err)
	}
	publishFinished(b, sess.ID, bootstrap.ID, "end_turn")
	waitState(t, sess, StateSuspended)
	assertNoInput(t, inputs)
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
	assertInputPrompt(t, waitInput(t, inputs), DevelopRecoveryPrompt)
	assertPhase(t, sess, phaseRecoveryDevelop)
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

func TestManagerHoldsActorLeaseForControllerLifetime(t *testing.T) {
	b := eventbus.NewBus()
	acquired := make(chan struct{}, 1)
	released := make(chan struct{}, 1)
	m := NewManager(b, WithActorLease(func(string) (func(), error) {
		acquired <- struct{}{}
		var once sync.Once
		return func() { once.Do(func() { released <- struct{}{} }) }, nil
	}))
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
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("controller did not acquire actor lease")
	}
	prompt := waitInput(t, inputs)
	select {
	case <-released:
		t.Fatal("controller released actor lease while its turn was active")
	default:
	}
	if err := writeState(context.Background(), sess, StateCompleted); err != nil {
		t.Fatal(err)
	}
	publishFinished(b, sess.ID, prompt.ID, "end_turn")
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("controller did not release actor lease after completion")
	}
}

func TestManagerLeaseFailureSuspendsLoop(t *testing.T) {
	wantErr := errors.New("actor unavailable")
	m := NewManager(eventbus.NewBus(), WithActorLease(func(string) (func(), error) {
		return nil, wantErr
	}))
	defer m.Close()
	sess := session.New("root", nil)

	err := m.Start(context.Background(), sess, "implement it")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want %v", err, wantErr)
	}
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

func appendLargeLoopHistory(sess *session.Session) {
	for range session.CompactFallbackKeepMessages() + 5 {
		sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: strings.Repeat("x", 8_000)})
	}
}

type failingLoopCompactClient struct {
	err error
}

func (c failingLoopCompactClient) Completion(context.Context, providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	resp.Err <- c.err
	return resp
}

func (failingLoopCompactClient) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("unexpected CompletionNonStreaming call")
}

func (failingLoopCompactClient) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("unexpected StructuredPredict call")
}

type blockingLoopCompactClient struct {
	started chan struct{}
	once    sync.Once
}

func (c *blockingLoopCompactClient) Completion(ctx context.Context, _ providers.Request) providers.Response {
	c.once.Do(func() { close(c.started) })
	resp := providers.NewCommonResponse()
	go func() {
		<-ctx.Done()
		resp.Err <- ctx.Err()
	}()
	return resp
}

func (*blockingLoopCompactClient) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("unexpected CompletionNonStreaming call")
}

func (*blockingLoopCompactClient) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("unexpected StructuredPredict call")
}
