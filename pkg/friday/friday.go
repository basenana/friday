package friday

import (
	"friday/config"
	"friday/pkg/docs"
	"friday/pkg/embedding"
	huggingfaceembedding "friday/pkg/embedding/huggingface"
	openaiembedding "friday/pkg/embedding/openai/v1"
	"friday/pkg/llm"
	glm_6b "friday/pkg/llm/client/glm-6b"
	openaiv1 "friday/pkg/llm/client/openai/v1"
	"friday/pkg/spliter"
	"friday/pkg/utils/logger"
	"friday/pkg/vectorstore"
)

const defaultTopK = 6

type Friday struct {
	log logger.Logger

	llm       llm.LLM
	embedding embedding.Embedding
	vector    vectorstore.VectorStore
	doc       *docs.BleveClient
	spliter   spliter.Spliter
}

func NewFriday(conf *config.Config) (f *Friday, err error) {
	var (
		llmClient      llm.LLM
		embeddingModel embedding.Embedding
		vectorStore    vectorstore.VectorStore
		docStore       *docs.BleveClient
	)
	if conf.LLMType == config.LLMOpenAI {
		llmClient = openaiv1.NewOpenAIV1()
	}
	if conf.LLMType == config.LLMGLM6B {
		llmClient = glm_6b.NewGLM(conf.LLMUrl)
	}

	if conf.EmbeddingType == config.EmbeddingOpenAI {
		embeddingModel = openaiembedding.NewOpenAIEmbedding()
	}
	if conf.EmbeddingType == config.EmbeddingHuggingFace {
		embeddingModel = huggingfaceembedding.NewHuggingFace(conf.EmbeddingUrl, conf.EmbeddingModel)
		testEmbed, _, err := embeddingModel.VectorQuery("test")
		if err != nil {
			return nil, err
		}
		conf.EmbeddingDim = len(testEmbed)
	}

	if conf.VectorStoreType == config.VectorStoreRedis {
		if conf.EmbeddingDim == 0 {
			vectorStore, err = vectorstore.NewRedisClient(conf.VectorUrl)
			if err != nil {
				return nil, err
			}
		} else {
			vectorStore, err = vectorstore.NewRedisClientWithDim(conf.VectorUrl, conf.EmbeddingDim)
			if err != nil {
				return nil, err
			}
		}
	}

	bleveIndex := docs.DefaultIndexname
	if conf.BleveIndexName != "" {
		bleveIndex = conf.BleveIndexName
	}

	docStore, err = docs.NewBleveClient(bleveIndex)
	if err != nil {
		return nil, err
	}

	chunkSize := spliter.DefaultChunkSize
	overlapSize := spliter.DefaultChunkOverlap
	separator := "\n"
	if conf.SpliterChunkSize != 0 {
		chunkSize = conf.SpliterChunkSize
	}
	if conf.SpliterChunkOverlap != 0 {
		overlapSize = conf.SpliterChunkOverlap
	}
	if conf.SpliterSeparator != "" {
		separator = conf.SpliterSeparator
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
