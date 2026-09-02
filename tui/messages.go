// Package tui implements an interactive Bubble Tea chat client for Friday.
//
// It consumes AG-UI events from a core/actor Actor and renders them as a
// Claude-Code-style terminal UI: streaming markdown text, reasoning blocks,
// bordered tool call boxes, spinner, and a status bar.
package tui

import (
	"github.com/charmbracelet/bubbletea"

	"github.com/basenana/friday/core/actor/events"
)

// actorEventMsg wraps an events.Event delivered into the Bubble Tea loop.
type actorEventMsg struct {
	token uint64
	event events.Event
}

// actorDoneMsg is emitted when the actor's subscription channel closes.
type actorDoneMsg struct {
	token uint64
}

// waitForActorEvent returns a tea.Cmd that reads one event from the actor's
// subscription channel. Bubble Tea runs the returned func on its own goroutine;
// blocking here is expected and does not stall the UI. The cmd re-arms itself
// by being re-issued from Update after each event.
func waitForActorEvent(evts <-chan events.Event, token uint64) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-evts
		if !ok {
			return actorDoneMsg{token: token}
		}
		return actorEventMsg{token: token, event: evt}
	}
}
