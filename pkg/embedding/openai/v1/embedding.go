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

func (o *OpenAIEmbedding) VectorQuery(doc string) ([]float32, error) {
	return o.Embedding(doc)
}

func (o *OpenAIEmbedding) VectorDocs(docs []string) ([][]float32, error) {
	res := make([][]float32, len(docs))
	var err error
	for i, doc := range docs {
		res[i], err = o.Embedding(doc)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}
