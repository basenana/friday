package tools

import (
	"context"
	"fmt"
	"strconv"

	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils"
)

func SearchTools(store storehouse.Storehouse, chunkTypes ...string) []*Tool {
	var vector storehouse.Vector
	if vs, ok := store.(storehouse.Vector); ok {
		vector = vs
	}

	result := baseKnowledgeTools(store, vector, chunkTypes...)

	if vs, ok := store.(storehouse.Vector); ok {
		result = append(result, vectorTools(vs, chunkTypes...)...)
	}

	if se, ok := store.(storehouse.SearchEngine); ok {
		result = append(result, searchEngineTools(se)...)
	}

	return result
}

func baseKnowledgeTools(store storehouse.Storehouse, vector storehouse.Vector, chunkTypes ...string) []*Tool {
	matchChunkType := make(map[string]bool)
	for _, chunkType := range chunkTypes {
		matchChunkType[chunkType] = true
	}

	return []*Tool{
		NewTool("knowledge_semantic_query",
			WithDescription("Using natural language to query and recall content from knowledge bases. "+
				"The knowledge base stores all personalized data related to the current user. "+
				"When you need more accurate information, please use this tool actively."),
			WithString("query",
				Required(),
				Description("Describe your problem using natural language. For search accuracy, query only one simple question at a time."),
			),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				query, ok := request.Arguments["query"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: query")
				}

				if vector == nil {
					return NewToolResultError("vector search not available"), nil
				}

				chunkList, err := vector.SemanticQuery(ctx, types.TypeAll, query, 5)
				if err != nil {
					return NewToolResultError(err.Error()), nil
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
					return NewToolResultError("no results found"), nil
				}

				return NewToolResultText(utils.Res2Str(chunkList)), nil
			}),
		),
		NewTool("knowledge_related_query",
			WithDescription("Based on the known knowledge ID, query more content associated information with this knowledge. "+
				"When you confirm a specific knowledge as required information, utilize this tool to enrich the contextual framework of that knowledge."),
			WithString("id",
				Required(),
				Description("The ID of the knowledge that needs to be supplemented"),
			),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				cid, ok := request.Arguments["id"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: id")
				}

				chunk, err := store.GetChunk(ctx, cid)
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}

				if chunk == nil || len(chunk.Metadata) == 0 || chunk.Metadata[types.MetadataChunkDocument] == "" {
					return NewToolResultText("No additional information"), nil
				}

				idx, _ := strconv.Atoi(chunk.Metadata[types.MetadataChunkIndex])
				startIdx := idx - 2
				endIdx := idx + 2
				relatedChunks, err := store.FilterChunks(ctx, chunk.Type, map[string]string{types.MetadataChunkDocument: chunk.Metadata[types.MetadataChunkDocument]})
				if err != nil {
					return NewToolResultError(err.Error()), nil
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

				return NewToolResultText(utils.Res2Str(chunkList)), nil
			}),
		),
	}
}

func vectorTools(store storehouse.Vector, chunkTypes ...string) []*Tool {
	matchChunkType := make(map[string]bool)
	for _, chunkType := range chunkTypes {
		matchChunkType[chunkType] = true
	}

	return []*Tool{
		NewTool("vector_query",
			WithDescription("Query chunks by vector similarity. This tool uses vector embedding matching to find similar content."),
			WithString("query",
				Required(),
				Description("The natural language query to convert to vector for similarity search"),
			),
			WithNumber("topK",
				Description("Number of results to return"),
			),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				query, ok := request.Arguments["query"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: query")
				}

				topK := 5
				if n, ok := request.Arguments["topK"].(float64); ok {
					topK = int(n)
				}

				chunkList, err := store.SemanticQuery(ctx, types.TypeAll, query, topK)
				if err != nil {
					return NewToolResultError(err.Error()), nil
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

				return NewToolResultText(utils.Res2Str(chunkList)), nil
			}),
		),
	}
}

func searchEngineTools(store storehouse.SearchEngine) []*Tool {
	return []*Tool{
		NewTool("document_search",
			WithDescription("Search for documents using query language. Returns matching documents from the search engine."),
			WithString("query",
				Required(),
				Description("The search query in query language format"),
			),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				query, ok := request.Arguments["query"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: query")
				}

				docs, err := store.QueryLanguage(ctx, query)
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}

				return NewToolResultText(utils.Res2Str(docs)), nil
			}),
		),
	}
}
