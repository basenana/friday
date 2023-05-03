package config

type Config struct {
	Debug bool `json:"debug,omitempty"`

	EmbeddingType  EmbeddingType `json:"embedding_type"`
	EmbeddingDim   int           `json:"embedding_dim,omitempty"`
	EmbeddingUrl   string        `json:"embedding_url,omitempty"`
	EmbeddingModel string        `json:"embedding_model,omitempty"`

	VectorStoreType VectorStoreType `json:"vector_store_type"`
	VectorUrl       string          `json:"vector_url"`

	LLMType LLMType `json:"llm_type"`
	LLMUrl  string  `json:"llm_url,omitempty"`

	BleveIndexName string `json:"bleve_index_name,omitempty"`

	SpliterChunkSize    int    `json:"spliter_chunk_size,omitempty"`
	SpliterChunkOverlap int    `json:"spliter_chunk_overlap,omitempty"`
	SpliterSeparator    string `json:"spliter_separator,omitempty"`
}

type LLMType string

const (
	LLMGLM6B  LLMType = "glm-6b"
	LLMOpenAI LLMType = "openai"
)

type EmbeddingType string

const (
	EmbeddingOpenAI      EmbeddingType = "openai"
	EmbeddingHuggingFace EmbeddingType = "huggingface"
)

type VectorStoreType string

const (
	VectorStoreRedis VectorStoreType = "redis"
)
