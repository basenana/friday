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

package v1

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type EmbeddingResult struct {
	Object string       `json:"object"`
	Data   []Embeddings `json:"data"`
	Model  string       `json:"model"`
	Usage  Usage        `json:"usage"`
}

type EmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type Embeddings struct {
	Embedding []float32 `json:"embedding"`
}

type Usage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (o *OpenAIV1) Embedding(ctx context.Context, doc string) (*EmbeddingResult, error) {
	answer, err := o.embedding(ctx, doc)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "rate_limit_exceeded") {
			o.log.Warn("meets rate limit exceeded, sleep 30 seconds and retry")
			time.Sleep(time.Duration(30) * time.Second)
			return o.embedding(ctx, doc)
		}
		return nil, err
	}
	return answer, err
}

func (o *OpenAIV1) embedding(ctx context.Context, doc string) (*EmbeddingResult, error) {
	path := "v1/embeddings"

	model := "text-embedding-ada-002"
	data := map[string]interface{}{
		"model": model,
		"input": doc,
	}

	respBody, err := o.request(ctx, path, "POST", data)
	if err != nil {
		return nil, err
	}

	var res EmbeddingResult
	err = json.Unmarshal(respBody, &res)
	return &res, err
}
