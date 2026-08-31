package actor

import "github.com/basenana/friday/core/actor/events"

// IsTerminal reports whether an event marks the end of a turn.
// Terminal events are never dropped by the EventStream: when a
// subscriber's buffer is full, non-terminal queued events are evicted
// to make room for a terminal so consumers always learn how the turn
// ended.
func IsTerminal(evt events.Event) bool {
	switch evt.Type {
	case events.KindRunFinished, events.KindRunError:
		return true
	default:
		return false
	}
}
