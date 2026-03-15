package utils

import "strings"

func ExtractJSON(response string) string {
	response = strings.TrimSpace(response)

	if strings.HasPrefix(response, "```json") {
		content := response[7:]
		end := strings.Index(content, "```")
		if end > 0 {
			return strings.TrimSpace(content[:end])
		}
		return strings.TrimSpace(content)
	}
	if strings.HasPrefix(response, "```") {
		content := response[3:]
		end := strings.Index(content, "```")
		if end > 0 {
			return strings.TrimSpace(content[:end])
		}
		return strings.TrimSpace(content)
	}

	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start >= 0 && end > start {
		return response[start : end+1]
	}

	return response
}
