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
	"fmt"
	"io"
	"net/http"
	"net/url"

	"golang.org/x/time/rate"

	"github.com/basenana/friday/pkg/llm"
	"github.com/basenana/friday/pkg/utils/logger"
)

const (
	defaultQueryPerMinute = 3
	defaultBurst          = 5
)

type OpenAIV1 struct {
	log logger.Logger

	baseUri string
	key     string

	limiter *rate.Limiter
}

func NewOpenAIV1(baseUrl, key string, qpm, burst int) *OpenAIV1 {
	if qpm <= 0 {
		qpm = defaultQueryPerMinute
	}
	if burst <= 0 {
		burst = defaultBurst
	}

	limiter := rate.NewLimiter(rate.Limit(qpm), burst*60)

	return &OpenAIV1{
		log:     logger.NewLogger("openai"),
		baseUri: baseUrl,
		key:     key,
		limiter: limiter,
	}
}

var _ llm.LLM = &OpenAIV1{}

func (o *OpenAIV1) request(ctx context.Context, path string, method string, body io.Reader) ([]byte, error) {
	err := o.limiter.WaitN(ctx, 60)
	if err != nil {
		return nil, err
	}

	uri, err := url.JoinPath(o.baseUri, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, uri, body)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.key))

	o.log.Debugf("request: %s", uri)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read Response Body
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fail to call openai, status code error: %d, resp body: %s", resp.StatusCode, string(respBody))
	}
	//o.log.Debugf("response: %s", respBody)
	return respBody, nil
}
