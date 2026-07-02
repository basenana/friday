package commands

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/core/types"
)

// --- /clear ---

type clearCmd struct{}

func (clearCmd) Name() string         { return "clear" }
func (clearCmd) Aliases() []string    { return nil }
func (clearCmd) Description() string  { return "Clear the conversation view" }
func (clearCmd) Execute(_ *Context) (*Result, error) {
	return &Result{ClearMessages: true}, nil
}

// --- /new ---

type newCmd struct{}

func (newCmd) Name() string         { return "new" }
func (newCmd) Aliases() []string    { return nil }
func (newCmd) Description() string  { return "Start a new session" }
func (newCmd) Execute(_ *Context) (*Result, error) {
	return &Result{SwitchSession: types.NewID()}, nil
}

// --- /quit ---

type quitCmd struct{}

func (quitCmd) Name() string         { return "quit" }
func (quitCmd) Aliases() []string    { return []string{"exit"} }
func (quitCmd) Description() string  { return "Exit Friday TUI (Ctrl+C also works when idle)" }
func (quitCmd) Execute(_ *Context) (*Result, error) {
	return &Result{Quit: true}, nil
}

// --- /help ---

type helpCmd struct {
	registry *Registry
}

func (h helpCmd) Name() string         { return "help" }
func (h helpCmd) Aliases() []string    { return nil }
func (h helpCmd) Description() string  { return "Show available commands" }
func (h helpCmd) Execute(_ *Context) (*Result, error) {
	return &Result{Message: buildHelpText(h.registry)}, nil
}

func buildHelpText(reg *Registry) string {
	var b strings.Builder
	b.WriteString("Available commands:\n")
	for _, cmd := range reg.List() {
		aliases := cmd.Aliases()
		name := "/" + cmd.Name()
		if len(aliases) > 0 {
			for i, a := range aliases {
				aliases[i] = "/" + a
			}
			name = name + " (" + strings.Join(aliases, ", ") + ")"
		}
		b.WriteString(fmt.Sprintf("  %-20s %s\n", name, cmd.Description()))
	}
	b.WriteString("\nKeys:\n")
	b.WriteString("  Enter     Send message\n")
	b.WriteString("  Ctrl+C    Cancel running task, or quit when idle\n")
	b.WriteString("  Esc       Cancel running task, or clear input when idle\n")
	b.WriteString("  PgUp/Dn   Scroll history\n")
	b.WriteString("  Ctrl+U/D  Half-page scroll\n")
	b.WriteString("  Wheel     Scroll history\n")
	return b.String()
}

// RegisterBuiltins registers the simple builtin commands into reg.
// Agent-backed commands (/plan, /review, /advisor) are registered separately
// via RegisterAgentCommands.
func RegisterBuiltins(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(clearCmd{})
	reg.Register(newCmd{})
	reg.Register(quitCmd{})
	reg.Register(helpCmd{registry: reg})
}
