// Package tui implements an interactive Bubble Tea chat client for Friday.
//
// It consumes AG-UI events from the session's actor over the topic event
// bus (agent.<sid>.* topics, bridged by bus/bridge) and renders them as a
// Claude-Code-style terminal UI: streaming markdown text, reasoning blocks,
// bordered tool call boxes, spinner, and a status bar.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

// actorEventMsg wraps an events.Event delivered into the Bubble Tea loop.
type actorEventMsg struct {
	token uint64
	event events.Event
}

// feedClosedMsg is emitted when the model's bus feed is closed (session
// switch or quit). Update ignores it; it only exists to unblock
// waitForActorEvent commands reading the old feed.
type feedClosedMsg struct{}

// waitForActorEvent returns a tea.Cmd that reads one event from the feed.
// Bubble Tea runs the returned func on its own goroutine; blocking here is
// expected and does not stall the UI. The cmd re-arms itself by being
// re-issued from Update after each event.
func waitForActorEvent(f *bus.Feed, token uint64) tea.Cmd {
	return func() tea.Msg {
		select {
		case evt := <-f.Events():
			return actorEventMsg{token: token, event: evt}
		case <-f.Done():
			return feedClosedMsg{}
		}
	}
}
