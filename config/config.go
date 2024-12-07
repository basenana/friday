/*
 * Copyright 2023 friday
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package config

import (
	"github.com/basenana/friday/pkg/utils/logger"
)

type Config struct {
	Debug  bool `json:"debug,omitempty"`
	Logger logger.Logger

	HttpAddr string `json:"httpAddr,omitempty"`

	PoolNum int `json:"poolNum,omitempty"`

	// plugins
	Plugins []string `json:"plugins,omitempty"`

	// meilisearch
	MeiliConfig MeiliConfig `json:"meiliConfig,omitempty"`

	// llm limit token
	LimitToken int `json:"limitToken,omitempty"` // used by summary, split input into mutil sub-docs summaried by llm separately.

	// openai key
	OpenAIBaseUrl string `json:"openAiBaseUrl,omitempty"` // if openai is used for embedding or llm, it is needed, default is "https://api.openai.com"
	OpenAIKey     string `json:"openAiKey,omitempty"`     // if openai is used for embedding or llm, it is needed

	// gemini key
	GeminiBaseUri string `json:"geminiBaseUri,omitempty"` // if gemini is used for embedding or llm, it is needed, default is "https://generativelanguage.googleapis.com"
	GeminiKey     string `json:"geminiKey,omitempty"`     // if gemini is used for embedding or llm, it is needed

	// embedding config
	EmbeddingConfig EmbeddingConfig `json:"embeddingConfig,omitempty"`

	// vector store config
	VectorStoreConfig VectorStoreConfig `json:"vectorStoreConfig,omitempty"`

	// LLM
	LLMConfig LLMConfig `json:"llmConfig,omitempty"`

	// text spliter
	TextSpliterConfig TextSpliterConfig `json:"textSpliterConfig,omitempty"`
}

type MeiliConfig struct {
	MeiliUrl     string `json:"meiliUrl,omitempty"`
	MasterKey    string `json:"masterKey,omitempty"`
	AdminApiKey  string `json:"adminApiKey,omitempty"`
	SearchApiKey string `json:"searchApiKey,omitempty"`
	Index        string `json:"index,omitempty"`
}

type LLMConfig struct {
	LLMType LLMType           `json:"llmType"`
	Prompts map[string]string `json:"prompts,omitempty"`
	OpenAI  OpenAIConfig      `json:"openai,omitempty"`
	GLM6B   GLM6BConfig       `json:"glm6b,omitempty"`
	Gemini  GeminiConfig      `json:"gemini,omitempty"`
}

type GLM6BConfig struct {
	Url string `json:"url,omitempty"`
}

type OpenAIConfig struct {
	QueryPerMinute   int      `json:"queryPerMinute,omitempty"` // qpm, default is 3
	Burst            int      `json:"burst,omitempty"`          // burst, default is 5
	Model            *string  `json:"model,omitempty"`          // model of openai, default for llm is "gpt-3.5-turbo"; default for embedding is "text-embedding-ada-002"
	MaxReturnToken   *int     `json:"maxReturnToken,omitempty"` // maxReturnToken + VectorStoreConfig.TopK * TextSpliterConfig.SpliterChunkSize <= token limit of llm model
	FrequencyPenalty *uint    `json:"frequencyPenalty,omitempty"`
	PresencePenalty  *uint    `json:"presencePenalty,omitempty"`
	Temperature      *float32 `json:"temperature,omitempty"`
}

type GeminiConfig struct {
	QueryPerMinute int     `json:"queryPerMinute,omitempty"` // qpm, default is 3
	Burst          int     `json:"burst,omitempty"`          // burst, default is 5
	Model          *string `json:"model,omitempty"`          // model of gemini, default for llm is "gemini-pro"; default for embedding is "embedding-001"
}

type EmbeddingConfig struct {
	EmbeddingType EmbeddingType     `json:"embeddingType"`
	OpenAI        OpenAIConfig      `json:"openai,omitempty"`
	HuggingFace   HuggingFaceConfig `json:"huggingFace,omitempty"`
	Gemini        GeminiConfig      `json:"gemini,omitempty"`
}

type HuggingFaceConfig struct {
	EmbeddingUrl   string `json:"embeddingUrl,omitempty"`
	EmbeddingModel string `json:"embeddingModel,omitempty"`
}

type VectorStoreConfig struct {
	VectorStoreType VectorStoreType `json:"vectorStoreType"`
	VectorUrl       string          `json:"vectorUrl"`
	TopK            *int            `json:"topK,omitempty"`         // topk of knn, default is 6
	EmbeddingDim    int             `json:"embeddingDim,omitempty"` // embedding dimension, default is 1536
}

type TextSpliterConfig struct {
	SpliterChunkSize    int    `json:"spliterChunkSize,omitempty"`    // chunk of files splited to store, default is 4000
	SpliterChunkOverlap int    `json:"spliterChunkOverlap,omitempty"` // overlap of each chunks, default is 200
	SpliterSeparator    string `json:"spliterSeparator,omitempty"`    // separator to split files, default is \n
}

type LLMType string

const (
	LLMGLM6B  LLMType = "glm-6b"
	LLMOpenAI LLMType = "openai"
	LLMGemini LLMType = "gemini"
)

type EmbeddingType string

const (
	EmbeddingOpenAI      EmbeddingType = "openai"
	EmbeddingHuggingFace EmbeddingType = "huggingface"
	EmbeddingGemini      EmbeddingType = "gemini"
)

type VectorStoreType string

const (
	VectorStoreRedis    VectorStoreType = "redis"
	VectorStorePostgres VectorStoreType = "postgres"
	VectorStorePGVector VectorStoreType = "pgvector"
)
