// Package tui implements an interactive Bubble Tea chat client for Friday.
//
// It consumes AG-UI events from the session's actor over the topic event
// bus (agent.<sid>.* topics, bridged by bus/bridge) and renders them as a
// Claude-Code-style terminal UI: streaming markdown text, reasoning blocks,
// bordered tool call boxes, spinner, and a status bar.
package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/bus"
	codebasepkg "github.com/basenana/friday/coder/codebase"
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

type codebaseActivitiesMsg struct {
	token      uint64
	activities []codebasepkg.Activity
	dropped    uint64
}
type codebaseFeedClosedMsg struct{ token uint64 }
type codebaseExpireMsg struct {
	operationID string
	revision    uint64
}
type codebaseCommandMsg struct {
	content string
	err     error
}

type sessionSwitchPreparedMsg struct {
	sessionID  string
	transition *codebasepkg.SessionTransition
	err        error
}

func waitForCodebaseActivity(f *bus.Feed, token uint64) tea.Cmd {
	return func() tea.Msg {
		select {
		case evt := <-f.Events():
			activities := make([]codebasepkg.Activity, 0, 32)
			if activity, ok := decodeCodebaseActivity(evt); ok {
				activities = append(activities, activity)
			}
			timer := time.NewTimer(4 * time.Millisecond)
			defer timer.Stop()
			for len(activities) < 32 {
				select {
				case next := <-f.Events():
					if activity, ok := decodeCodebaseActivity(next); ok {
						activities = append(activities, activity)
					}
				case <-timer.C:
					return codebaseActivitiesMsg{token: token, activities: activities, dropped: f.Dropped()}
				case <-f.Done():
					return codebaseActivitiesMsg{token: token, activities: activities, dropped: f.Dropped()}
				}
			}
			return codebaseActivitiesMsg{token: token, activities: activities, dropped: f.Dropped()}
		case <-f.Done():
			return codebaseFeedClosedMsg{token: token}
		}
	}
}

func decodeCodebaseActivity(evt events.Event) (codebasepkg.Activity, bool) {
	var activity codebasepkg.Activity
	return activity, events.DecodePayload(evt, &activity) == nil
}

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

// mergeAdjacentStreamEvents merges adjacent events from the same stream
// (text deltas, reasoning deltas) into one event per run. Runs are merged
// in linear time: every event is decoded once and the run is marshaled
// once, instead of re-decoding and re-marshaling a growing payload per
// pair.
func mergeAdjacentStreamEvents(input []events.Event) []events.Event {
	if len(input) < 2 {
		return input
	}
	merged := make([]events.Event, 0, len(input))
	for i := 0; i < len(input); {
		evt := input[i]
		if !streamMergeable(evt) {
			merged = append(merged, evt)
			i++
			continue
		}
		var acc strings.Builder
		if !decodeStreamPayload(evt, &acc) {
			merged = append(merged, evt)
			i++
			continue
		}
		j := i + 1
		for j < len(input) && streamMergeable(input[j]) && sameStreamKey(evt, input[j]) {
			if !decodeStreamPayload(input[j], &acc) {
				break
			}
			j++
		}
		if j > i+1 {
			evt = withStreamPayload(evt, acc.String())
			evt.Seq, evt.Timestamp = input[j-1].Seq, input[j-1].Timestamp
		}
		merged = append(merged, evt)
		i = j
	}
	return merged
}

// streamMergeable reports whether evt is a stream delta that participates in
// adjacent-merge runs.
func streamMergeable(evt events.Event) bool {
	switch evt.Type {
	case events.KindTextMessageContent:
		return true
	case events.KindCustom:
		return evt.Name == events.CustomReasoningDelta
	default:
		return false
	}
}

// sameStreamKey reports whether two events belong to the same stream run.
func sameStreamKey(a, b events.Event) bool {
	return a.Type == b.Type && a.Name == b.Name &&
		a.ActorID == b.ActorID && a.RunID == b.RunID && a.MessageID == b.MessageID
}

// decodeStreamPayload decodes evt's delta content and appends it to acc.
func decodeStreamPayload(evt events.Event, acc *strings.Builder) bool {
	switch evt.Type {
	case events.KindTextMessageContent:
		var d events.TextMessageContentData
		if events.DecodePayload(evt, &d) != nil {
			return false
		}
		acc.WriteString(d.Content)
	case events.KindCustom:
		var d events.ReasoningDeltaBody
		if events.DecodePayload(evt, &d) != nil {
			return false
		}
		acc.WriteString(d.Content)
	default:
		return false
	}
	return true
}

// withStreamPayload rebuilds evt with the accumulated delta content.
func withStreamPayload(evt events.Event, content string) events.Event {
	if evt.Type == events.KindCustom {
		return evt.WithPayload(events.ReasoningDeltaBody{Content: content})
	}
	return evt.WithPayload(events.TextMessageContentData{Content: content})
}
