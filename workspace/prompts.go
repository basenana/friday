package workspace

import (
	"fmt"
	"strings"
)

const (
	basePrompts = `<friday_project>
You are Friday, a Unix-philosophy AI agent CLI built by Hypo for terminal users.

Your command is 'friday' — users invoke you with it. Use this command to understand yourself, configure settings, or delegate subtasks to another Friday process. 
Run 'friday --help' to see your capabilities.

All your data resides in your data directory. You may freely explore and use it.
{friday_directories}

</friday_project>
`
)

// ComposeSystemPrompt combines the default prompt with workspace content and paths info
func ComposeSystemPrompt(content *LoadedContent, paths *Paths) string {
	var (
		base      = basePrompts
		pathsInfo string
	)

	if paths != nil {
		pathsInfo = fmt.Sprintf(`
- DataDir: %s — Root data directory for all friday data
- Workspace: %s — Markdown files for agent context (SOUL.md, USER.md, etc.)
- Sessions: %s — Conversation history storage
- Memory: %s — Daily memory logs
- State: %s — Persistent key-value state storage
- Log: %s — Application log file
`, paths.DataDir, paths.Workspace, paths.Sessions, paths.Memory, paths.State, paths.Log)
	} else {
		pathsInfo = "DataDir: ~/.friday — Root data directory for all friday data"
	}
	strings.ReplaceAll(base, "{friday_directories}", pathsInfo)

	if content == nil || len(content.SystemPrompts) == 0 {
		return base
	}

	var parts = []string{base}
	for _, prompt := range content.SystemPrompts {
		prompt = strings.TrimSpace(prompt)
		if prompt != "" {
			parts = append(parts, prompt)
		}
	}

	return strings.Join(parts, "\n\n")
}
