package loop

import (
	"context"
	"errors"
	"testing"

	"github.com/basenana/friday/core/session"
)

func TestReadStateMissingRecordStaysAbsent(t *testing.T) {
	ctx := context.Background()
	sess := session.New("root", nil)

	state, err := readState(ctx, sess)
	if err != nil || state != "" {
		t.Fatalf("readState() = %q, %v; want empty state", state, err)
	}
	if _, err := sess.ReadRecord(ctx, StateNamespace); !errors.Is(err, session.ErrRecordNotFound) {
		t.Fatalf("missing state record was created: %v", err)
	}
}

func TestReadStateRepairsUnknownValuesToSuspended(t *testing.T) {
	for _, raw := range []string{"", "garbage", "ACTIVE", "failed", "finishing"} {
		t.Run(raw, func(t *testing.T) {
			ctx := context.Background()
			sess := session.New("root", nil)
			if err := writeState(ctx, sess, State(raw)); err != nil {
				t.Fatal(err)
			}

			state, err := readState(ctx, sess)
			if err != nil || state != StateSuspended {
				t.Fatalf("readState(%q) = %q, %v; want suspended", raw, state, err)
			}
			persisted, err := sess.ReadRecord(ctx, StateNamespace)
			if err != nil || string(persisted) != string(StateSuspended) {
				t.Fatalf("persisted state = %q, %v; want suspended", persisted, err)
			}
		})
	}
}

func TestTransitionStateNormalizesUnknownValueAtomically(t *testing.T) {
	ctx := context.Background()
	sess := session.New("root", nil)
	if err := writeState(ctx, sess, State("broken")); err != nil {
		t.Fatal(err)
	}

	changed, err := transitionState(ctx, sess, []State{StateSuspended}, StateCancelled)
	if err != nil || !changed {
		t.Fatalf("transitionState() = %v, %v; want changed", changed, err)
	}
	state, err := readState(ctx, sess)
	if err != nil || state != StateCancelled {
		t.Fatalf("state = %q, %v; want cancelled", state, err)
	}
}

type updateFailingRecords struct {
	err error
}

func (r updateFailingRecords) ReadRecord(context.Context, string) ([]byte, error) {
	return []byte("broken"), nil
}

func (r updateFailingRecords) UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error {
	return r.err
}

func TestReadStateReportsUnknownValueRepairFailure(t *testing.T) {
	want := errors.New("disk is read-only")
	state, err := readState(context.Background(), updateFailingRecords{err: want})
	if state != "" || !errors.Is(err, want) {
		t.Fatalf("readState() = %q, %v; want empty state and repair error", state, err)
	}
}

type changedBeforeRepairRecords struct {
	persisted []byte
}

func (r *changedBeforeRepairRecords) ReadRecord(context.Context, string) ([]byte, error) {
	return []byte("broken"), nil
}

func (r *changedBeforeRepairRecords) UpdateRecord(_ context.Context, _ string, update func([]byte) ([]byte, error)) error {
	next, err := update([]byte(StateCompleted))
	if err == nil {
		r.persisted = next
	}
	return err
}

func TestReadStateRepairDoesNotOverwriteConcurrentTerminalState(t *testing.T) {
	records := &changedBeforeRepairRecords{}
	state, err := readState(context.Background(), records)
	if err != nil || state != StateCompleted {
		t.Fatalf("readState() = %q, %v; want completed", state, err)
	}
	if string(records.persisted) != string(StateCompleted) {
		t.Fatalf("persisted state = %q; want completed", records.persisted)
	}
}

func TestPhaseRecordRoundTripAndUnknownValuesFailClosed(t *testing.T) {
	ctx := context.Background()
	sess := session.New("root", nil)
	if got, err := readPhase(ctx, sess); err != nil || got != phaseUnknown {
		t.Fatalf("missing phase = %q, %v; want unknown", got, err)
	}
	for _, want := range []phase{phaseBootstrap, phaseSelect, phaseDevelop, phaseReview, phaseUpdate, phaseRecovery} {
		if err := writePhase(ctx, sess, want); err != nil {
			t.Fatal(err)
		}
		if got, err := readPhase(ctx, sess); err != nil || got != want {
			t.Fatalf("phase = %q, %v; want %q", got, err, want)
		}
	}
	if err := sess.UpdateRecord(ctx, phaseNamespace, func([]byte) ([]byte, error) {
		return []byte("future-phase"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := readPhase(ctx, sess); err != nil || got != phaseUnknown {
		t.Fatalf("invalid phase = %q, %v; want unknown", got, err)
	}
	if err := writePhase(ctx, sess, phaseUnknown); err == nil {
		t.Fatal("writePhase accepted unknown phase")
	}
}
