package commands

import "strings"

// Registry stores commands keyed by Name (case-sensitive) and resolves aliases.
type Registry struct {
	commands map[string]Command
	aliases  map[string]string // alias → canonical Name
}

func NewRegistry() *Registry {
	return &Registry{commands: make(map[string]Command), aliases: make(map[string]string)}
}

func (r *Registry) Register(cmd Command) {
	if cmd == nil {
		return
	}
	name := cmd.Name()
	if name == "" {
		return
	}
	r.commands[name] = cmd
	for _, alias := range cmd.Aliases() {
		r.aliases[alias] = name
	}
}

// Lookup resolves a name or alias to its command. Returns (nil, false) when
// not found. Lookup is case-insensitive to be forgiving at the prompt.
func (r *Registry) Lookup(name string) (Command, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if cmd, ok := r.commands[name]; ok {
		return cmd, true
	}
	if canonical, ok := r.aliases[name]; ok {
		cmd, ok := r.commands[canonical]
		return cmd, ok
	}
	return nil, false
}

func (r *Registry) List() []Command {
	out := make([]Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		out = append(out, cmd)
	}
	return out
}
