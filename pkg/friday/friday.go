package friday

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"friday/config"
	"friday/pkg/docs"
	"friday/pkg/embedding"
	huggingfaceembedding "friday/pkg/embedding/huggingface"
	openaiembedding "friday/pkg/embedding/openai/v1"
	"friday/pkg/llm"
	glm_6b "friday/pkg/llm/client/glm-6b"
	openaiv1 "friday/pkg/llm/client/openai/v1"
	"friday/pkg/llm/prompts"
	"friday/pkg/models"
	"friday/pkg/utils/logger"
	"friday/pkg/vectorstore"
)

type Friday struct {
	log *zap.SugaredLogger

	llm       llm.LLM
	embedding embedding.Embedding
	vector    vectorstore.VectorStore
	doc       *docs.BleveClient
}

func NewFriday(config *config.Config) (f *Friday, err error) {
	var (
		llmClient      llm.LLM
		embeddingModel embedding.Embedding
		vectorStore    vectorstore.VectorStore
		docStore       *docs.BleveClient
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
		testEmbed, _, err := embeddingModel.VectorQuery("test")
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

	bleveIndex := docs.DefaultIndexname
	if config.BleveIndexName != "" {
		bleveIndex = config.BleveIndexName
	}

	docStore, err = docs.NewBleveClient(bleveIndex)
	if err != nil {
		return nil, err
	}

	f = &Friday{
		log:       logger.NewLogger("friday"),
		llm:       llmClient,
		embedding: embeddingModel,
		vector:    vectorStore,
		doc:       docStore,
	}
	return
}

func (f *Friday) Ingest(elements []models.Element) error {
	texts := []string{}
	for _, element := range elements {
		texts = append(texts, element.Content)
	}
	f.log.Infof("Ingesting %d ...", len(elements))

	// store docs
	if err := f.doc.IndexDocByGroup(elements); err != nil {
		return err
	}

	vectors, m, err := f.embedding.VectorDocs(texts)
	if err != nil {
		return err
	}

	for i, text := range texts {
		t := strings.TrimSpace(text)
		id := uuid.New().String()
		metadata := make(map[string]interface{})
		if m != nil {
			metadata = m[i]
		}
		metadata["title"] = elements[i].Metadata.Title
		metadata["category"] = elements[i].Metadata.Category
		metadata["group"] = elements[i].Metadata.Group
		v := vectors[i]
		f.log.Infof("store vector %s ...", t)
		if err := f.vector.Store(id, t, metadata, v); err != nil {
			return err
		}
	}
	return nil
}

func (f *Friday) IngestFromElementFile(ps string) error {
	doc, err := os.ReadFile(ps)
	if err != nil {
		return err
	}
	elements := []models.Element{}
	if err := json.Unmarshal(doc, &elements); err != nil {
		return err
	}
	return f.Ingest(elements)
}

func (f *Friday) Question(prompt prompts.PromptTemplate, q string) (string, error) {
	c, err := f.searchDocs(q)
	if err != nil {
		return "", err
	}
	if f.llm != nil {
		ans, err := f.llm.Completion(prompt, map[string]string{
			"context":  c,
			"question": q,
		})
		if err != nil {
			return "", fmt.Errorf("llm completion error: %w", err)
		}
		return ans[0], nil
	}
	return c, nil
}

func (f *Friday) searchDocs(q string) (string, error) {
	f.log.Debugf("vector query for %s ...", q)
	qv, _, err := f.embedding.VectorQuery(q)
	if err != nil {
		return "", fmt.Errorf("vector embedding error: %w", err)
	}
	contexts, err := f.vector.Search(qv, 2)
	if err != nil {
		return "", fmt.Errorf("vector search error: %w", err)
	}
	texts := make([]string, len(contexts))
	docMap := make(map[string]models.Element)
	for _, con := range contexts {
		doc, err := f.doc.Search(con.Content)
		if err != nil {
			continue
		}
		if doc == nil {
			continue
		}
		id := fmt.Sprintf("%s-%s", doc.Metadata.Title, doc.Metadata.Group)
		docMap[id] = *doc
	}
	for id, doc := range docMap {
		f.log.Debugf("docs query contexts, id: %s, context: %s", id, doc.Content)
		texts = append(texts, doc.Content)
	}
	return strings.Join(texts, "\n"), nil
}
