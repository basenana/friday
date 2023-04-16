package friday

import (
	"strings"

	"friday/pkg/embedding"
	openaiembedding "friday/pkg/embedding/openai/v1"
	"friday/pkg/llm"
	openaiv1 "friday/pkg/llm/client/openai/v1"
	"friday/pkg/llm/prompts"
	"friday/pkg/vectorstore"
)

type Friday struct {
	llm       llm.LLM
	embedding embedding.Embedding
	vector    vectorstore.VectorStore
}

type Config struct {
	EmbeddingType   string
	EmbeddingDim    int
	VectorStoreType string
	VectorUrl       string
	LLMType         string
}

func NewFriday(config *Config) (f *Friday, err error) {
	var (
		llmClient      llm.LLM
		embeddingModel embedding.Embedding
		vectorStore    vectorstore.VectorStore
	)
	if config.LLMType == "openai" {
		llmClient = openaiv1.NewOpenAIV1()
	}
	if config.EmbeddingType == "openai" {
		embeddingModel = openaiembedding.NewOpenAIEmbedding()
	}
	if config.VectorStoreType == "redis" {
		if config.EmbeddingDim == 0 {
			vectorStore, err = vectorstore.NewRedisClient(config.VectorUrl)
			if err != nil {
				return nil, err
			}
		} else {
			vectorStore, err = vectorstore.NewRedisClientWithDim(config.VectorUrl, config.EmbeddingDim)
			if err != nil {
				return nil, err
			}
		}
	}
	f = &Friday{
		llm:       llmClient,
		embedding: embeddingModel,
		vector:    vectorStore,
	}
	return
}

func (f *Friday) Ingest(id, doc string) {
	v, err := f.embedding.Vector(doc)
	if err != nil {
		return
	}
	f.vector.EmbeddingDoc(id, doc, v)
}

func (f *Friday) Question(prompt prompts.PromptTemplate, q string) (string, error) {
	qv, err := f.embedding.Vector(q)
	if err != nil {
		return "", err
	}
	contexts, err := f.vector.Search(qv, 10)
	if err != nil {
		return "", err
	}
	c := strings.Join(contexts, "\n")
	if f.llm != nil {
		ans, err := f.llm.Completion(prompt, map[string]string{
			"context":  c,
			"question": q,
		})
		if err != nil {
			return "", err
		}
		return ans[0], nil
	}
	return c, nil
}
