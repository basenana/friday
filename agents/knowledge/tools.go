package knowledge

import (
	"context"
	"fmt"
	"github.com/basenana/friday/types"
	"strconv"

	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/tools"
)

func storehouseTools(store storehouse.Storehouse, chunkType string, metadata map[string]string) []*tools.Tool {
	common := []*tools.Tool{
		tools.NewTool("save_knowledge_to_base",
			tools.WithDescription("Save knowledge card into the knowledge base for subsequent recall and utilization."),
			tools.WithString("card_content",
				tools.Required(),
				tools.Description("The content of the knowledge card, Do not exceed 500 words"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				content, ok := request.Arguments["card_content"].(string)
				if !ok || content == "" {
					return nil, fmt.Errorf("missing required parameter: card_content")
				}

				chunks, err := store.SaveChunks(ctx, &types.Chunk{
					ID:       "",
					Type:     chunkType,
					Metadata: metadata,
					Content:  content,
				})

				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				if len(chunks) != 1 {
					return tools.NewToolResultError("Expected 1 chunk but got " + strconv.Itoa(len(chunks))), nil
				}

				return tools.NewToolResultText(fmt.Sprintf("chunk %s saved", chunks[0].ID)), nil
			}),
		),
	}

	common = append(common, store.SearchTools()...)
	return common
}
