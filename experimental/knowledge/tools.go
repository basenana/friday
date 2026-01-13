package knowledge

import (
	"context"
	"fmt"
	"strconv"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils"
)

func SearchTools(store storehouse.Storehouse, chunkTypes ...string) []*tools.Tool {
	result := baseTools(store)

	if vs, ok := store.(storehouse.Vector); ok {
		result = append(result, vectorTools(store, vs, chunkTypes...)...)
	}

	if se, ok := store.(storehouse.SearchEngine); ok {
		result = append(result, searchEngineTools(se)...)
	}

	return result
}

func baseTools(store storehouse.Storehouse) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("list_memory_categories",
			tools.WithDescription("List all memory categories for a specific memory type."),
			tools.WithString("memory_type",
				tools.Required(),
				tools.Description("The type of memories to list categories for (e.g., 'memory')"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				memoryType, ok := request.Arguments["memory_type"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: memory_type")
				}

				categories, err := store.ListMemoryCategories(ctx, memoryType)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(utils.Res2Str(categories)), nil
			}),
		),
		tools.NewTool("list_documents",
			tools.WithDescription("List all documents."),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				docs, err := store.ListDocuments(ctx)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				result := make([]map[string]interface{}, 0, len(docs))
				for _, doc := range docs {
					result = append(result, map[string]interface{}{
						"id":       doc.ID,
						"title":    doc.Title,
						"metadata": doc.Metadata,
					})
				}

				return tools.NewToolResultText(utils.Res2Str(result)), nil
			}),
		),
		tools.NewTool("get_document",
			tools.WithDescription("Get a document by its ID."),
			tools.WithString("id",
				tools.Required(),
				tools.Description("The ID of the document to retrieve"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				id, ok := request.Arguments["id"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: id")
				}

				doc, err := store.GetDocument(ctx, id)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(utils.Res2Str(doc)), nil
			}),
		),
		tools.NewTool("query_memories_by_category",
			tools.WithDescription("Query memories by category and type."),
			tools.WithString("type",
				tools.Required(),
				tools.Description("The type of memories to query"),
			),
			tools.WithString("category",
				tools.Required(),
				tools.Description("The category to filter memories by"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				memoryType, ok := request.Arguments["type"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: type")
				}
				category, ok := request.Arguments["category"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: category")
				}

				memories, err := store.FilterMemories(ctx, map[string]string{
					types.MetadataMemoryType:     memoryType,
					types.MetadataMemoryCategory: category,
				})
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				if len(memories) == 0 {
					return tools.NewToolResultError("no memories found"), nil
				}

				return tools.NewToolResultText(utils.Res2Str(memories)), nil
			}),
		),
	}
}

func vectorTools(store storehouse.Storehouse, vector storehouse.Vector, chunkTypes ...string) []*tools.Tool {
	matchChunkType := make(map[string]bool)
	for _, chunkType := range chunkTypes {
		matchChunkType[chunkType] = true
	}

	return []*tools.Tool{
		tools.NewTool("knowledge_semantic_query",
			tools.WithDescription("Using natural language to query and recall content from knowledge bases. "+
				"The knowledge base stores all personalized data related to the current user. "+
				"When you need more accurate information, please use this tool actively."),
			tools.WithString("query",
				tools.Required(),
				tools.Description("Describe your problem using natural language. For search accuracy, query only one simple question at a time."),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				query, ok := request.Arguments["query"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: query")
				}

				chunkList, err := vector.SemanticQuery(ctx, types.TypeAll, nil, query, 5)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				if len(matchChunkType) > 0 {
					fc := make([]*types.Chunk, 0, len(chunkList))
					for _, chunk := range chunkList {
						if _, ok = matchChunkType[chunk.Type]; !ok {
							continue
						}
						fc = append(fc, chunk)
					}
					chunkList = fc
				}

				if len(chunkList) == 0 {
					return tools.NewToolResultError("no results found"), nil
				}

				return tools.NewToolResultText(utils.Res2Str(chunkList)), nil
			}),
		),
		tools.NewTool("knowledge_related_query",
			tools.WithDescription("Based on the known knowledge ID, query more content associated information with this knowledge. "+
				"When you confirm a specific knowledge as required information, utilize this tool to enrich the contextual framework of that knowledge."),
			tools.WithString("id",
				tools.Required(),
				tools.Description("The ID of the knowledge that needs to be supplemented"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				cid, ok := request.Arguments["id"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: id")
				}

				chunk, err := store.GetChunk(ctx, cid)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				if chunk == nil || len(chunk.Metadata) == 0 || chunk.Metadata[types.MetadataChunkDocument] == "" {
					return tools.NewToolResultText("No additional information"), nil
				}

				idx, _ := strconv.Atoi(chunk.Metadata[types.MetadataChunkIndex])
				startIdx := idx - 2
				endIdx := idx + 2
				relatedChunks, err := store.FilterChunks(ctx, chunk.Type, map[string]string{types.MetadataChunkDocument: chunk.Metadata[types.MetadataChunkDocument]})
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				var chunkList []*types.Chunk
				for _, relatedChunk := range relatedChunks {
					if _, ok = relatedChunk.Metadata[types.MetadataChunkIndex]; !ok {
						continue
					}
					idx, err = strconv.Atoi(relatedChunk.Metadata[types.MetadataChunkIndex])
					if err != nil {
						continue
					}

					if idx >= startIdx && idx <= endIdx {
						chunkList = append(chunkList, relatedChunk)
					}
				}

				return tools.NewToolResultText(utils.Res2Str(chunkList)), nil
			}),
		),
	}
}

func searchEngineTools(store storehouse.SearchEngine) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("document_search",
			tools.WithDescription("Search for documents using query language. Returns matching documents from the search engine."),
			tools.WithString("query",
				tools.Required(),
				tools.Description("The search query in query language format"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				query, ok := request.Arguments["query"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: query")
				}

				docs, err := store.QueryLanguage(ctx, query)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(utils.Res2Str(docs)), nil
			}),
		),
	}
}
