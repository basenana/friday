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

package friday

import (
	"friday/config"
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
	spliter   spliter.Spliter
}

func NewFriday(conf *config.Config) (f *Friday, err error) {
	var (
		llmClient      llm.LLM
		embeddingModel embedding.Embedding
		vectorStore    vectorstore.VectorStore
	)
	// init LLM client
	if conf.LLMType == config.LLMOpenAI {
		llmClient = openaiv1.NewOpenAIV1()
	}
	if conf.LLMType == config.LLMGLM6B {
		llmClient = glm_6b.NewGLM(conf.LLMUrl)
	}

	// init embedding client
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

	// init vector store
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

	// init text spliter
	chunkSize := spliter.DefaultChunkSize
	overlapSize := spliter.DefaultChunkOverlap
	separator := "\n"
	if conf.SpliterChunkSize > 0 {
		chunkSize = conf.SpliterChunkSize
	}
	if conf.SpliterChunkOverlap > 0 {
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
		spliter:   textSpliter,
	}
	return
}
