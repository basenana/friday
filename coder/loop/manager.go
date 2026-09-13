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
	phaseBootstrap phase = iota
	phaseSelect
	phaseDevelop
	phaseReview
	phaseUpdate
	phaseRecovery
	phaseFinalize
)

type controller struct {
	session *session.Session
	phase   phase
	cancel  context.CancelFunc

	mu                  sync.Mutex
	currentInputEventID string
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
	if loopEnabled(state) {
		return errors.New("a loop is already active in this session")
	}
	// A just-completed controller may still be unwinding its goroutine. It no
	// longer owns task state, so retire it before installing the new loop.
	m.Detach(sess.ID)
	seed := "# Working Note\n\n## Original Request\n\n" + task
	if err := sess.UpdateRecord(ctx, WorkingNoteNamespace, func([]byte) ([]byte, error) { return []byte(seed), nil }); err != nil {
		return fmt.Errorf("initialize working note: %w", err)
	}
	if err := writeState(ctx, sess, StateActive); err != nil {
		return fmt.Errorf("activate loop: %w", err)
	}
	return m.launch(sess, phaseBootstrap)
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
	case StateActive:
		return m.launch(sess, phaseRecovery)
	case StateFinishing:
		return m.launch(sess, phaseFinalize)
	default:
		return nil
	}
}

func (m *Manager) IsActive(ctx context.Context, sess *session.Session) (bool, error) {
	if sess == nil {
		return false, nil
	}
	state, err := readState(ctx, sess)
	return loopEnabled(state), err
}

func (m *Manager) launch(sess *session.Session, initial phase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.controllers[sess.ID]; exists {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &controller{session: sess, phase: initial, cancel: cancel}
	m.controllers[sess.ID] = c
	go m.run(ctx, c)
	return nil
}

func (m *Manager) run(ctx context.Context, c *controller) {
	defer func() {
		m.mu.Lock()
		if m.controllers[c.session.ID] == c {
			delete(m.controllers, c.session.ID)
		}
		m.mu.Unlock()
	}()

	for {
		stopReason, err := m.dispatch(ctx, c, promptFor(c.phase))
		if err != nil {
			return
		}
		state, err := readState(ctx, c.session)
		if err != nil {
			return
		}
		switch state {
		case StateCancelled, StateCompleted, "":
			return
		case StateFinishing:
			if stopReason == "end_turn" {
				_, _ = transitionState(context.Background(), c.session, []State{StateFinishing}, StateCompleted)
			}
			return
		case StateActive:
			if stopReason != "end_turn" {
				c.phase = phaseRecovery
				continue
			}
			c.phase = nextPhase(c.phase)
		}
	}
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
				// A user or another producer may finish the Loop while this
				// controller prompt is queued behind that turn. Once that turn has
				// ended normally, it already delivered the final response. Cancel
				// our stale prompt and complete without running an extra recovery or
				// phase turn.
				if body.StopReason == "end_turn" {
					state, stateErr := readState(ctx, c.session)
					if stateErr != nil {
						return "", stateErr
					}
					if state == StateFinishing {
						m.cancelInput(c.session.ID, env.ID, "loop finished by another input")
						_, transitionErr := transitionState(context.Background(), c.session, []State{StateFinishing}, StateCompleted)
						return body.StopReason, transitionErr
					}
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
	cancelled, err := transitionState(ctx, sess, []State{StateActive, StateFinishing}, StateCancelled)
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
		c.cancel()
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
	case phaseFinalize:
		return FinalizePrompt
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
