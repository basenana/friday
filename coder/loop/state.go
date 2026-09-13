package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/session"
)

const (
	StateNamespace       = "coder.loop.state"
	WorkingNoteNamespace = "coder.loop.working_note"
)

type State string

const (
	StateActive    State = "active"
	StateSuspended State = "suspended"
	// StateFinishing is accepted for compatibility with sessions written by
	// older versions. New code never writes it and it is treated as terminal.
	StateFinishing State = "finishing"
	StateCompleted State = "completed"
	StateCancelled State = "cancelled"
	StateFailed    State = "failed"
)

func readState(ctx context.Context, records interface {
	ReadRecord(context.Context, string) ([]byte, error)
}) (State, error) {
	raw, err := records.ReadRecord(ctx, StateNamespace)
	if errors.Is(err, session.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	state := State(strings.TrimSpace(string(raw)))
	switch state {
	case "", StateActive, StateSuspended, StateFinishing, StateCompleted, StateCancelled, StateFailed:
		return state, nil
	default:
		return "", fmt.Errorf("unknown loop state %q", state)
	}
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
		state := State(strings.TrimSpace(string(current)))
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
