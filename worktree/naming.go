package worktree

import (
	"strings"
	"unicode"
)

func generatedName(requirement string) string {
	return truncateName(slug(requirement), 20)
}

func truncateName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	return strings.TrimRight(name[:limit], "-")
}

func slug(input string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(input)) {
		if unicode.IsLetter(r) && r <= unicode.MaxASCII || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "task"
	}
	return name
}
