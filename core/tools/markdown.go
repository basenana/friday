package tools

import "strings"

// MarkdownTitle returns the first Markdown heading or fallback when none exists.
func MarkdownTitle(markdown, fallback string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		body := strings.TrimLeft(line, "#")
		level := len(line) - len(body)
		if level == 0 || level > 6 || body == "" || (body[0] != ' ' && body[0] != '\t') {
			continue
		}
		title := strings.TrimSpace(strings.TrimRight(body, "#"))
		if title != "" {
			return title
		}
	}
	return fallback
}
