package common

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/core/types"
)

// RepairToolHistory performs only unambiguous, provider-agnostic cleanup.
// Provider-specific tool-call ordering rules must be enforced by each client.
func RepairToolHistory(history []types.Message) []types.Message {
	if len(history) == 0 {
		return history
	}

	fixed := make([]types.Message, 0, len(history))
	for i := range history {
		msg := history[i]
		if msg.Role == types.RoleAgent {
			msg.Role = types.RoleUser
		}
		if msg.Role == types.RoleTool && msg.ToolResult != nil && strings.TrimSpace(msg.ToolResult.CallID) == "" {
			msg = convertToolResultToText(msg, "")
		}
		if len(fixed) > 0 && shouldMergeAssistantMessages(fixed[len(fixed)-1], msg) {
			mergeAssistantMessages(&fixed[len(fixed)-1], msg)
			continue
		}
		fixed = append(fixed, msg)
	}
	return fixed
}

func shouldMergeAssistantMessages(dst, src types.Message) bool {
	return isPlainAssistantText(dst) && isPlainAssistantText(src)
}

func isPlainAssistantText(msg types.Message) bool {
	return msg.Role == types.RoleAssistant &&
		msg.Image == nil &&
		len(msg.ToolCalls) == 0 &&
		msg.ToolResult == nil &&
		msg.Reasoning == "" &&
		msg.ReasoningSignature == "" &&
		msg.RedactedThinking == "" &&
		len(msg.Metadata) == 0 &&
		msg.Time.IsZero()
}

func mergeAssistantMessages(dst *types.Message, src types.Message) {
	if src.Content != "" {
		if dst.Content != "" {
			dst.Content += "\n" + src.Content
		} else {
			dst.Content = src.Content
		}
	}
}

func convertToolResultToText(msg types.Message, toolName string) types.Message {
	converted := msg
	converted.Role = types.RoleUser
	converted.Content = formatToolResultFallback(msg.ToolResult, toolName)
	converted.ToolResult = nil
	return converted
}

func formatToolResultFallback(result *types.ToolResult, toolName string) string {
	if result == nil {
		return "[historical tool result omitted]"
	}
	if toolName == "" {
		toolName = "unknown_tool"
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return fmt.Sprintf("[historical tool result omitted for tool call: %s]", toolName)
	}
	return fmt.Sprintf("[historical tool result for tool call %s] %s", toolName, content)
}
