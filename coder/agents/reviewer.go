package agents

import "github.com/basenana/friday/config"

// ReviewerSpec returns the AgentSpec for the code-review subagent.
//
// The reviewer reads the git diff and produces a verdict. It may run bash
// (e.g. to invoke git or go test) but cannot modify files directly.
func ReviewerSpec(model config.ModelConfig) *AgentSpec {
	return &AgentSpec{
		Name:        NameReviewer,
		Description: "Reviews uncommitted changes via git diff and produces a structured verdict (APPROVE / COMMENT / REQUEST_CHANGES) with at most 5 issues. May run bash for git/test commands.",
		Model:       model,
		SystemPrompt: ReviewerSystemPrompt,
		ToolPolicy: ToolPolicy{
			Deny: []string{
				ToolFsWrite,
				ToolFsEdit,
				ToolFsMkdir,
				ToolFsDelete,
				ToolBgTask,
				ToolKillTask,
			},
		},
		MaxLoopTimes: 30,
		Mode:         ModeSubagent,
	}
}
