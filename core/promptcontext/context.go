package promptcontext

import (
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

type Block string

const (
	ProjectInstructions Block = "project_instructions"
	WorktreeContext     Block = "worktree_context"
	ApprovedPlan        Block = "approved_plan"

	contextMetadataKey   = "friday.context"
	contextMetadataValue = "builtin"
	blockMetadataPrefix  = "friday.context.block."
)

var blockOrder = []Block{ProjectInstructions, WorktreeContext, ApprovedPlan}

// SetBlock upserts one request-local section of Friday's leading built-in
// context message. Sections are kept in message metadata so independent hooks
// never need to parse or rewrite one another's prompt text.
func SetBlock(req providers.Request, block Block, content string) {
	if req == nil {
		return
	}

	history := append([]types.Message(nil), req.History()...)
	if len(history) == 0 || !isBuiltin(history[0]) {
		history = append([]types.Message{{
			Role: types.RoleAgent,
			Metadata: map[string]string{
				contextMetadataKey: contextMetadataValue,
			},
		}}, history...)
	}

	message := history[0]
	message.Metadata = cloneMetadata(message.Metadata)
	key := blockMetadataPrefix + string(block)
	if strings.TrimSpace(content) == "" {
		delete(message.Metadata, key)
	} else {
		message.Metadata[key] = strings.TrimSpace(content)
	}
	message.Content = render(message.Metadata)

	if message.Content == "" {
		history = history[1:]
	} else {
		history[0] = message
	}
	req.SetHistory(history)
}

func isBuiltin(message types.Message) bool {
	return message.Role == types.RoleAgent && message.Metadata[contextMetadataKey] == contextMetadataValue
}

func render(metadata map[string]string) string {
	blocks := make([]string, 0, len(blockOrder))
	for _, block := range blockOrder {
		if content := strings.TrimSpace(metadata[blockMetadataPrefix+string(block)]); content != "" {
			blocks = append(blocks, content)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func cloneMetadata(metadata map[string]string) map[string]string {
	cloned := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		cloned[key] = value
	}
	cloned[contextMetadataKey] = contextMetadataValue
	return cloned
}
