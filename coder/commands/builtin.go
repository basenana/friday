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
func (clearCmd) Metadata() Metadata {
	return Metadata{Usage: "/clear", Category: "Session", Policy: PolicyDeferred}
}
func (clearCmd) Execute(_ *Context) (*Result, error) {
	return ResultOf(ClearSessionAction{SessionID: types.NewID()}), nil
}

// --- /quit ---

type quitCmd struct{}

func (quitCmd) Name() string        { return "quit" }
func (quitCmd) Aliases() []string   { return []string{"exit"} }
func (quitCmd) Description() string { return "Exit Friday TUI (Ctrl+C also works)" }
func (quitCmd) Metadata() Metadata {
	return Metadata{Usage: "/quit", Category: "Session", Policy: PolicyDeferred}
}
func (quitCmd) Execute(_ *Context) (*Result, error) {
	return ResultOf(QuitAction{}), nil
}

// --- /help ---

type helpCmd struct {
	registry *Registry
}

func (h helpCmd) Name() string        { return "help" }
func (h helpCmd) Aliases() []string   { return nil }
func (h helpCmd) Description() string { return "Show available commands" }
func (h helpCmd) Metadata() Metadata {
	return Metadata{Usage: "/help [command]", Category: "Info", Policy: PolicyImmediate}
}
func (h helpCmd) Execute(ctx *Context) (*Result, error) {
	if ctx != nil && len(ctx.Args) > 0 {
		name := strings.TrimPrefix(strings.ToLower(ctx.Args[0]), "/")
		if cmd, ok := h.registry.Lookup(name); ok {
			meta := CommandMetadata(cmd)
			aliases := cmd.Aliases()
			aliasText := ""
			if len(aliases) > 0 {
				aliasText = "\nAliases: /" + strings.Join(aliases, ", /")
			}
			return MessageResult(fmt.Sprintf("## %s\n\n%s\n\nUsage: `%s`%s\nPolicy: `%s`", "/"+cmd.Name(), cmd.Description(), meta.Usage, aliasText, meta.Policy)), nil
		}
		return MessageResult("unknown command: /" + name), nil
	}
	return MessageResult(buildHelpText(h.registry)), nil
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
		meta := CommandMetadata(cmd)
		b.WriteString(fmt.Sprintf("- `%s` — %s · `%s`\n", name, cmd.Description(), meta.Usage))
	}
	b.WriteString("\n## Keys\n\n")
	b.WriteString("- `Enter` — Send; during Loop, send after the current task; otherwise steer the active task\n")
	b.WriteString("- `Tab` — Complete a command; while running, send input after the current task\n")
	b.WriteString("- `Ctrl+J` — Insert a newline\n")
	b.WriteString("- `Ctrl+G` — Edit the prompt with `VISUAL`/`EDITOR`\n")
	b.WriteString("- `Ctrl+R` — Search prompt history\n")
	b.WriteString("- `Shift+Tab` — Toggle Default/Plan Mode while idle\n")
	b.WriteString("- `Ctrl+L` — Clear the terminal view, keeping the session\n")
	b.WriteString("- `Ctrl+C` — Quit\n")
	b.WriteString("- `Esc` — Cancel current task or close the active popup\n")
	b.WriteString("- `PgUp`/`PgDn`, `Ctrl+U`/`Ctrl+D` — Scroll history\n")
	return b.String()
}

// RegisterBuiltins registers the simple builtin commands into reg. Collaboration
// commands such as /plan are registered separately via RegisterAgentCommands.
func RegisterBuiltins(reg *Registry) {
	if reg == nil {
		return
	}
	reg.Register(clearCmd{})
	reg.Register(quitCmd{})
	reg.Register(helpCmd{registry: reg})
}
