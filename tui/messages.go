// Package tui implements an interactive Bubble Tea chat client for Friday.
//
// It consumes AG-UI events from the session's actor over the topic event
// bus (agent.<sid>.* topics, bridged by bus/bridge) and renders them as a
// Claude-Code-style terminal UI: streaming markdown text, reasoning blocks,
// bordered tool call boxes, spinner, and a status bar.
package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

// actorEventMsg wraps an events.Event delivered into the Bubble Tea loop.
type actorEventMsg struct {
	token uint64
	event events.Event
}

type actorEventsMsg struct {
	token   uint64
	events  []events.Event
	dropped uint64
}

const (
	actorEventBatchLimit  = 512
	actorEventBatchWindow = 16 * time.Millisecond
)

// feedClosedMsg is emitted when the model's bus feed is closed (session
// switch, quit, or an unexpected teardown). The token lets Update distinguish
// an obsolete subscription from the active feed while unblocking the pending
// waitForActorEvent command.
type feedClosedMsg struct{ token uint64 }

// waitForActorEvent returns a tea.Cmd that waits for one event, then drains a
// short bounded batch. Bubble Tea runs the returned func on its own goroutine;
// blocking here does not stall the UI. Update re-arms the command after each
// batch, limiting expensive transcript renders without adding visible latency.
func waitForActorEvent(f *bus.Feed, token uint64) tea.Cmd {
	return func() tea.Msg {
		select {
		case evt := <-f.Events():
			batch := make([]events.Event, 0, actorEventBatchLimit)
			batch = append(batch, evt)
			timer := time.NewTimer(actorEventBatchWindow)
			defer timer.Stop()
			for len(batch) < actorEventBatchLimit {
				select {
				case evt = <-f.Events():
					batch = append(batch, evt)
				case <-timer.C:
					return actorEventsMsg{token: token, events: batch, dropped: f.Dropped()}
				case <-f.Done():
					return actorEventsMsg{token: token, events: batch, dropped: f.Dropped()}
				}
			}
			return actorEventsMsg{token: token, events: batch, dropped: f.Dropped()}
		case <-f.Done():
			return feedClosedMsg{token: token}
		}
	}
}

func mergeAdjacentStreamEvents(input []events.Event) []events.Event {
	if len(input) < 2 {
		return input
	}
	merged := make([]events.Event, 0, len(input))
	for _, evt := range input {
		if len(merged) > 0 && mergeStreamEvent(&merged[len(merged)-1], evt) {
			continue
		}
		merged = append(merged, evt)
	}
	return merged
}

func mergeStreamEvent(previous *events.Event, next events.Event) bool {
	if previous.Type != next.Type || previous.Name != next.Name ||
		previous.ActorID != next.ActorID || previous.RunID != next.RunID ||
		previous.MessageID != next.MessageID {
		return false
	}
	switch next.Type {
	case events.KindTextMessageContent:
		var left, right events.TextMessageContentData
		if events.DecodePayload(*previous, &left) != nil || events.DecodePayload(next, &right) != nil {
			return false
		}
		*previous = previous.WithPayload(events.TextMessageContentData{Content: left.Content + right.Content})
	case events.KindCustom:
		if next.Name != events.CustomReasoningDelta {
			return false
		}
		var left, right events.ReasoningDeltaBody
		if events.DecodePayload(*previous, &left) != nil || events.DecodePayload(next, &right) != nil {
			return false
		}
		*previous = previous.WithPayload(events.ReasoningDeltaBody{Content: left.Content + right.Content})
	default:
		return false
	}
	previous.Timestamp = next.Timestamp
	previous.Seq = next.Seq
	return true
}
