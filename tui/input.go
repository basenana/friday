package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basenana/friday/core/types"
)

const helpText = `Available commands:
  /clear   Clear the conversation view
  /new     Start a new session
  /quit    Exit Friday TUI (Ctrl+C also works when idle)
  /help    Show this message

Keys:
  Enter     Send message
  Ctrl+C    Cancel running task, or quit when idle
  Esc       Cancel running task, or clear input when idle
  PgUp/Dn   Scroll history
  Ctrl+U/D  Half-page scroll
  Wheel     Scroll history
`

// handleSlash dispatches slash commands. Returns updated model + next cmd.
func (m *model) handleSlash(text string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return m, nil
	}
	switch parts[0] {
	case "/clear":
		m.messages = nil
		m.invalidateRendered()
		return m, nil

	case "/new":
		newID := types.NewID()
		if err := m.ensureSession(newID); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return m, nil
		}

		// Shutdown current actor + session, start fresh.
		m.closeSubscription()
		m.registry.Shutdown(m.sessionID)
		m.sessionID = newID
		m.messages = nil
		m.tokenCount = 0
		m.iteration = 0
		m.running = false
		m.textBuf.Reset()
		m.reasonBuf.Reset()
		m.toolCalls = make(map[string]*toolCallBlock)
		if err := m.bindSession(newID); err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
			return m, nil
		}
		return m, m.waitForActorEvent()

	case "/quit", "/exit":
		m.quitting = true
		m.closeSubscription()
		m.registry.Shutdown(m.sessionID)
		return m, tea.Quit

	case "/help":
		m.appendBlock(chatBlock{kind: blockAssistant, content: helpText})
		return m, nil

	default:
		m.appendBlock(chatBlock{kind: blockError, content: "unknown command: " + parts[0] + " (try /help)"})
		return m, nil
	}
}
