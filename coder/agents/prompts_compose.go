package agents

import "strings"

// ComposeSystemPrompt appends the agent-specific prompt after the shared
// workspace prompt so the specialization is the last stable base instruction.
func ComposeSystemPrompt(workspacePrompt, agentPrompt string) string {
	parts := make([]string, 0, 2)
	if prompt := strings.TrimSpace(workspacePrompt); prompt != "" {
		parts = append(parts, prompt)
	}
	if prompt := strings.TrimSpace(agentPrompt); prompt != "" {
		parts = append(parts, prompt)
	}
	return strings.Join(parts, "\n\n")
}
