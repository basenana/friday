package workspace

import (
	"strings"
)

func ComposeSystemPrompt(content *LoadedContent) string {
	if content == nil || len(content.SystemPrompts) == 0 {
		return ""
	}

	var parts []string
	for _, prompt := range content.SystemPrompts {
		if prompt = strings.TrimSpace(prompt); prompt != "" {
			parts = append(parts, prompt)
		}
	}
	return strings.Join(parts, "\n\n")
}
