package config

type Config struct {
	Debug bool `json:"debug,omitempty"`

	// embedding config
	EmbeddingType  EmbeddingType `json:"embedding_type"`
	EmbeddingUrl   string        `json:"embedding_url,omitempty"`   // only needed for huggingface
	EmbeddingModel string        `json:"embedding_model,omitempty"` // only needed for huggingface

	// vector store config
	VectorStoreType VectorStoreType `json:"vector_store_type"`
	VectorUrl       string          `json:"vector_url"`
	EmbeddingDim    int             `json:"embedding_dim,omitempty"` // embedding dimension, default is 1536

	// LLM
	LLMType LLMType `json:"llm_type"`
	LLMUrl  string  `json:"llm_url,omitempty"` // only needed for glm-6b

	// text spliter
	SpliterChunkSize    int    `json:"spliter_chunk_size,omitempty"`    // chunk of files splited to store, default is 4000
	SpliterChunkOverlap int    `json:"spliter_chunk_overlap,omitempty"` // overlap of each chunks, default is 200
	SpliterSeparator    string `json:"spliter_separator,omitempty"`     // separator to split files, default is \n
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
