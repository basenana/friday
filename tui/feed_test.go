package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

func TestWaitForActorEventBatchesBurst(t *testing.T) {
	b := eventbus.NewBus()
	feed := bus.SubscribeAgentFeed(b, "batch")
	t.Cleanup(func() {
		feed.Close()
		b.Wait()
	})

	cmd := waitForActorEvent(feed, 42)
	for i := 0; i < 100; i++ {
		b.Publish(bus.TopicReplyContent("batch"), bus.Envelope{
			Event: events.NewEvent(events.KindTextMessageContent, "run").
				WithMessageID("message").
				WithPayload(events.TextMessageContentData{Content: fmt.Sprintf("%d", i)}),
			Session: "batch",
			Seq:     uint64(i + 1),
		})
	}

	msg, ok := cmd().(actorEventsMsg)
	if !ok {
		t.Fatalf("message type = %T, want actorEventsMsg", msg)
	}
	if msg.token != 42 || len(msg.events) != 100 {
		t.Fatalf("batch token/events = %d/%d, want 42/100", msg.token, len(msg.events))
	}
	if got := mergeAdjacentStreamEvents(msg.events); len(got) != 1 {
		t.Fatalf("merged events = %d, want 1", len(got))
	}
}

func TestMergeAdjacentStreamEventsPreservesBarriers(t *testing.T) {
	text := func(content string) events.Event {
		return events.NewEvent(events.KindTextMessageContent, "run").
			WithMessageID("message").
			WithPayload(events.TextMessageContentData{Content: content})
	}
	reasoning := func(content string) events.Event {
		return events.NewEvent(events.KindCustom, "run").
			WithName(events.CustomReasoningDelta).
			WithMessageID("reasoning").
			WithPayload(events.ReasoningDeltaBody{Content: content})
	}
	barrier := events.NewEvent(events.KindToolCallStart, "run").WithPayload(events.ToolCallStartData{
		ToolCallID: "tool", ToolName: "shell",
	})

	got := mergeAdjacentStreamEvents([]events.Event{
		text("a"), text("b"), barrier, text("c"), text("d"), reasoning("e"), reasoning("f"),
	})
	if len(got) != 4 {
		t.Fatalf("merged event count = %d, want 4", len(got))
	}
	var first, second events.TextMessageContentData
	if err := events.DecodePayload(got[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := events.DecodePayload(got[2], &second); err != nil {
		t.Fatal(err)
	}
	var thought events.ReasoningDeltaBody
	if err := events.DecodePayload(got[3], &thought); err != nil {
		t.Fatal(err)
	}
	if first.Content != "ab" || got[1].Type != events.KindToolCallStart || second.Content != "cd" || thought.Content != "ef" {
		t.Fatalf("merged stream = %#v / %#v / %#v / %#v", first, got[1], second, thought)
	}
}

func TestAgentFeedReportsOverflow(t *testing.T) {
	b := eventbus.NewBus()
	feed := bus.SubscribeAgentFeed(b, "overflow")
	t.Cleanup(func() {
		feed.Close()
		b.Wait()
	})
	if got := cap(feed.Events()); got != 512 {
		t.Fatalf("feed channel capacity = %d, want 512", got)
	}

	for i := 0; i < 2048; i++ {
		b.Publish(bus.TopicReplyContent("overflow"), bus.Envelope{
			Event:   events.NewEvent(events.KindTextMessageContent, "run"),
			Session: "overflow",
			Seq:     uint64(i + 1),
		})
	}
	deadline := time.Now().Add(2 * time.Second)
	for feed.Dropped() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if feed.Dropped() == 0 {
		t.Fatal("feed did not report overflow")
	}
}

func TestObserveActorEventsDetectsSequenceGap(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.resetEventTracking()
	m.observeActorEvents([]events.Event{
		{Type: events.KindRunStarted, ActorID: "actor", Seq: 10},
		{Type: events.KindTextMessageContent, ActorID: "actor", Seq: 12},
	}, 0)
	if !m.transcriptDesynced {
		t.Fatal("sequence gap did not mark transcript desynchronized")
	}
	if m.lastEventSeq != 12 {
		t.Fatalf("last sequence = %d, want 12", m.lastEventSeq)
	}
}

func TestHighVolumeStreamBatchesPreserveContent(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.messages = nil
	m.resetStreaming()
	m.resetEventTracking()

	const (
		reasoningChunks = 4978
		textChunks      = 1192
	)
	stream := make([]events.Event, 0, reasoningChunks+textChunks+3)
	seq := int64(1)
	add := func(evt events.Event) {
		evt.ActorID = "actor-volume"
		evt.Seq = seq
		seq++
		stream = append(stream, evt)
	}
	add(events.NewEvent(events.KindRunStarted, "run-volume"))
	for i := 0; i < reasoningChunks; i++ {
		add(events.NewEvent(events.KindCustom, "run-volume").
			WithName(events.CustomReasoningDelta).
			WithMessageID("reasoning-volume").
			WithPayload(events.ReasoningDeltaBody{Content: "思"}))
	}
	add(events.NewEvent(events.KindTextMessageStart, "run-volume").WithMessageID("message-volume"))
	for i := 0; i < textChunks; i++ {
		add(events.NewEvent(events.KindTextMessageContent, "run-volume").
			WithMessageID("message-volume").
			WithPayload(events.TextMessageContentData{Content: "文"}))
	}
	add(events.NewEvent(events.KindTextMessageEnd, "run-volume").WithMessageID("message-volume"))

	for start := 0; start < len(stream); start += actorEventBatchLimit {
		end := min(start+actorEventBatchLimit, len(stream))
		batch := stream[start:end]
		m.observeActorEvents(batch, 0)
		for _, evt := range mergeAdjacentStreamEvents(batch) {
			m.handleActorEvent(evt)
		}
	}
	if m.transcriptDesynced {
		t.Fatal("complete high-volume stream was marked desynchronized")
	}
	if len(m.messages) != 2 {
		t.Fatalf("message blocks = %d, want reasoning and assistant", len(m.messages))
	}
	if got, want := m.messages[0].content, strings.Repeat("思", reasoningChunks); got != want {
		t.Fatalf("reasoning content length = %d, want %d runes", len([]rune(got)), reasoningChunks)
	}
	if got, want := m.messages[1].content, strings.Repeat("文", textChunks); got != want {
		t.Fatalf("assistant content length = %d, want %d runes", len([]rune(got)), textChunks)
	}
}

func TestTranscriptReconciliationReplacesCorruptedLiveContent(t *testing.T) {
	m, _, store := newTestModel(t)
	sink, err := store.OpenEventSink(context.Background(), m.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-reconcile"
	persisted := []events.Event{
		events.NewEvent(events.KindRunStarted, runID),
		events.NewEvent(events.KindTextMessageStart, runID).WithMessageID("message"),
		events.NewEvent(events.KindTextMessageContent, runID).WithMessageID("message").
			WithPayload(events.TextMessageContentData{Content: "complete response"}),
		events.NewEvent(events.KindTextMessageEnd, runID).WithMessageID("message"),
		events.NewEvent(events.KindRunFinished, runID).
			WithPayload(events.RunFinishedData{StopReason: "end_turn"}),
	}
	for _, evt := range persisted {
		if err := sink.Append(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	m.messages = []chatBlock{{kind: blockAssistant, content: "corrupted"}}
	m.transcriptDesynced = true
	m.reconciling = true
	msg := m.reconcileTranscript(persisted[len(persisted)-1].ID)()
	updated, _ := m.Update(msg)
	m = updated.(*model)
	if m.transcriptDesynced || m.reconciling {
		t.Fatalf("reconciliation flags remain set: desynced=%v reconciling=%v", m.transcriptDesynced, m.reconciling)
	}
	var response string
	for _, block := range m.messages {
		if block.kind == blockAssistant {
			response += block.content
		}
		if strings.Contains(block.content, "reconcil") || strings.Contains(block.content, "恢复") {
			t.Fatalf("successful silent recovery added a visible notice: %#v", block)
		}
	}
	if response != "complete response" {
		t.Fatalf("reconciled response = %q, want complete response", response)
	}
}

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
