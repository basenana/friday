package v1

import (
	"friday/pkg/embedding"
	"friday/pkg/llm/client/openai/v1"
)

type OpenAIEmbedding struct {
	*v1.OpenAIV1
}

var _ embedding.Embedding = &OpenAIEmbedding{}

func NewOpenAIEmbedding() embedding.Embedding {
	return &OpenAIEmbedding{
		OpenAIV1: v1.NewOpenAIV1(),
	}
}

func (o *OpenAIEmbedding) VectorQuery(doc string) ([]float32, map[string]interface{}, error) {
	res, err := o.Embedding(doc)
	if err != nil {
		return nil, nil, err
	}
	usage := res.Usage
	metadata := make(map[string]interface{})
	metadata["prompt_tokens"] = usage.PromptTokens
	metadata["total_tokens"] = usage.TotalTokens

	return res.Data[0].Embedding, metadata, nil
}

func (o *OpenAIEmbedding) VectorDocs(docs []string) ([][]float32, []map[string]interface{}, error) {
	res := make([][]float32, len(docs))
	metadata := make([]map[string]interface{}, len(docs))

	for i, doc := range docs {
		r, err := o.Embedding(doc)
		if err != nil {
			return nil, nil, err
		}
		res[i] = r.Data[0].Embedding
		metadata[i] = map[string]interface{}{
			"prompt_tokens": r.Usage.PromptTokens,
			"total_tokens":  r.Usage.TotalTokens,
		}
	}
	return res, metadata, nil
}
