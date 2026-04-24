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

package providers

import (
	"context"

	"github.com/basenana/friday/core/types"
)

type Client interface {
	Completion(ctx context.Context, request Request) Response
	CompletionNonStreaming(ctx context.Context, request Request) (string, error)
	StructuredPredict(ctx context.Context, request Request, model any) error
}

type ContextWindowProvider interface {
	ContextWindow() int64
}

type Embedding interface {
	Vectorization(ctx context.Context, content string) ([]float64, error)
}

type Request interface {
	Messages() []types.Message
	History() []types.Message
	ToolDefines() []ToolDefine
	SystemPrompt() string
	PromptCacheKey() string

	SetHistory([]types.Message)
	SetToolDefines([]ToolDefine)
	SetSystemPrompt(string)
	SetPromptCacheKey(string)
	AppendHistory(...types.Message)
	AppendToolDefines(...ToolDefine)
	AppendSystemPrompt(...string)
}

type Response interface {
	Message() <-chan Delta
	Error() <-chan error
	Tokens() Tokens
}

type Delta struct {
	Content            string
	Reasoning          string
	ReasoningSignature string
	RedactedThinking   string
	ToolUse            []ToolCall
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
	Error     string
}

type ToolDefine interface {
	GetName() string
	GetDescription() string
	GetParameters() map[string]any
}

type Tokens struct {
	CompletionTokens   int64
	PromptTokens       int64
	CachedPromptTokens int64
	TotalTokens        int64
}

type Apply struct {
	ToolUse  []ToolCall
	Continue bool
	Abort    bool
}
