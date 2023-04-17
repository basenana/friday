package friday

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"friday/pkg/embedding"
	openaiembedding "friday/pkg/embedding/openai/v1"
	"friday/pkg/llm"
	glm_6b "friday/pkg/llm/client/glm-6b"
	openaiv1 "friday/pkg/llm/client/openai/v1"
	"friday/pkg/llm/prompts"
	"friday/pkg/spliter"
	"friday/pkg/vectorstore"
)

type Friday struct {
	llm       llm.LLM
	embedding embedding.Embedding
	vector    vectorstore.VectorStore
	spliter   spliter.Spliter
}

type Config struct {
	EmbeddingType   string
	EmbeddingDim    int
	VectorStoreType string
	VectorUrl       string
	LLMType         string
	SpliterType     string
	ChunkSize       int
}

func NewFriday(config *Config) (f *Friday, err error) {
	var (
		llmClient      llm.LLM
		embeddingModel embedding.Embedding
		vectorStore    vectorstore.VectorStore
		textSpliter    spliter.Spliter
	)
	if config.LLMType == "openai" {
		llmClient = openaiv1.NewOpenAIV1()
	}
	if config.LLMType == "glm-6b" {
		llmClient = glm_6b.NewGLM("http://localhost:8000")
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
	if config.SpliterType == "text" {
		textSpliter = spliter.NewTextSpliter(config.ChunkSize, 200, "\n")
	}
	f = &Friday{
		llm:       llmClient,
		embedding: embeddingModel,
		vector:    vectorStore,
		spliter:   textSpliter,
	}
	return
}

func (f Friday) Ingest(ps string) error {
	docs, err := f.readFiles(ps)
	if err != nil {
		return err
	}
	for title, doc := range docs {
		texts := f.spliter.Split(doc)
		for i, text := range texts {
			t := strings.TrimSpace(text)
			id := fmt.Sprintf("%s-%d", title, i)
			metadata := map[string]interface{}{
				"title": title,
			}
			v, err := f.embedding.Vector(t)
			if err != nil {
				return err
			}
			if err := f.vector.EmbeddingDoc(id, t, metadata, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f Friday) ingest(title, doc string) error {
	texts := f.spliter.Split(doc)
	for i, text := range texts {
		id := fmt.Sprintf("%s-%d", title, i)
		metadata := map[string]interface{}{
			"title": title,
		}
		v, err := f.embedding.Vector(text)
		if err != nil {
			return err
		}
		if err := f.vector.EmbeddingDoc(id, doc, metadata, v); err != nil {
			return err
		}
	}
	return nil
}

func (f Friday) readFiles(ps string) (docs map[string]string, err error) {
	var p os.FileInfo
	docs = map[string]string{}
	p, err = os.Stat(ps)
	if err != nil {
		return
	}
	if p.IsDir() {
		var subFiles []os.DirEntry
		subFiles, err = os.ReadDir(ps)
		if err != nil {
			return
		}
		for _, subFile := range subFiles {
			subDocs := make(map[string]string)
			subDocs, err = f.readFiles(filepath.Join(ps, subFile.Name()))
			if err != nil {
				return
			}
			for k, v := range subDocs {
				docs[k] = v
			}
		}
		return
	}
	doc, err := os.ReadFile(ps)
	if err != nil {
		return
	}
	docs[ps] = string(doc)
	return
}

func (f Friday) Question(prompt prompts.PromptTemplate, q string) (string, error) {
	qv, err := f.embedding.Vector(q)
	if err != nil {
		return "", err
	}
	contexts, err := f.vector.Search(qv, 10)
	if err != nil {
		return "", err
	}
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
