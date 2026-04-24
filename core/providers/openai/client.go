package openai

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
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"golang.org/x/time/rate"
)

type client struct {
	openai     openai.Client
	model      Model
	apiLimiter *rate.Limiter
	logger     logger.Logger
}

func (c *client) ContextWindow() int64 {
	return c.model.ContextWindow
}

func (c *client) Completion(ctx context.Context, request providers.Request) providers.Response {
	c.logger.Infow("llm processing...")
	ctx, span := tracing.Start(ctx, "llm.openai.completion",
		tracing.WithAttributes(tracing.String("model", string(c.model.Name))),
	)
	resp := newResponse(request)
	go func() {
		defer span.End()
		defer resp.close()
		var (
			p       = c.chatCompletionNewParams(request)
			startAt = time.Now()
			err     error
		)

		defer func() {
			span.SetAttributes(
				tracing.Int("prompt_tokens", resp.Token.PromptTokens),
				tracing.Int("completion_tokens", resp.Token.CompletionTokens),
			)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(tracing.StatusError, err.Error())
			} else {
				span.SetStatus(tracing.StatusOK, "")
			}
			sec := time.Since(startAt).Seconds()
			if sec < 1 {
				sec = 1
			}
			tps := float64(resp.Token.CompletionTokens) / sec
			c.logger.Infow("completion-with-streaming finish", "elapsed", time.Since(startAt).String(), "tps", fmt.Sprintf("%.2f", tps))
		}()

	Retry:
		if err = c.apiLimiter.Wait(ctx); err != nil {
			c.logger.Errorw("new completion stream error", "err", err)
			resp.fail(err)
			return
		}
		if time.Since(startAt).Seconds() > 1 {
			c.logger.Infow("client-side llm api throttled", "wait", time.Since(startAt).String())
		}

		stream := c.openai.Chat.Completions.NewStreaming(ctx, *p)

		for stream.Next() {
			chunk := stream.Current()
			resp.updateUsage(chunk.Usage)

			if len(chunk.Choices) == 0 {
				continue
			}

			ch := chunk.Choices[0]
			resp.nextChoice(ch)
		}

		if err = stream.Err(); err != nil {
			if isTooManyError(err) {
				time.Sleep(time.Second * 10)
				c.logger.Warn("too many requests try again")
				goto Retry
			}
			c.logger.Errorw("completion stream error", "err", err)
			resp.fail(err)
			return
		}

		// Fallback: if API didn't return token counts, estimate using FuzzyTokens
		resp.applyTokenFallback(request.Messages())
	}()
	return resp
}

func (c *client) CompletionNonStreaming(ctx context.Context, request providers.Request) (_ string, retErr error) {
	ctx, span := tracing.Start(ctx, "llm.openai.completion_sync",
		tracing.WithAttributes(tracing.String("model", string(c.model.Name))),
	)
	defer span.End()
	defer func() { tracing.DeferStatus(span, &retErr) }()

	c.logger.Infow("llm processing...")
	var (
		p       = c.chatCompletionNewParams(request)
		startAt = time.Now()
		err     error
	)

	defer func() {
		c.logger.Infow("completion-non-streaming finish", "elapsed", time.Since(startAt).String())
	}()

Retry:
	if err = c.apiLimiter.Wait(ctx); err != nil {
		c.logger.Errorw("new completion error", "err", err)
		return "", err
	}
	if time.Since(startAt).Seconds() > 1 {
		c.logger.Infow("client-side llm api throttled", "wait", time.Since(startAt).String())
	}

	response, err := c.openai.Chat.Completions.New(ctx, *p,
		[]option.RequestOption{
			option.WithJSONSet("stream", false), // for some model using stream as default
		}...)
	if err != nil {
		if isTooManyError(err) {
			time.Sleep(time.Second * 10)
			c.logger.Warn("too many requests try again")
			goto Retry
		}
		c.logger.Errorw("completion error", "err", err)
		return "", err
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no completion choices returned")
	}

	return response.Choices[0].Message.Content, nil
}

func (c *client) StructuredPredict(ctx context.Context, request providers.Request, model any) (retErr error) {
	ctx, span := tracing.Start(ctx, "llm.openai.structured_predict",
		tracing.WithAttributes(tracing.String("model", string(c.model.Name))),
	)
	defer span.End()
	defer func() { tracing.DeferStatus(span, &retErr) }()

	messages := request.Messages()
	if len(messages) == 0 || messages[0].Content == "" {
		return fmt.Errorf("user request is empty")
	}
	prompt := DEFAULT_STRUCTURED_PREDICT_PROMPT
	prompt = strings.ReplaceAll(prompt, "{insert_user_request_here}", messages[0].Content)
	schemaRaw, _ := json.Marshal(jsonschema.Reflect(model))
	prompt = strings.ReplaceAll(prompt, "{insert_json_schema_here}", string(schemaRaw))

	return common.StructuredPredictWithFallback(
		ctx,
		providers.NewPromptRequest(prompt),
		model,
		c.CompletionNonStreaming,
		c.Completion,
		c.logger,
	)
}

func (c *client) chatCompletionNewParams(request providers.Request) *openai.ChatCompletionNewParams {
	p := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{},
		Model:    c.model.Name,
		TopP:     param.NewOpt(1.0),
		N:        param.NewOpt(int64(1)),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}

	if c.model.Temperature != nil {
		p.Temperature = param.NewOpt(*c.model.Temperature)
	}
	if c.model.FrequencyPenalty != nil {
		p.FrequencyPenalty = param.NewOpt(*c.model.FrequencyPenalty)
	}
	if c.model.PresencePenalty != nil {
		p.PresencePenalty = param.NewOpt(*c.model.PresencePenalty)
	}
	if key := request.PromptCacheKey(); key != "" {
		p.PromptCacheKey = param.NewOpt(key)
	}

	messages := normalizeOpenAIToolMessages(request.Messages())

	thinkingMode := false
	for _, msg := range messages {
		if msg.Role == types.RoleAssistant && assistantReasoningContent(msg) != "" {
			thinkingMode = true
			break
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case types.RoleSystem:
			p.Messages = append(p.Messages,
				openai.SystemMessage(msg.Content),
			)

		case types.RoleUser:
			var contentParts []openai.ChatCompletionContentPartUnionParam

			// Add text content
			if msg.Content != "" {
				contentParts = append(contentParts, openai.TextContentPart(msg.Content))
			}

			// Add image content
			if msg.Image != nil {
				switch msg.Image.Type {
				case types.ImageTypeURL:
					contentParts = append(contentParts,
						openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
							URL: msg.Image.URL,
						}),
					)
				case types.ImageTypeBase64:
					// OpenAI supports data URI format
					dataURI := fmt.Sprintf("data:%s;base64,%s", msg.Image.MediaType, msg.Image.Data)
					contentParts = append(contentParts,
						openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
							URL: dataURI,
						}),
					)
				}
			}

			p.Messages = append(p.Messages, openai.UserMessage(contentParts))

		case types.RoleAgent:
			p.Messages = append(p.Messages,
				openai.UserMessage(msg.Content),
			)

		case types.RoleAssistant:
			p.Messages = append(p.Messages, assistantMessageParam(msg, thinkingMode))

		case types.RoleTool:
			if msg.ToolResult != nil {
				p.Messages = append(p.Messages,
					openai.ToolMessage(msg.ToolResult.Content, msg.ToolResult.CallID),
				)
			}
		}
	}

	tools := sortedToolDefines(request.ToolDefines())
	for _, t := range tools {
		p.Tools = append(p.Tools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.GetName(),
				Strict:      param.NewOpt(c.model.StrictMode),
				Description: param.NewOpt(t.GetDescription()),
				Parameters:  t.GetParameters(),
			},
			Type: "function",
		})
	}

	return &p
}

func New(host, apiKey string, model Model) providers.Client {
	return newClient(host, apiKey, model)
}

func newClient(host, apiKey string, model Model) *client {
	tp := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: model.InsecureSkipVerify}}
	// Use system proxy by default if no explicit proxy is configured
	if model.Proxy == "" {
		tp.Proxy = http.ProxyFromEnvironment
	} else {
		proxyUrl, err := url.Parse(model.Proxy)
		if err == nil {
			tp.Proxy = http.ProxyURL(proxyUrl)
		}
	}
	cli := &http.Client{
		Transport: tp,
		Timeout:   time.Hour,
	}

	oc := openai.NewClient(
		option.WithBaseURL(host),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(cli),
	)

	if model.QPM == 0 {
		model.QPM = 20
	}

	return &client{
		openai:     oc,
		model:      model,
		apiLimiter: rate.NewLimiter(rate.Limit(float64(model.QPM)/60), int(model.QPM/2)),
		logger:     logger.New("openai"),
	}
}

var _ providers.Client = (*client)(nil)

func assistantMessageParam(msg types.Message, thinkingMode bool) openai.ChatCompletionMessageParamUnion {
	reasoningContent := assistantReasoningContent(msg)
	if len(msg.ToolCalls) == 0 && reasoningContent == "" && !thinkingMode {
		return openai.AssistantMessage(msg.Content)
	}

	tmsg := &openai.ChatCompletionAssistantMessageParam{}
	if msg.Content != "" || (len(msg.ToolCalls) == 0 && (reasoningContent != "" || thinkingMode)) {
		tmsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
			OfString: param.NewOpt(msg.Content),
		}
	}
	if len(msg.ToolCalls) > 0 {
		toolCalls := make([]openai.ChatCompletionMessageToolCallParam, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			toolCalls[i] = openai.ChatCompletionMessageToolCallParam{
				ID:   tc.ID,
				Type: "function",
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			}
		}
		tmsg.ToolCalls = toolCalls
	}
	if reasoningContent != "" || thinkingMode {
		tmsg.SetExtraFields(map[string]any{"reasoning_content": reasoningContent})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: tmsg}
}

type response struct {
	*providers.CommonResponse

	request providers.Request

	incompleteTool struct {
		ID        string
		Name      string
		Arguments string
	}

	// accumulatedContent tracks the content for token fallback calculation
	accumulatedContent string
}

func (r *response) nextChoice(chunk openai.ChatCompletionChunkChoice) {
	if len(chunk.Delta.ToolCalls) > 0 {
		for _, tc := range chunk.Delta.ToolCalls {
			if tc.ID != "" {
				if r.incompleteTool.ID != "" {
					r.flushToolUse()
				}
				r.incompleteTool.ID = tc.ID
				r.incompleteTool.Name = tc.Function.Name
			}
			r.incompleteTool.Arguments += tc.Function.Arguments
		}
	}

	if chunk.Delta.Content != "" {
		r.accumulatedContent += chunk.Delta.Content
		r.Stream <- providers.Delta{Content: chunk.Delta.Content}
	}

	if reasoning := deltaExtraString(chunk.Delta, "reasoning_content"); reasoning != "" {
		r.Stream <- providers.Delta{Reasoning: reasoning}
	}
	if sig := deltaExtraString(chunk.Delta, "reasoning_content_signature"); sig != "" {
		r.Stream <- providers.Delta{ReasoningSignature: sig}
	}
}

func (r *response) flushToolUse() {
	if r.incompleteTool.ID == "" {
		return
	}
	r.Stream <- providers.Delta{
		ToolUse: []providers.ToolCall{{
			ID:        r.incompleteTool.ID,
			Name:      r.incompleteTool.Name,
			Arguments: r.incompleteTool.Arguments,
		}},
	}
	r.incompleteTool = struct {
		ID        string
		Name      string
		Arguments string
	}{}
}

func (r *response) updateUsage(chunk openai.CompletionUsage) {
	r.Token.CompletionTokens += chunk.CompletionTokens
	r.Token.PromptTokens += chunk.PromptTokens
	r.Token.CachedPromptTokens += chunk.PromptTokensDetails.CachedTokens
	r.Token.TotalTokens += chunk.TotalTokens
}

// applyTokenFallback fills in token counts using FuzzyTokens if API didn't return them
func (r *response) applyTokenFallback(requestMessages []types.Message) {
	overhead := session.EstimateRequestOverhead(r.request)
	r.Token.PromptTokens, r.Token.CompletionTokens, r.Token.TotalTokens =
		common.ApplyTokenFallback(r.Token.PromptTokens, r.Token.CompletionTokens, r.accumulatedContent, requestMessages, overhead)
}

func (r *response) fail(err error) { r.Err <- err }

func (r *response) close() {
	r.flushToolUse()
	close(r.Stream)
	close(r.Err)
}

func newResponse(req providers.Request) *response {
	return &response{CommonResponse: providers.NewCommonResponse(), request: req}
}

var _ providers.Response = (*response)(nil)

func sortedToolDefines(tools []providers.ToolDefine) []providers.ToolDefine {
	if len(tools) == 0 {
		return nil
	}
	sorted := append([]providers.ToolDefine(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].GetName() < sorted[j].GetName()
	})
	return sorted
}

func assistantReasoningContent(msg types.Message) string {
	return msg.Reasoning
}

func deltaExtraString(delta openai.ChatCompletionChunkChoiceDelta, key string) string {
	if field, ok := delta.JSON.ExtraFields[key]; ok && field.Valid() {
		var result string
		if json.Unmarshal([]byte(field.Raw()), &result) == nil {
			return result
		}
	}

	// Some OpenAI-compatible gateways do not always surface unknown delta fields
	// through openai-go's ExtraFields map, but RawJSON still contains them.
	raw := strings.TrimSpace(delta.RawJSON())
	if raw == "" {
		return ""
	}

	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return ""
	}

	rawValue, ok := payload[key]
	if !ok {
		return ""
	}

	var result string
	if json.Unmarshal(rawValue, &result) != nil {
		return ""
	}
	return result
}

func normalizeOpenAIToolMessages(messages []types.Message) []types.Message {
	if len(messages) == 0 {
		return messages
	}

	normalized := make([]types.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		if msg.Role == types.RoleAssistant && len(msg.ToolCalls) > 0 {
			resultMessages := collectImmediateOpenAIToolResults(messages, i+1)
			assistantMsg, pairedToolNames := normalizeOpenAIAssistantToolUseMessage(msg, resultMessages)
			normalized = append(normalized, assistantMsg)

			if len(resultMessages) > 0 {
				normalized = append(normalized, normalizeOpenAIToolResultMessages(resultMessages, pairedToolNames)...)
				i += len(resultMessages)
			}
			continue
		}

		if msg.Role == types.RoleTool && msg.ToolResult != nil {
			normalized = append(normalized, convertOpenAIToolResultToText(msg, nil))
			continue
		}

		normalized = append(normalized, msg)
	}

	return normalized
}

func collectImmediateOpenAIToolResults(messages []types.Message, start int) []types.Message {
	var collected []types.Message
	for i := start; i < len(messages); i++ {
		if messages[i].Role != types.RoleTool || messages[i].ToolResult == nil {
			break
		}
		collected = append(collected, messages[i])
	}
	return collected
}

func normalizeOpenAIAssistantToolUseMessage(msg types.Message, resultMessages []types.Message) (types.Message, map[string]string) {
	resultIDs := make(map[string]struct{})
	for _, resultMsg := range resultMessages {
		if resultMsg.ToolResult != nil {
			resultIDs[resultMsg.ToolResult.CallID] = struct{}{}
		}
	}

	pairedToolNames := make(map[string]string)
	normalized := msg
	normalized.ToolCalls = make([]types.ToolCall, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		if _, ok := resultIDs[tc.ID]; ok {
			pairedToolNames[tc.ID] = tc.Name
			normalized.ToolCalls = append(normalized.ToolCalls, tc)
			continue
		}
		normalized.Content = appendOpenAIFallbackText(normalized.Content, formatOpenAIToolUseFallback(tc))
	}
	return normalized, pairedToolNames
}

func normalizeOpenAIToolResultMessages(messages []types.Message, pairedToolNames map[string]string) []types.Message {
	normalized := make([]types.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.ToolResult == nil {
			normalized = append(normalized, msg)
			continue
		}
		if _, ok := pairedToolNames[msg.ToolResult.CallID]; ok {
			normalized = append(normalized, msg)
			continue
		}
		normalized = append(normalized, convertOpenAIToolResultToText(msg, pairedToolNames))
	}
	return normalized
}

func convertOpenAIToolResultToText(msg types.Message, toolNames map[string]string) types.Message {
	converted := msg
	converted.Role = types.RoleUser
	converted.Content = formatOpenAIToolResultFallback(msg.ToolResult, toolNames[msg.ToolResult.CallID])
	converted.ToolResult = nil
	return converted
}

func appendOpenAIFallbackText(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "\n" + extra
	}
}

func formatOpenAIToolUseFallback(tc types.ToolCall) string {
	name := tc.Name
	if name == "" {
		name = "unknown_tool"
	}
	args := strings.TrimSpace(tc.Arguments)
	if args == "" {
		return fmt.Sprintf("[tool call %s result omitted]", name)
	}
	return fmt.Sprintf("[tool call %s(%s) result omitted]", name, args)
}

func formatOpenAIToolResultFallback(result *types.ToolResult, toolName string) string {
	if result == nil {
		return "[historical tool result omitted]"
	}
	if toolName == "" {
		toolName = "unknown_tool"
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return fmt.Sprintf("[historical tool result omitted for tool call: %s]", toolName)
	}
	return fmt.Sprintf("[historical tool result for tool call %s] %s", toolName, content)
}
