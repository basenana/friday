package agents

import "github.com/basenana/friday/config"

// AgentMode controls how an agent is executed.
type AgentMode int

const (
	// ModeSubagent forks a child session, runs the agent, and returns a report
	// to the parent session. Used by /plan, /review, /advisor and the explore tool.
	ModeSubagent AgentMode = iota
	// ModeInline runs in the current session (reserved for future use).
	ModeInline
)

// AgentSpec is a declarative description of a named agent. It is consumed by
// ClientFactory to build concrete agents.Agent instances with the right
// provider client, system prompt, and filtered tool set.
type AgentSpec struct {
	Name         string
	Description  string
	Model        config.ModelConfig // zero value = inherit primary client
	SystemPrompt string
	ToolPolicy   ToolPolicy
	MaxLoopTimes int
	Mode         AgentMode
}
