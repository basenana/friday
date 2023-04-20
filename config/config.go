package config

type Config struct {
	EmbeddingType  string `json:"embedding_type"`
	EmbeddingDim   int    `json:"embedding_dim,omitempty"`
	EmbeddingUrl   string `json:"embedding_url,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`

	VectorStoreType string `json:"vector_store_type"`
	VectorUrl       string `json:"vector_url"`

	LLMType string `json:"llm_type"`
	LLMUrl  string `json:"llm_url,omitempty"`
}
