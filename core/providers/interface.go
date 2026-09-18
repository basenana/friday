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

// ClientPolicy describes request-routing defaults applied by a forkable
// multi-model client. PreferredModel is a model name, not an endpoint ID, so
// every configured endpoint exposing that name is promoted together.
type ClientPolicy struct {
	PreferredModel string
	Effort         string
}

// ForkableClient creates a lightweight policy view while sharing the same
// underlying provider clients and rate limiters.
type ForkableClient interface {
	Fork(ClientPolicy) Client
}

// ClientRuntimeInfo is a consistent snapshot of the model candidate selected
// by a client view. Actual is false when the values are derived from current
// routing policy before that policy has made a physical model request.
type ClientRuntimeInfo struct {
	Model       string
	EndpointKey string
	Effort      string
	Actual      bool
}

// ClientRuntimeInfoProvider exposes request-routing state for observability.
// Each forked client view owns its own snapshot so subagent calls cannot
// overwrite the primary Session client's state.
type ClientRuntimeInfoProvider interface {
	RuntimeInfo() ClientRuntimeInfo
}

func RuntimeInfo(client Client) (ClientRuntimeInfo, bool) {
	provider, ok := client.(ClientRuntimeInfoProvider)
	if !ok {
		return ClientRuntimeInfo{}, false
	}
	return provider.RuntimeInfo(), true
}

type ContextWindowProvider interface {
	ContextWindow() int64
}

// MaxOutputTokensProvider is an optional capability that a Client may implement
// to expose the model's configured max output tokens (per-request completion
// budget). Callers can use this for logging/observability without importing
// provider-specific types.
type MaxOutputTokensProvider interface {
	MaxOutputTokens() int64
}

// ModelNameProvider is an optional capability that a Client may implement
// to expose the configured model name for observability (usage logging).
type ModelNameProvider interface {
	ModelName() string
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

// ReasoningEffortRequest is an optional request capability. Keeping it out of
// Request preserves compatibility with custom request implementations.
type ReasoningEffortRequest interface {
	ReasoningEffort() string
	SetReasoningEffort(string)
}

// DefaultReasoningEffortRequest is an optional request capability carrying a
// fallback effort. Providers use it only when the normally resolved request or
// model effort is empty or "default". Keeping it separate from
// ReasoningEffortRequest lets callers such as Plan Mode provide default
// behavior without overriding explicit request, Session, Agent, or model
// configuration.
type DefaultReasoningEffortRequest interface {
	DefaultReasoningEffort() string
	SetDefaultReasoningEffort(string)
}

func RequestReasoningEffort(req Request) string {
	if capable, ok := req.(ReasoningEffortRequest); ok {
		return capable.ReasoningEffort()
	}
	return ""
}

func SetRequestReasoningEffort(req Request, effort string) bool {
	if capable, ok := req.(ReasoningEffortRequest); ok {
		capable.SetReasoningEffort(effort)
		return true
	}
	return false
}

func RequestDefaultReasoningEffort(req Request) string {
	if capable, ok := req.(DefaultReasoningEffortRequest); ok {
		return capable.DefaultReasoningEffort()
	}
	return ""
}

func SetRequestDefaultReasoningEffort(req Request, effort string) bool {
	if capable, ok := req.(DefaultReasoningEffortRequest); ok {
		capable.SetDefaultReasoningEffort(effort)
		return true
	}
	return false
}

type Response interface {
	Message() <-chan Delta
	Error() <-chan error
	Tokens() Tokens
}

// ResponseRuntimeInfoProvider exposes the concrete model selected for one
// response. It is response-scoped so concurrent calls through the same client
// cannot overwrite each other's attribution data.
type ResponseRuntimeInfoProvider interface {
	RuntimeInfo() ClientRuntimeInfo
}

// ResponseRuntimeInfo returns the concrete runtime metadata attached to resp.
// The boolean is false when the response does not expose call-level metadata.
func ResponseRuntimeInfo(resp Response) (ClientRuntimeInfo, bool) {
	provider, ok := resp.(ResponseRuntimeInfoProvider)
	if !ok {
		return ClientRuntimeInfo{}, false
	}
	return provider.RuntimeInfo(), true
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
	CompletionTokens    int64
	PromptTokens        int64
	CachedPromptTokens  int64
	CacheCreationTokens int64
	TotalTokens         int64
}

type Apply struct {
	ToolUse  []ToolCall
	Continue bool
	Abort    bool
}
