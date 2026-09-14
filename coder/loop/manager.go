package loop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/session"
)

type phase int

const (
	phaseUnknown phase = iota
	phaseBootstrap
	phaseSelect
	phaseDevelop
	phaseReview
	phaseUpdate
	phaseRecovery
)

func (p phase) String() string {
	switch p {
	case phaseBootstrap:
		return "bootstrap"
	case phaseSelect:
		return "select"
	case phaseDevelop:
		return "develop"
	case phaseReview:
		return "review"
	case phaseUpdate:
		return "update"
	case phaseRecovery:
		return "recovery"
	default:
		return "unknown"
	}
}

type controller struct {
	session *session.Session
	phase   phase
	cancel  context.CancelFunc
	done    chan struct{}

	mu                  sync.Mutex
	currentInputEventID string
	resumePending       bool
}

type Manager struct {
	bus *eventbus.Bus

	mu          sync.Mutex
	controllers map[string]*controller
}

func NewManager(b *eventbus.Bus) *Manager {
	return &Manager{bus: b, controllers: make(map[string]*controller)}
}

func (m *Manager) Start(ctx context.Context, sess *session.Session, task string) error {
	if sess == nil || sess.Root == nil || sess.ID != sess.Root.ID {
		return errors.New("loop requires a root session")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return errors.New("loop task is required")
	}
	state, err := readState(ctx, sess)
	if err != nil {
		return err
	}
	// An explicit Start always creates a new Loop. Mark any recoverable Loop
	// terminal before stopping its controller so the retiring controller cannot
	// suspend or resume the new Loop while it unwinds.
	if loopEnabled(state) {
		if _, err := transitionState(ctx, sess, []State{StateActive, StateSuspended}, StateCancelled); err != nil {
			return fmt.Errorf("replace existing loop: %w", err)
		}
	}
	if err := m.retireController(ctx, sess.ID, "user started a new loop"); err != nil {
		return err
	}
	seed := "# Working Note\n\n## Original Request\n\n" + task
	if err := sess.UpdateRecord(ctx, WorkingNoteNamespace, func([]byte) ([]byte, error) { return []byte(seed), nil }); err != nil {
		return fmt.Errorf("initialize working note: %w", err)
	}
	if err := writePhase(ctx, sess, phaseBootstrap); err != nil {
		return fmt.Errorf("initialize loop phase: %w", err)
	}
	if err := writeState(ctx, sess, StateActive); err != nil {
		return fmt.Errorf("activate loop: %w", err)
	}
	return m.launch(ctx, sess, phaseBootstrap)
}

func (m *Manager) Attach(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.Root == nil || sess.ID != sess.Root.ID {
		return nil
	}
	state, err := readState(ctx, sess)
	if err != nil {
		return err
	}
	switch state {
	case StateActive, StateSuspended:
		return m.Resume(ctx, sess)
	default:
		return nil
	}
}

// Resume wakes a recoverable loop. When its previous controller is still
// unwinding, the wake-up is deferred until that controller has relinquished
// ownership so the request cannot be lost or create duplicate controllers.
func (m *Manager) Resume(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.Root == nil || sess.ID != sess.Root.ID {
		return nil
	}
	state, err := readState(ctx, sess)
	if err != nil {
		return err
	}
	switch state {
	case StateActive:
		return m.launch(ctx, sess, phaseRecovery)
	case StateSuspended:
		m.mu.Lock()
		if c := m.controllers[sess.ID]; c != nil {
			c.resumePending = true
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()

		if err := writePhase(ctx, sess, phaseRecovery); err != nil {
			return fmt.Errorf("prepare loop recovery phase: %w", err)
		}
		changed, transitionErr := transitionState(ctx, sess, []State{StateSuspended}, StateActive)
		if transitionErr != nil {
			return transitionErr
		}
		if changed {
			return m.launch(ctx, sess, phaseRecovery)
		}
		// A concurrent resume may already have activated the state. Ensure it
		// also owns a controller; launch is idempotent.
		state, err = readState(ctx, sess)
		if err != nil {
			return err
		}
		if state == StateActive {
			return m.launch(ctx, sess, phaseRecovery)
		}
	}
	return nil
}

func (m *Manager) IsActive(ctx context.Context, sess *session.Session) (bool, error) {
	if sess == nil {
		return false, nil
	}
	state, err := readState(ctx, sess)
	return loopEnabled(state), err
}

func (m *Manager) launch(ctx context.Context, sess *session.Session, initial phase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.controllers[sess.ID]; exists {
		return nil
	}
	if err := writePhase(ctx, sess, initial); err != nil {
		_, _ = transitionState(context.Background(), sess, []State{StateActive}, StateSuspended)
		return fmt.Errorf("persist loop phase %q: %w", initial, err)
	}
	controllerCtx, cancel := context.WithCancel(context.Background())
	c := &controller{session: sess, phase: initial, cancel: cancel, done: make(chan struct{})}
	m.controllers[sess.ID] = c
	go m.run(controllerCtx, c)
	return nil
}

func (m *Manager) run(ctx context.Context, c *controller) {
	defer close(c.done)
	defer m.finishController(c)

	for {
		state, err := readState(ctx, c.session)
		if err != nil || state != StateActive {
			return
		}
		if err := writePhase(ctx, c.session, c.phase); err != nil {
			_ = m.suspend(context.Background(), c.session, "loop phase could not be persisted", false)
			return
		}
		stopReason, err := m.dispatch(ctx, c, promptFor(c.phase))
		if err != nil {
			_ = m.suspend(context.Background(), c.session, "loop turn interrupted", false)
			return
		}
		state, err = readState(ctx, c.session)
		if err != nil {
			return
		}
		switch state {
		case StateCancelled, StateCompleted, StateSuspended, "":
			return
		case StateActive:
			switch stopReason {
			case "end_turn":
				c.phase = nextPhase(c.phase)
			case "cancelled":
				_, _ = transitionState(context.Background(), c.session, []State{StateActive}, StateCancelled)
				return
			default:
				_ = m.suspend(context.Background(), c.session, "loop turn failed", false)
				return
			}
		}
	}
}

func (m *Manager) retireController(ctx context.Context, sessionID, reason string) error {
	m.mu.Lock()
	c := m.controllers[sessionID]
	if c != nil {
		delete(m.controllers, sessionID)
		c.resumePending = false
	}
	m.mu.Unlock()
	if c == nil {
		return nil
	}

	c.mu.Lock()
	inputID := c.currentInputEventID
	c.mu.Unlock()
	c.cancel()
	m.cancelInput(sessionID, inputID, reason)
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) finishController(c *controller) {
	m.mu.Lock()
	resume := false
	if m.controllers[c.session.ID] == c {
		delete(m.controllers, c.session.ID)
		resume = c.resumePending
	}
	m.mu.Unlock()
	if !resume {
		return
	}

	state, err := readState(context.Background(), c.session)
	if err != nil || state != StateSuspended {
		return
	}
	if err := writePhase(context.Background(), c.session, phaseRecovery); err != nil {
		return
	}
	changed, err := transitionState(context.Background(), c.session, []State{StateSuspended}, StateActive)
	if err == nil && changed {
		_ = m.launch(context.Background(), c.session, phaseRecovery)
	}
}

func (m *Manager) suspend(ctx context.Context, sess *session.Session, reason string, interruptController bool) error {
	state, err := readState(ctx, sess)
	if err != nil || !loopEnabled(state) || state == StateSuspended {
		return err
	}
	changed, err := transitionState(ctx, sess, []State{StateActive}, StateSuspended)
	if err != nil || !changed {
		return err
	}

	if interruptController {
		m.interruptController(sess.ID, reason)
	}
	return nil
}

func (m *Manager) interruptController(sessionID, reason string) {
	m.mu.Lock()
	c := m.controllers[sessionID]
	m.mu.Unlock()
	if c == nil {
		return
	}
	c.mu.Lock()
	inputID := c.currentInputEventID
	c.mu.Unlock()
	c.cancel()
	m.cancelInput(sessionID, inputID, reason)
}

func (m *Manager) dispatch(ctx context.Context, c *controller, prompt string) (string, error) {
	eventCh := make(chan events.Event, 256)
	ids := bus.SubscribeAgent(m.bus, c.session.ID, func(env bus.Envelope) {
		select {
		case eventCh <- env.Event:
		case <-ctx.Done():
		}
	}, eventbus.SerialConfig{Buffer: 256, Overflow: eventbus.OverflowBlock})
	defer bus.UnsubscribeAll(m.bus, ids...)

	env := bus.NewUserInput(c.session.ID, "loop", bus.UserTextInput{Text: prompt, Delivery: bus.DeliveryNormal})
	c.mu.Lock()
	c.currentInputEventID = env.ID
	c.mu.Unlock()
	m.bus.Publish(bus.TopicInbox(c.session.ID), env)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case evt := <-eventCh:
			if evt.Type == events.KindRunFinished {
				var body events.RunFinishedData
				if err := events.DecodePayload(evt, &body); err != nil {
					return "", err
				}
				if slices.Contains(evt.CausedBy, env.ID) {
					return body.StopReason, nil
				}
				continue
			}
			if !slices.Contains(evt.CausedBy, env.ID) {
				continue
			}
			if evt.Type == events.KindCustom && evt.Name == "status."+bus.StatusInboxDropped {
				return "", errors.New("loop input was dropped")
			}
			if evt.Type == events.KindCustom && evt.Name == events.CustomInputCancelled {
				return "", context.Canceled
			}
		}
	}
}

func (m *Manager) Cancel(ctx context.Context, sess *session.Session) (bool, error) {
	if sess == nil {
		return false, nil
	}
	state, err := readState(ctx, sess)
	if err != nil {
		return false, err
	}
	if !loopEnabled(state) {
		return false, nil
	}
	cancelled, err := transitionState(ctx, sess, []State{StateActive, StateSuspended}, StateCancelled)
	if err != nil {
		return false, err
	}
	if !cancelled {
		return false, nil
	}
	m.mu.Lock()
	c := m.controllers[sess.ID]
	m.mu.Unlock()
	if c != nil {
		c.mu.Lock()
		inputID := c.currentInputEventID
		c.mu.Unlock()
		c.cancel()
		if inputID != "" {
			m.cancelInput(sess.ID, inputID, "user cancelled loop")
		}
	}
	return true, nil
}

func (m *Manager) cancelInput(sessionID, eventID, reason string) {
	if eventID == "" {
		return
	}
	m.bus.Publish(bus.TopicInbox(sessionID), bus.NewCancelInput(sessionID, "loop", bus.CancelInput{
		EventID: eventID,
		Reason:  reason,
	}))
}

func (m *Manager) Detach(sessionID string) {
	m.mu.Lock()
	c := m.controllers[sessionID]
	if c != nil {
		delete(m.controllers, sessionID)
	}
	m.mu.Unlock()
	if c != nil {
		_, _ = transitionState(context.Background(), c.session, []State{StateActive}, StateSuspended)
		c.cancel()
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	controllers := make([]*controller, 0, len(m.controllers))
	for _, c := range m.controllers {
		controllers = append(controllers, c)
	}
	m.controllers = make(map[string]*controller)
	m.mu.Unlock()
	for _, c := range controllers {
		_, _ = transitionState(context.Background(), c.session, []State{StateActive}, StateSuspended)
		c.cancel()
	}
}

// RecordRunFinished makes actor outcomes durable even when they were produced
// by user input rather than by the controller's current prompt.
func (m *Manager) RecordRunFinished(ctx context.Context, sess *session.Session, stopReason string) error {
	if sess == nil {
		return nil
	}
	state, err := readState(ctx, sess)
	if err != nil || state == "" {
		return err
	}
	if state == StateCompleted || state == StateCancelled {
		m.interruptController(sess.ID, "loop reached a terminal state")
		return nil
	}
	switch stopReason {
	case "cancelled":
		_, err = transitionState(ctx, sess, []State{StateActive, StateSuspended}, StateCancelled)
		if err == nil {
			m.interruptController(sess.ID, "loop turn was cancelled")
		}
		return err
	case "error":
		return m.suspend(ctx, sess, "loop suspended after turn error", true)
	default:
		return nil
	}
}

func promptFor(p phase) string {
	switch p {
	case phaseBootstrap:
		return BootstrapPrompt
	case phaseSelect:
		return SelectPrompt
	case phaseDevelop:
		return DevelopPrompt
	case phaseReview:
		return ReviewPrompt
	case phaseUpdate:
		return UpdatePrompt
	case phaseRecovery:
		return RecoveryPrompt
	default:
		return RecoveryPrompt
	}
}

func nextPhase(p phase) phase {
	switch p {
	case phaseBootstrap, phaseRecovery:
		return phaseSelect
	case phaseSelect:
		return phaseDevelop
	case phaseDevelop:
		return phaseReview
	case phaseReview:
		return phaseUpdate
	case phaseUpdate:
		return phaseSelect
	default:
		return phaseSelect
	}
}
