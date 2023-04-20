package friday

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"friday/config"
	"friday/pkg/embedding"
	huggingfaceembedding "friday/pkg/embedding/huggingface"
	openaiembedding "friday/pkg/embedding/openai/v1"
	"friday/pkg/llm"
	glm_6b "friday/pkg/llm/client/glm-6b"
	openaiv1 "friday/pkg/llm/client/openai/v1"
	"friday/pkg/llm/prompts"
	"friday/pkg/utils/logger"
	"friday/pkg/vectorstore"
)

type Friday struct {
	log *zap.SugaredLogger

	llm       llm.LLM
	embedding embedding.Embedding
	vector    vectorstore.VectorStore
}

type Element struct {
	Content  string   `json:"content"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Title      string `json:"title"`
	PageNumber int    `json:"page_number"`
	Category   string `json:"category"`
}

func NewFriday(config *config.Config) (f *Friday, err error) {
	var (
		llmClient      llm.LLM
		embeddingModel embedding.Embedding
		vectorStore    vectorstore.VectorStore
	)
	if config.LLMType == "openai" {
		llmClient = openaiv1.NewOpenAIV1()
	}
	if config.LLMType == "glm-6b" {
		llmClient = glm_6b.NewGLM(config.LLMUrl)
	}

	if config.EmbeddingType == "openai" {
		embeddingModel = openaiembedding.NewOpenAIEmbedding()
	}
	if config.EmbeddingType == "huggingface" {
		embeddingModel = huggingfaceembedding.NewHuggingFace(config.EmbeddingUrl, config.EmbeddingModel)
		testEmbed, err := embeddingModel.VectorQuery("test")
		if err != nil {
			return nil, err
		}
		config.EmbeddingDim = len(testEmbed)
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
		log:       logger.NewLogger("friday"),
		llm:       llmClient,
		embedding: embeddingModel,
		vector:    vectorStore,
	}
	return
}

func (f Friday) Ingest(elements []Element) error {
	texts := []string{}
	for _, element := range elements {
		texts = append(texts, element.Content)
	}
	f.log.Infof("Ingesting %d ...", len(elements))
	vectors, err := f.embedding.VectorDocs(texts)
	if err != nil {
		return err
	}

	for i, text := range texts {
		t := strings.TrimSpace(text)
		id := uuid.New().String()
		metadata := map[string]interface{}{
			"title":    elements[i].Metadata.Title,
			"category": elements[i].Metadata.Category,
		}
		v := vectors[i]
		f.log.Infof("store vector %s ...", t)
		if err := f.vector.Store(id, t, metadata, v); err != nil {
			return err
		}
	}
	return nil
}

func (f Friday) IngestFromElementFile(ps string) error {
	doc, err := os.ReadFile(ps)
	if err != nil {
		return err
	}
	elements := []Element{}
	if err := json.Unmarshal(doc, &elements); err != nil {
		return err
	}
	return f.Ingest(elements)
}

func (f Friday) Question(prompt prompts.PromptTemplate, q string) (string, error) {
	qv, err := f.embedding.VectorQuery(q)
	if err != nil {
		return "", err
	}
	contexts, err := f.vector.Search(qv, 10)
	if err != nil {
		return "", err
	}
	f.log.Debugf("vector query contexts: %v", contexts)
	texts := make([]string, len(contexts))
	for _, con := range contexts {
		texts = append(texts, con.Content)
	}
	c := strings.Join(texts, "\n")
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
