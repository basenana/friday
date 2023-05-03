package friday

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

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
	"friday/pkg/spliter"
	"friday/pkg/utils/files"
	"friday/pkg/utils/logger"
	"friday/pkg/vectorstore"
)

const defaultTopK = 6

type Friday struct {
	log *zap.SugaredLogger

	llm       llm.LLM
	embedding embedding.Embedding
	vector    vectorstore.VectorStore
	doc       *docs.BleveClient
	spliter   spliter.Spliter
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

	chunkSize := spliter.DefaultChunkSize
	overlapSize := spliter.DefaultChunkOverlap
	separator := "\n"
	if config.SpliterChunkSize != 0 {
		chunkSize = config.SpliterChunkSize
	}
	if config.SpliterChunkOverlap != 0 {
		overlapSize = config.SpliterChunkOverlap
	}
	if config.SpliterSeparator != "" {
		separator = config.SpliterSeparator
	}
	textSpliter := spliter.NewTextSpliter(chunkSize, overlapSize, separator)

	f = &Friday{
		log:       logger.NewLogger("friday"),
		llm:       llmClient,
		embedding: embeddingModel,
		vector:    vectorStore,
		doc:       docStore,
		spliter:   textSpliter,
	}
	return
}

func (f *Friday) Ingest(elements []models.Element) error {
	f.log.Debugf("Ingesting %d ...", len(elements))
	for i, element := range elements {
		// id: sha256(source)-group
		h := sha256.New()
		h.Write([]byte(element.Metadata.Source))
		val := hex.EncodeToString(h.Sum(nil))[:64]
		id := fmt.Sprintf("%s-%s", val, element.Metadata.Group)
		if f.vector.Exist(id) {
			f.log.Debugf("vector %d(th) id(%s) source(%s) exist, skip ...", i, id, element.Metadata.Source)
			continue
		}

		vectors, m, err := f.embedding.VectorQuery(element.Content)
		if err != nil {
			return err
		}

		t := strings.TrimSpace(element.Content)

		metadata := make(map[string]interface{})
		if m != nil {
			metadata = m
		}
		metadata["title"] = element.Metadata.Title
		metadata["source"] = element.Metadata.Source
		metadata["category"] = element.Metadata.Category
		metadata["group"] = element.Metadata.Group
		v := vectors
		f.log.Debugf("store %d(th) vector id (%s) source(%s) ...", i, id, element.Metadata.Source)
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
	merged := f.spliter.Merge(elements)
	return f.Ingest(merged)
}

func (f *Friday) IngestFromFile(ps string) error {
	fs, err := files.ReadFiles(ps)
	if err != nil {
		return err
	}

	elements := []models.Element{}
	for n, file := range fs {
		subDocs := f.spliter.Split(file)
		for i, subDoc := range subDocs {
			e := models.Element{
				Content: subDoc,
				Metadata: models.Metadata{
					Source: n,
					Group:  strconv.Itoa(i),
				},
			}
			elements = append(elements, e)
		}
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
	contexts, err := f.vector.Search(qv, defaultTopK)
	if err != nil {
		return "", fmt.Errorf("vector search error: %w", err)
	}

	cs := []string{}
	for _, c := range contexts {
		f.log.Debugf("searched from [%s] for %s", c.Metadata["source"], c.Content)
		cs = append(cs, c.Content)
	}
	return strings.Join(cs, "\n"), nil
}
