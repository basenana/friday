package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

func TestBindSessionObservesCreatedEpoch(t *testing.T) {
	m, _, _ := newTestModel(t)
	waitForCreatedEpoch(t, m.feed)
}

func waitForCreatedEpoch(t *testing.T, feed *bus.Feed) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-feed.Events():
			if evt.Type == events.KindCustom && evt.Name == "status."+bus.StatusCreated {
				return
			}
		case <-deadline:
			t.Fatal("feed did not observe status.created during bind")
		}
	}
}

// TestAgentFeedPreservesCrossTopicOrder verifies the model's bus feed
// receives events in publish order even when publishes interleave topics
// (deltas vs lifecycle markers vs tool results), mirroring how the
// OutBridge publishes from its single goroutine.
func TestAgentFeedPreservesCrossTopicOrder(t *testing.T) {
	m, _, _ := newTestModel(t)
	f := m.feed
	if f == nil {
		t.Fatal("model has no feed after initialModel")
	}
	waitForCreatedEpoch(t, f)

	topics := []string{
		bus.TopicRun(m.sessionID, "started"),
		bus.TopicReplyContent(m.sessionID),
		bus.TopicToolCall(m.sessionID, "bash"),
		bus.TopicToolUse(m.sessionID, "bash"),
		bus.TopicStatus(m.sessionID, "created"),
	}

	var sent []string
	for i := 0; i < 100; i++ {
		for j, tp := range topics {
			name := fmt.Sprintf("evt-%d-%d", i, j)
			sent = append(sent, name)
			evt := events.NewEvent(events.KindCustom, "").WithName(name)
			m.registry.Bus().Publish(tp, bus.Envelope{
				Event:   evt,
				Topic:   tp,
				Session: m.sessionID,
			})
		}
	}

	for _, want := range sent {
		select {
		case evt := <-f.Events():
			if evt.Name != want {
				t.Fatalf("out of order: got %q, want %q", evt.Name, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	f.Close()
}

// TestSendUserTextDroppedRendersError verifies an inbox_dropped status
// envelope (e.g. unknown inbox kind) surfaces as an error block in the
// chat view.
func TestSendUserTextDroppedRendersError(t *testing.T) {
	m, _, _ := newTestModel(t)

	evt := events.NewEvent(events.KindCustom, "").WithName("bogus.kind")
	m.registry.Bus().Publish(bus.TopicInbox(m.sessionID), bus.Envelope{
		Event:   evt,
		Topic:   bus.TopicInbox(m.sessionID),
		Session: m.sessionID,
		From:    "test",
	})

	for {
		var e events.Event
		select {
		case e = <-m.feed.Events():
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for inbox_dropped event on the feed")
		}
		m.handleActorEvent(e)
		if n := len(m.messages); n > 0 {
			if m.messages[n-1].kind != blockError {
				t.Fatalf("last block kind = %v, want blockError", m.messages[n-1].kind)
			}
			if want := "message dropped: unknown inbox kind: bogus.kind"; m.messages[n-1].content != want {
				t.Fatalf("error block = %q, want %q", m.messages[n-1].content, want)
			}
			return
		}
	}
}
