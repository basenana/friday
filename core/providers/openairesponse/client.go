package openairesponse

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
	"github.com/invopop/jsonschema"
	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"golang.org/x/time/rate"
)

const providerName = "openai-response"

type client struct {
	openai     openaisdk.Client
	model      Model
	apiLimiter *rate.Limiter
	logger     logger.Logger
}

func New(host, apiKey string, model Model) providers.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: model.InsecureSkipVerify}}
	if model.Proxy == "" {
		transport.Proxy = http.ProxyFromEnvironment
	} else if proxyURL, err := url.Parse(model.Proxy); err == nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	httpClient := &http.Client{Transport: transport, Timeout: time.Hour}
	sdkClient := openaisdk.NewClient(
		option.WithBaseURL(host),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	if model.QPM <= 0 {
		model.QPM = 20
	}
	burst := int(model.QPM / 2)
	if burst < 1 {
		burst = 1
	}

	return &client{
		openai:     sdkClient,
		model:      model,
		apiLimiter: rate.NewLimiter(rate.Limit(float64(model.QPM)/60), burst),
		logger:     logger.New("openai.response"),
	}
}

func (c *client) ContextWindow() int64   { return c.model.ContextWindow }
func (c *client) MaxOutputTokens() int64 { return c.model.MaxTokens }
func (c *client) ModelName() string      { return c.model.Name }

func (c *client) Completion(ctx context.Context, request providers.Request) providers.Response {
	return c.completionWithParams(ctx, request, c.responseNewParams(request))
}

func (c *client) completionWithParams(ctx context.Context, request providers.Request, params responses.ResponseNewParams) providers.Response {
	c.logger.Infow("llm processing...")
	ctx, span := tracing.Start(ctx, "llm.openai_response.completion",
		tracing.WithAttributes(tracing.String("model", c.model.Name)),
	)
	resp := newResponse(request)
	resp.logger = c.logger

	go func() {
		defer span.End()
		defer resp.close()
		startAt := time.Now()
		attempts := 1
		var err error

		defer func() {
			tokens := resp.Tokens()
			span.SetAttributes(
				tracing.Int("prompt_tokens", tokens.PromptTokens),
				tracing.Int("completion_tokens", tokens.CompletionTokens),
			)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(tracing.StatusError, err.Error())
			} else {
				span.SetStatus(tracing.StatusOK, "")
			}
			c.logger.Infow("completion-with-streaming finish", "elapsed", time.Since(startAt).String())
		}()

	Retry:
		if err = c.apiLimiter.Wait(ctx); err != nil {
			resp.fail(err)
			return
		}

		stream := c.openai.Responses.NewStreaming(ctx, params)
		for stream.Next() {
			if eventErr := resp.nextEvent(stream.Current()); eventErr != nil {
				err = eventErr
				_ = stream.Close()
				break
			}
		}
		if err == nil {
			err = stream.Err()
		}
		if err != nil {
			if common.IsRetriableError(err) && attempts < common.MaxAttempts && resp.canRetry() {
				attempts++
				resp.resetForRetry()
				backoff := common.RetryDelay(err, attempts-1)
				providers.NotifyRetry(ctx, providers.RetryEvent{Provider: providerName, Model: c.model.Name, Attempt: attempts, MaxAttempts: common.MaxAttempts, Error: err, Backoff: backoff})
				if err = common.WaitBackoff(ctx, backoff); err != nil {
					resp.fail(err)
					return
				}
				goto Retry
			}
			resp.fail(err)
			return
		}

		resp.applyTokenFallback(request.Messages())
	}()

	return resp
}

func (c *client) CompletionNonStreaming(ctx context.Context, request providers.Request) (_ string, retErr error) {
	ctx, span := tracing.Start(ctx, "llm.openai_response.completion_sync",
		tracing.WithAttributes(tracing.String("model", c.model.Name)),
	)
	defer span.End()
	defer func() { tracing.DeferStatus(span, &retErr) }()

	return common.ReadAllContent(ctx, c.Completion(ctx, request))
}

func (c *client) StructuredPredict(ctx context.Context, request providers.Request, model any) error {
	if firstUserContent(request.Messages()) == "" {
		return fmt.Errorf("user request is empty")
	}

	schemaBytes, err := json.Marshal(jsonschema.Reflect(model))
	if err != nil {
		return fmt.Errorf("marshal response schema: %w", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return fmt.Errorf("decode response schema: %w", err)
	}

	p := c.responseNewParams(request)
	p.Tools = nil
	p.Text.Format = responses.ResponseFormatTextConfigParamOfJSONSchema("structured_response", schema)
	p.Text.Format.OfJSONSchema.Strict = param.NewOpt(false)
	raw, nativeErr := c.nonStreaming(ctx, request, p)
	if nativeErr == nil {
		if err := common.ExtractJSON(raw, model); err == nil {
			return nil
		} else {
			nativeErr = err
		}
	}

	// Some third-party Responses-compatible endpoints do not implement
	// text.format yet. Preserve interoperability by falling back to prompt-based
	// JSON generation over the same Responses protocol.
	prompt := fmt.Sprintf("Return only one JSON object matching this JSON Schema.\n\nRequest:\n%s\n\nJSON Schema:\n%s", firstUserContent(request.Messages()), schemaBytes)
	fallbackRequest := providers.NewPromptRequest(prompt)
	if err := common.StructuredPredictWithFallback(
		ctx,
		fallbackRequest,
		model,
		c.CompletionNonStreaming,
		c.Completion,
		c.logger,
	); err != nil {
		return fmt.Errorf("native Responses structured output failed: %v; fallback failed: %w", nativeErr, err)
	}
	return nil
}

func firstUserContent(messages []types.Message) string {
	for _, message := range messages {
		if (message.Role == types.RoleUser || message.Role == types.RoleAgent) && strings.TrimSpace(message.Content) != "" {
			return message.Content
		}
	}
	return ""
}

func (c *client) nonStreaming(ctx context.Context, request providers.Request, params responses.ResponseNewParams) (string, error) {
	return common.ReadAllContent(ctx, c.completionWithParams(ctx, request, params))
}

func (c *client) responseNewParams(request providers.Request) responses.ResponseNewParams {
	p := responses.ResponseNewParams{
		Model: shared.ResponsesModel(c.model.Name),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responseInput(request),
		},
		Store: param.NewOpt(false),
		Include: []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		},
	}
	if prompt := strings.TrimSpace(request.SystemPrompt()); prompt != "" {
		p.Instructions = param.NewOpt(prompt)
	}
	if c.model.MaxTokens > 0 {
		p.MaxOutputTokens = param.NewOpt(c.model.MaxTokens)
	}
	if c.model.Temperature != nil {
		p.Temperature = param.NewOpt(*c.model.Temperature)
	}
	if key := request.PromptCacheKey(); key != "" {
		p.PromptCacheKey = param.NewOpt(key)
	}
	effort := providers.RequestReasoningEffort(request)
	if effort == "" {
		effort = c.model.ReasoningEffort
	}
	if effort != "" && effort != providers.ReasoningEffortDefault && effort != providers.ReasoningEffortNone {
		p.Reasoning.Effort = shared.ReasoningEffort(effort)
		p.Reasoning.Summary = shared.ReasoningSummaryAuto
	}

	tools := append([]providers.ToolDefine(nil), request.ToolDefines()...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].GetName() < tools[j].GetName() })
	for _, tool := range tools {
		param := responses.ToolParamOfFunction(tool.GetName(), tool.GetParameters(), c.model.StrictMode)
		param.OfFunction.Description = paramOptString(tool.GetDescription())
		p.Tools = append(p.Tools, param)
	}
	return p
}

func paramOptString(value string) param.Opt[string] {
	if value == "" {
		return param.Opt[string]{}
	}
	return param.NewOpt(value)
}

func responseInput(request providers.Request) responses.ResponseInputParam {
	messages := common.RepairToolHistory(request.History())
	input := make(responses.ResponseInputParam, 0, len(messages))
	seenReasoning := make(map[string]struct{})

	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == types.RoleAssistant && msg.ReasoningSignature != "" {
			if _, ok := seenReasoning[msg.ReasoningSignature]; !ok {
				if reasoning, ok := decodeReasoningItem(msg.ReasoningSignature); ok {
					input = append(input, reasoning)
					seenReasoning[msg.ReasoningSignature] = struct{}{}
				}
			}
		}

		if msg.Role == types.RoleAssistant && len(msg.ToolCalls) > 0 {
			results := immediateToolResults(messages, i+1)
			if exactToolPairing(msg.ToolCalls, results) {
				if msg.Content != "" {
					input = append(input, responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleAssistant))
				}
				for _, call := range msg.ToolCalls {
					input = append(input, responses.ResponseInputItemParamOfFunctionCall(call.Arguments, call.ID, call.Name))
				}
				for _, result := range results {
					input = append(input, responses.ResponseInputItemParamOfFunctionCallOutput(result.ToolResult.CallID, result.ToolResult.Content))
				}
				i += len(results)
				continue
			}

			content := msg.Content
			for _, call := range msg.ToolCalls {
				content = appendFallback(content, fmt.Sprintf("[historical tool call: %s] %s", call.Name, call.Arguments))
			}
			if content != "" {
				input = append(input, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleAssistant))
			}
			continue
		}

		switch msg.Role {
		case types.RoleSystem:
			// System guidance is sent through the top-level instructions field.
		case types.RoleUser, types.RoleAgent:
			input = append(input, userInputItem(msg))
		case types.RoleAssistant:
			if msg.Content != "" {
				input = append(input, responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleAssistant))
			}
		case types.RoleTool:
			if msg.ToolResult != nil {
				content := fmt.Sprintf("[historical tool result for call %s] %s", msg.ToolResult.CallID, msg.ToolResult.Content)
				input = append(input, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser))
			}
		}
	}
	return input
}

func userInputItem(msg types.Message) responses.ResponseInputItemUnionParam {
	images := msg.ImageContents()
	if len(images) == 0 {
		return responses.ResponseInputItemParamOfMessage(msg.Content, responses.EasyInputMessageRoleUser)
	}

	parts := make(responses.ResponseInputMessageContentListParam, 0, len(images)+1)
	if msg.Content != "" {
		parts = append(parts, responses.ResponseInputContentParamOfInputText(msg.Content))
	}
	for _, image := range images {
		imageURL := image.URL
		if image.Type == types.ImageTypeBase64 {
			imageURL = fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data)
		}
		part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
		part.OfInputImage.ImageURL = param.NewOpt(imageURL)
		parts = append(parts, part)
	}
	return responses.ResponseInputItemParamOfMessage(parts, responses.EasyInputMessageRoleUser)
}

func decodeReasoningItem(raw string) (responses.ResponseInputItemUnionParam, bool) {
	var item responses.ResponseReasoningItem
	if json.Unmarshal([]byte(raw), &item) != nil || item.ID == "" {
		return responses.ResponseInputItemUnionParam{}, false
	}
	paramItem := item.ToParam()
	return responses.ResponseInputItemUnionParam{OfReasoning: &paramItem}, true
}

func immediateToolResults(messages []types.Message, start int) []types.Message {
	var results []types.Message
	for i := start; i < len(messages); i++ {
		if messages[i].Role != types.RoleTool || messages[i].ToolResult == nil {
			break
		}
		results = append(results, messages[i])
	}
	return results
}

func exactToolPairing(calls []types.ToolCall, results []types.Message) bool {
	if len(calls) == 0 || len(calls) != len(results) {
		return false
	}
	for i, call := range calls {
		if call.ID == "" || results[i].ToolResult.CallID != call.ID {
			return false
		}
		if _, ok := common.ParseToolUseArguments(call.Arguments); !ok {
			return false
		}
	}
	return true
}

func appendFallback(content, fallback string) string {
	if content == "" {
		return fallback
	}
	return content + "\n\n" + fallback
}

var _ providers.Client = (*client)(nil)
