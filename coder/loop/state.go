package loop

import (
	"context"
	"errors"
	"strings"

	"github.com/basenana/friday/core/session"
)

const (
	StateNamespace       = "coder.loop.state"
	WorkingNoteNamespace = "coder.loop.working_note"
	phaseNamespace       = "coder.loop.phase"
)

type State string

const (
	StateActive    State = "active"
	StateSuspended State = "suspended"
	StateCompleted State = "completed"
	StateCancelled State = "cancelled"
)

type stateRecords interface {
	ReadRecord(context.Context, string) ([]byte, error)
	UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error
}

func parsePhase(raw []byte) (phase, bool) {
	switch strings.TrimSpace(string(raw)) {
	case "bootstrap":
		return phaseBootstrap, true
	case "select":
		// Select used to be a standalone phase. Resume persisted Loops at the
		// merged develop phase, which now performs selection before editing.
		return phaseDevelop, true
	case "develop":
		return phaseDevelop, true
	case "review":
		return phaseReview, true
	case "update":
		return phaseUpdate, true
	case "recovery":
		return phaseRecovery, true
	default:
		return phaseUnknown, false
	}
}

func readPhase(ctx context.Context, records stateRecords) (phase, error) {
	raw, err := records.ReadRecord(ctx, phaseNamespace)
	if errors.Is(err, session.ErrRecordNotFound) {
		return phaseUnknown, nil
	}
	if err != nil {
		return phaseUnknown, err
	}
	current, _ := parsePhase(raw)
	return current, nil
}

func writePhase(ctx context.Context, records stateRecords, current phase) error {
	if current == phaseUnknown {
		return errors.New("cannot persist unknown loop phase")
	}
	return records.UpdateRecord(ctx, phaseNamespace, func([]byte) ([]byte, error) {
		return []byte(current.String()), nil
	})
}

func parseState(raw []byte) (State, bool) {
	state := State(strings.TrimSpace(string(raw)))
	switch state {
	case StateActive, StateSuspended, StateCompleted, StateCancelled:
		return state, true
	default:
		return StateSuspended, false
	}
}

// readState returns the persisted loop state. A missing record means that the
// session has no loop. Any existing but unrecognized value is repaired to the
// recoverable suspended state so a hand-edited record cannot strand the loop.
func readState(ctx context.Context, records stateRecords) (State, error) {
	raw, err := records.ReadRecord(ctx, StateNamespace)
	if errors.Is(err, session.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if state, ok := parseState(raw); ok {
		return state, nil
	}

	resolved := StateSuspended
	err = records.UpdateRecord(ctx, StateNamespace, func(current []byte) ([]byte, error) {
		if state, ok := parseState(current); ok {
			resolved = state
			return current, nil
		}
		resolved = StateSuspended
		return []byte(StateSuspended), nil
	})
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func writeState(ctx context.Context, records interface {
	UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error
}, state State) error {
	return records.UpdateRecord(ctx, StateNamespace, func([]byte) ([]byte, error) {
		return []byte(state), nil
	})
}

func transitionState(ctx context.Context, records interface {
	UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error
}, from []State, to State) (bool, error) {
	changed := false
	err := records.UpdateRecord(ctx, StateNamespace, func(current []byte) ([]byte, error) {
		state, valid := parseState(current)
		if !valid {
			current = []byte(StateSuspended)
		}
		for _, candidate := range from {
			if state == candidate {
				changed = true
				return []byte(to), nil
			}
		}
		return current, nil
	})
	return changed, err
}

func loopEnabled(state State) bool {
	return state == StateActive || state == StateSuspended
}
