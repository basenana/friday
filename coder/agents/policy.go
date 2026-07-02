package agents

import "github.com/basenana/friday/core/tools"

// ToolPolicy controls which tools an agent is permitted to use.
//
// When Allow is non-empty, only tools whose Name appears in Allow are kept
// (whitelist mode). Otherwise, tools whose Name appears in Deny are removed
// (blacklist mode). An empty ToolPolicy (both Allow and Deny empty) keeps
// every tool.
type ToolPolicy struct {
	Allow []string
	Deny  []string
}

// Apply returns the subset of all that satisfies the policy.
// The input slice is not mutated; a new slice is returned when filtering occurs.
func (p ToolPolicy) Apply(all []*tools.Tool) []*tools.Tool {
	if len(p.Allow) == 0 && len(p.Deny) == 0 {
		return all
	}

	if len(p.Allow) > 0 {
		allowed := make(map[string]struct{}, len(p.Allow))
		for _, n := range p.Allow {
			allowed[n] = struct{}{}
		}
		out := make([]*tools.Tool, 0, len(all))
		for _, t := range all {
			if _, ok := allowed[t.Name]; ok {
				out = append(out, t)
			}
		}
		return out
	}

	denied := make(map[string]struct{}, len(p.Deny))
	for _, n := range p.Deny {
		denied[n] = struct{}{}
	}
	out := make([]*tools.Tool, 0, len(all))
	for _, t := range all {
		if _, ok := denied[t.Name]; !ok {
			out = append(out, t)
		}
	}
	return out
}
