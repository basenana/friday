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
	"fmt"
	"strings"
)

type ChatResult struct {
	Id      string         `json:"id"`
	Object  string         `json:"object"`
	Created int            `json:"created"`
	Model   string         `json:"model"`
	Choices []ChatChoice   `json:"choices"`
	Usage   map[string]int `json:"usage,omitempty"`
}

type ChatChoice struct {
	Index        int               `json:"index"`
	Message      map[string]string `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type ChatStreamResult struct {
	Id      string             `json:"id"`
	Object  string             `json:"object"`
	Created int                `json:"created"`
	Model   string             `json:"model"`
	Choices []ChatStreamChoice `json:"choices"`
}

type ChatStreamChoice struct {
	Index        int               `json:"index"`
	Delta        map[string]string `json:"delta"`
	FinishReason string            `json:"finish_reason,omitempty"`
}

func (o *OpenAIV1) Chat(ctx context.Context, stream bool, history []map[string]string, answers chan<- map[string]string) (map[string]int, error) {
	return o.chat(ctx, stream, history, answers)
}

func (o *OpenAIV1) chat(ctx context.Context, stream bool, history []map[string]string, resp chan<- map[string]string) (tokens map[string]int, err error) {
	path := "v1/chat/completions"

	data := map[string]interface{}{
		"model":             *o.conf.Model,
		"messages":          history,
		"max_tokens":        *o.conf.MaxReturnToken,
		"temperature":       *o.conf.Temperature,
		"top_p":             1,
		"frequency_penalty": *o.conf.FrequencyPenalty,
		"presence_penalty":  *o.conf.PresencePenalty,
		"n":                 1,
		"stream":            stream,
	}

	buf := make(chan []byte)
	go func() {
		err = o.request(ctx, stream, path, "POST", data, buf)
		close(buf)
	}()

	if err != nil {
		return
	}
	for line := range buf {
		var res ChatStreamResult
		if !strings.HasPrefix(string(line), "data:") || strings.Contains(string(line), "data: [DONE]") {
			continue
		}
		l := string(line)[6:]
		err = json.Unmarshal([]byte(l), &res)
		if err != nil {
			err = fmt.Errorf("cannot marshal msg: %s, err: %v", line, err)
			return
		}

		select {
		case <-ctx.Done():
			err = fmt.Errorf("context timeout in openai chat")
			return
		case resp <- res.Choices[0].Delta:
			continue
		}
	}
	return
}
