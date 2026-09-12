package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/types"
)

func TestCompactEventPayloads(t *testing.T) {
	finish := CompactFinishData("summary", CompactTriggerManual, 90_000, 30_000, 1500*time.Millisecond)
	wantFinish := map[string]string{
		"method": "summary", "trigger": "manual", "status": "ok",
		"tokens_before": "90000", "tokens_after": "30000", "saved_tokens": "60000", "duration_ms": "1500",
	}
	for key, want := range wantFinish {
		if got := finish[key]; got != want {
			t.Fatalf("finish[%q] = %q, want %q", key, got, want)
		}
	}

	skip := CompactSkipData("micro_compact", CompactTriggerSoft, "not_beneficial", 80_000, 30*time.Millisecond)
	if skip["reason"] != "not_beneficial" || skip["tokens_before"] != "80000" || skip["duration_ms"] != "30" {
		t.Fatalf("unexpected skip payload: %#v", skip)
	}
	if _, ok := skip["tokens_after"]; ok {
		t.Fatalf("skip must not contain tokens_after: %#v", skip)
	}

	fail := CompactFailData("summary", CompactTriggerOverflow, 120_000, 800*time.Millisecond, errors.New("provider failed"))
	if fail["status"] != "failed" || fail["error"] != "provider failed" || fail["duration_ms"] != "800" {
		t.Fatalf("unexpected failure payload: %#v", fail)
	}
	if _, ok := fail["saved_tokens"]; ok {
		t.Fatalf("failure must not contain saved_tokens: %#v", fail)
	}
}

func TestCompactFailDataTruncatesUnicodeByRunes(t *testing.T) {
	message := strings.Repeat("错", compactEventMaxErrorRunes+100)
	got := CompactFailData("summary", CompactTriggerHard, 1_000, time.Millisecond, errors.New(message))["error"]
	if len([]rune(got)) != compactEventMaxErrorRunes {
		t.Fatalf("error runes = %d, want %d", len([]rune(got)), compactEventMaxErrorRunes)
	}
	if !strings.HasSuffix(got, compactErrorTruncSuffix) {
		t.Fatalf("truncated error missing suffix: %q", got)
	}
}

func TestCompactHistoryPublishesStatisticsAndTrigger(t *testing.T) {
	sess := New("sess-compact-events", nil, WithHistory(
		types.Message{Role: types.RoleUser, Content: strings.Repeat("old context ", 100)},
		types.Message{Role: types.RoleAssistant, Content: "response"},
	))
	eventCh, unsubscribe := sess.SubscribeEvents()
	defer unsubscribe()

	if err := sess.CompactHistoryWithTrigger(context.Background(), CompactTriggerPlanHandoff); err != nil {
		t.Fatalf("CompactHistoryWithTrigger() error = %v", err)
	}
	events := drainCompactEvents(eventCh)
	finish := findCompactEvent(events, types.EventCompactFinish, "truncate")
	if finish == nil {
		t.Fatalf("missing truncate finish event: %#v", events)
	}
	for _, field := range []string{"trigger", "status", "tokens_before", "tokens_after", "saved_tokens", "duration_ms"} {
		if _, ok := finish.Data[field]; !ok {
			t.Fatalf("finish payload missing %q: %#v", field, finish.Data)
		}
	}
	if finish.Data["trigger"] != "plan_handoff" || finish.Data["status"] != "ok" {
		t.Fatalf("unexpected finish payload: %#v", finish.Data)
	}
}

func TestCompactHistoryPublishesFailure(t *testing.T) {
	sess := New("sess-compact-fail", &fakeCompactClient{completionErr: errors.New("llm boom")}, WithHistory(
		types.Message{Role: types.RoleUser, Content: "question"},
		types.Message{Role: types.RoleAssistant, Content: "answer"},
	))
	eventCh, unsubscribe := sess.SubscribeEvents()
	defer unsubscribe()

	if err := sess.CompactHistoryWithTrigger(context.Background(), CompactTriggerOverflow); err == nil {
		t.Fatal("expected compaction error")
	}
	finish := findCompactEvent(drainCompactEvents(eventCh), types.EventCompactFinish, "summary")
	if finish == nil || finish.Data["status"] != "failed" || finish.Data["trigger"] != "overflow" || finish.Data["error"] == "" {
		t.Fatalf("unexpected failure event: %#v", finish)
	}
}

type failingCompactWriter struct{ err error }

func (w failingCompactWriter) AppendMessages(string, ...types.Message) error  { return nil }
func (w failingCompactWriter) ReplaceMessages(string, ...types.Message) error { return w.err }

func TestCompactHistoryPublishesPersistenceFailure(t *testing.T) {
	persistErr := errors.New("disk full")
	sess := New("sess-compact-persist-fail", nil,
		WithMessageWriter(failingCompactWriter{err: persistErr}),
		WithHistory(types.Message{Role: types.RoleUser, Content: "question"}),
	)
	eventCh, unsubscribe := sess.SubscribeEvents()
	defer unsubscribe()

	if err := sess.CompactHistory(context.Background()); !errors.Is(err, persistErr) {
		t.Fatalf("CompactHistory() error = %v, want %v", err, persistErr)
	}
	finish := findCompactEvent(drainCompactEvents(eventCh), types.EventCompactFinish, "truncate")
	if finish == nil || finish.Data["status"] != "failed" || finish.Data["trigger"] != "manual" || finish.Data["error"] != "disk full" {
		t.Fatalf("unexpected persistence failure event: %#v", finish)
	}
}

func drainCompactEvents(ch <-chan types.Event) []types.Event {
	var result []types.Event
	for {
		select {
		case event := <-ch:
			result = append(result, event)
		default:
			return result
		}
	}
}

func findCompactEvent(events []types.Event, eventType types.EventType, method string) *types.Event {
	for i := range events {
		if events[i].Type == eventType && events[i].Data["method"] == method {
			return &events[i]
		}
	}
	return nil
}
