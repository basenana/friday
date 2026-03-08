package anthropics

import (
	"encoding/json"
	"strings"
)

func extractJSON(content string, model any) error {
	content = strings.TrimSpace(content)

	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")

	content = strings.TrimSpace(content)

	return json.Unmarshal([]byte(content), model)
}
