// Package tui implements an interactive Bubble Tea chat client for Friday.
//
// It consumes AG-UI events from an actor.Actor and renders them as a
// Claude-Code-style terminal UI: streaming markdown text, reasoning blocks,
// bordered tool call boxes, spinner, and a status bar.
package tui

import (
	"github.com/charmbracelet/bubbletea"

	"github.com/basenana/friday/actor"
)

// actorEventMsg wraps an actor.Event delivered into the Bubble Tea loop.
type actorEventMsg struct {
	token uint64
	event actor.Event
}

// actorDoneMsg is emitted when the actor's outcome channel closes.
type actorDoneMsg struct {
	token uint64
}

// waitForActorEvent returns a tea.Cmd that reads one event from the actor's
// subscription channel. Bubble Tea runs the returned func on its own goroutine;
// blocking here is expected and does not stall the UI. The cmd re-arms itself
// by being re-issued from Update after each event.
func waitForActorEvent(events <-chan actor.Event, token uint64) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-events
		if !ok {
			return actorDoneMsg{token: token}
		}
		return actorEventMsg{token: token, event: evt}
	}
}
