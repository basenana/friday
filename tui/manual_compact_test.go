package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/core/session"
)

type manualCompactorStub struct {
	beforeMessages int
	afterMessages  int
	beforeTokens   int64
	afterTokens    int64
	trigger        session.CompactTrigger
	err            error
	compacted      bool
}

func (s *manualCompactorStub) CompactHistoryWithTrigger(_ context.Context, trigger session.CompactTrigger) error {
	s.trigger = trigger
	s.compacted = true
	return s.err
}

func (s *manualCompactorStub) HistoryLen() int {
	if s.compacted {
		return s.afterMessages
	}
	return s.beforeMessages
}

func (s *manualCompactorStub) Tokens() int64 {
	if s.compacted {
		return s.afterTokens
	}
	return s.beforeTokens
}

func TestCompactManuallyCollectsStatisticsAndTrigger(t *testing.T) {
	stub := &manualCompactorStub{
		beforeMessages: 42,
		afterMessages:  7,
		beforeTokens:   12_000,
		afterTokens:    4_000,
	}
	msg, ok := compactManually("session-1", stub)().(manualCompactFinishedMsg)
	if !ok {
		t.Fatal("compactManually returned unexpected message")
	}
	if stub.trigger != session.CompactTriggerManual {
		t.Fatalf("trigger = %q, want manual", stub.trigger)
	}
	if msg.beforeMessages != 42 || msg.afterMessages != 7 || msg.beforeTokens != 12_000 || msg.afterTokens != 4_000 || msg.err != nil {
		t.Fatalf("unexpected compact result: %#v", msg)
	}
}

func TestManualCompactActionStartsAsyncState(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.queued = []pendingInput{{text: "next"}}
	handled, cmd := m.applySessionAction(codercmds.CompactSessionAction{})
	if !handled || cmd == nil || !m.manualCompacting {
		t.Fatalf("manual compact not started: handled=%v cmd=%v compacting=%v", handled, cmd != nil, m.manualCompacting)
	}
	if m.canDispatchQueued() {
		t.Fatal("queued input must not dispatch during manual compaction")
	}
}

func TestFinishManualCompactReportsSuccessAndFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m, _, _ := newTestModel(t)
		m.manualCompacting = true
		m.finishManualCompact(manualCompactFinishedMsg{
			sessionID:      m.sessionID,
			beforeTokens:   12_000,
			afterTokens:    4_000,
			beforeMessages: 42,
			afterMessages:  7,
		})
		if m.manualCompacting || m.tokenCount != 4_000 {
			t.Fatalf("unexpected post-compact state: compacting=%v tokens=%d", m.manualCompacting, m.tokenCount)
		}
		message := m.messages[len(m.messages)-1].content
		for _, want := range []string{"12000 → 4000 tokens", "saved 8000 (66.7%)", "42 → 7 messages"} {
			if !strings.Contains(message, want) {
				t.Fatalf("success message %q missing %q", message, want)
			}
		}
	})

	t.Run("failure", func(t *testing.T) {
		m, _, _ := newTestModel(t)
		m.manualCompacting = true
		m.finishManualCompact(manualCompactFinishedMsg{sessionID: m.sessionID, err: errors.New("provider unavailable")})
		if m.manualCompacting {
			t.Fatal("failure did not clear compacting state")
		}
		if message := m.messages[len(m.messages)-1].content; !strings.Contains(message, "compact: provider unavailable") {
			t.Fatalf("unexpected failure message: %q", message)
		}
	})
}

func TestFinishManualCompactRejectsStaleSession(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.manualCompacting = true
	m.finishManualCompact(manualCompactFinishedMsg{sessionID: "other-session"})
	if m.manualCompacting {
		t.Fatal("stale completion did not clear compacting state")
	}
	if message := m.messages[len(m.messages)-1].content; !strings.Contains(message, "session changed") {
		t.Fatalf("unexpected stale-session message: %q", message)
	}
}
