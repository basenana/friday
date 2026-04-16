package session

import (
	"encoding/json"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

// EstimateRequestOverhead returns the approximate prompt-token cost of data
// carried outside session history, such as the system prompt and tool schemas.
func EstimateRequestOverhead(req providers.Request) int64 {
	if req == nil {
		return 0
	}

	var total int64
	if prompt := strings.TrimSpace(req.SystemPrompt()); prompt != "" {
		total += types.Message{Role: types.RoleSystem, Content: prompt}.EstimatedTokens()
	}

	for _, tool := range req.ToolDefines() {
		total += estimateToolTokens(tool)
	}
	return total
}

func estimateToolTokens(tool providers.ToolDefine) int64 {
	if tool == nil {
		return 0
	}

	params, _ := json.Marshal(tool.GetParameters())
	body := tool.GetName() + "\n" + tool.GetDescription() + "\n" + string(params)
	return types.Message{Role: types.RoleSystem, Content: body}.EstimatedTokens()
}

func EstimateHistoryTokens(history []types.Message) int64 {
	var total int64
	for _, msg := range history {
		if msg.Tokens != 0 {
			total += msg.Tokens
			continue
		}
		total += msg.EstimatedTokens()
	}
	return total
}
