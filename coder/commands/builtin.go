package commands

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/core/types"
)

// --- /clear ---

type clearCmd struct{}

func (clearCmd) Name() string        { return "clear" }
func (clearCmd) Aliases() []string   { return nil }
func (clearCmd) Description() string { return "Clear the terminal and start a new session" }
func (clearCmd) Execute(_ *Context) (*Result, error) {
	return &Result{ClearMessages: true, SwitchSession: types.NewID()}, nil
}

// --- /new ---

type newCmd struct{}

func (newCmd) Name() string        { return "new" }
func (newCmd) Aliases() []string   { return nil }
func (newCmd) Description() string { return "Start a new session" }
func (newCmd) Execute(_ *Context) (*Result, error) {
	return &Result{SwitchSession: types.NewID(), PreserveTranscript: true}, nil
}

// --- /quit ---

type quitCmd struct{}

func (quitCmd) Name() string        { return "quit" }
func (quitCmd) Aliases() []string   { return []string{"exit"} }
func (quitCmd) Description() string { return "Exit Friday TUI (Ctrl+C also works)" }
func (quitCmd) Execute(_ *Context) (*Result, error) {
	return &Result{Quit: true}, nil
}

// --- /help ---

type helpCmd struct {
	registry *Registry
}

func (h helpCmd) Name() string        { return "help" }
func (h helpCmd) Aliases() []string   { return nil }
func (h helpCmd) Description() string { return "Show available commands" }
func (h helpCmd) Execute(_ *Context) (*Result, error) {
	return &Result{Message: buildHelpText(h.registry)}, nil
}

func buildHelpText(reg *Registry) string {
	var b strings.Builder
	b.WriteString("## Available commands\n\n")
	for _, cmd := range reg.List() {
		aliases := append([]string(nil), cmd.Aliases()...)
		name := "/" + cmd.Name()
		if len(aliases) > 0 {
			for i, a := range aliases {
				aliases[i] = "/" + a
			}
			name = name + " (" + strings.Join(aliases, ", ") + ")"
		}
		b.WriteString(fmt.Sprintf("- `%s` — %s\n", name, cmd.Description()))
	}
	b.WriteString("\n## Keys\n\n")
	b.WriteString("- `Enter` — Send; while running, steer the active task\n")
	b.WriteString("- `Tab` — Complete a command; while running, queue input\n")
	b.WriteString("- `Ctrl+J` — Insert a newline\n")
	b.WriteString("- `Ctrl+G` — Edit the prompt with `VISUAL`/`EDITOR`\n")
	b.WriteString("- `Ctrl+R` — Search prompt history\n")
	b.WriteString("- `Ctrl+L` — Clear the terminal view, keeping the session\n")
	b.WriteString("- `Ctrl+C` — Quit\n")
	b.WriteString("- `Esc` — Cancel current task or close the active popup\n")
	b.WriteString("- `PgUp`/`PgDn`, `Ctrl+U`/`Ctrl+D` — Scroll history\n")
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
