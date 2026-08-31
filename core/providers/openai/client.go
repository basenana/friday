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
	// host is the configured base URL, used to detect third-party
	// OpenAI-compatible endpoints that need vendor-specific fields.
	host string
}

// reasoningOpts returns per-request options for thinking-mode control.
// ReasoningEffortNone disables thinking via the top-level "thinking" field,
// which the OpenAI SDK params do not model. These fields follow the
// MiniMax-style dialect: they are only attached for third-party hosts,
// because api.openai.com rejects unknown top-level fields.
func (c *client) reasoningOpts() []option.RequestOption {
	if !isThirdPartyHost(c.host, "api.openai.com") {
		return nil
	}
	var opts []option.RequestOption
	if c.model.ReasoningSplit {
		opts = append(opts, option.WithJSONSet("reasoning_split", true))
	}
	if c.model.ReasoningEffort == providers.ReasoningEffortNone {
		opts = append(opts, option.WithJSONSet("thinking", map[string]string{"type": "disabled"}))
	}
	return opts
}

// isThirdPartyHost reports whether the configured base URL points at a host
// other than the vendor's official API endpoint. An empty base URL means the
// SDK default, i.e. the official endpoint.
func isThirdPartyHost(baseURL, officialHost string) bool {
	if baseURL == "" {
		return false
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host != officialHost && !strings.HasSuffix(u.Host, "."+officialHost)
}

func (c *client) ContextWindow() int64 {
	return c.model.ContextWindow
}

func (c *client) MaxOutputTokens() int64 {
	return c.model.MaxTokens
}

func (c *client) ModelName() string {
	return string(c.model.Name)
}

func (c *client) Completion(ctx context.Context, request providers.Request) providers.Response {
	c.logger.Infow("llm processing...")
	ctx, span := tracing.Start(ctx, "llm.openai.completion",
		tracing.WithAttributes(tracing.String("model", string(c.model.Name))),
	)
	resp := newResponse(request)
	resp.logger = c.logger
	go func() {
		defer span.End()
		defer resp.close()
		var (
			p       = c.chatCompletionNewParams(request)
			startAt = time.Now()
			err     error
		)

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
			sec := time.Since(startAt).Seconds()
			if sec < 1 {
				sec = 1
			}
			tps := float64(tokens.CompletionTokens) / sec
			c.logger.Infow("completion-with-streaming finish", "elapsed", time.Since(startAt).String(), "tps", fmt.Sprintf("%.2f", tps))
		}()

		var retries int

	Retry:
		if err = c.apiLimiter.Wait(ctx); err != nil {
			c.logger.Errorw("new completion stream error", "err", err)
			resp.fail(err)
			return
		}
		if time.Since(startAt).Seconds() > 1 {
			c.logger.Infow("client-side llm api throttled", "wait", time.Since(startAt).String())
		}

		stream := c.openai.Chat.Completions.NewStreaming(ctx, *p, c.reasoningOpts()...)

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
			if common.IsRetriableError(err) && retries < common.MaxRetriableAttempts && resp.canRetry() {
				retries++
				resp.resetForRetry()
				backoff := common.RetryBackoffDelay(retries)
				c.logger.Warnw("retriable LLM error, retrying", "attempt", retries, "backoff", backoff, "err", err)
				if err = common.WaitBackoff(ctx, backoff); err != nil {
					resp.fail(err)
					return
				}
				goto Retry
			}
			switch {
			case !common.IsRetriableError(err):
				// Not retriable; fall through to fail below.
			case !resp.canRetry():
				c.logger.Warnw("retriable LLM stream error, not retrying after partial output", "err", err)
			default:
				c.logger.Warnw("retriable LLM stream error, retry budget exhausted", "attempts", retries, "err", err)
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

	var retries int

Retry:
	if err = c.apiLimiter.Wait(ctx); err != nil {
		c.logger.Errorw("new completion error", "err", err)
		return "", err
	}
	if time.Since(startAt).Seconds() > 1 {
		c.logger.Infow("client-side llm api throttled", "wait", time.Since(startAt).String())
	}

	opts := append(c.reasoningOpts(),
		option.WithJSONSet("stream", false), // for some model using stream as default
	)
	response, err := c.openai.Chat.Completions.New(ctx, *p, opts...)
	if err != nil {
		if common.IsRetriableError(err) && retries < common.MaxRetriableAttempts {
			retries++
			backoff := common.RetryBackoffDelay(retries)
			c.logger.Warnw("retriable LLM error, retrying", "attempt", retries, "backoff", backoff, "err", err)
			if err = common.WaitBackoff(ctx, backoff); err != nil {
				return "", err
			}
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
	if c.model.MaxTokens > 0 {
		p.MaxCompletionTokens = param.NewOpt(c.model.MaxTokens)
	}
	if c.model.FrequencyPenalty != nil {
		p.FrequencyPenalty = param.NewOpt(*c.model.FrequencyPenalty)
	}
	if c.model.PresencePenalty != nil {
		p.PresencePenalty = param.NewOpt(*c.model.PresencePenalty)
	}
	if e := c.model.ReasoningEffort; e != "" && e != providers.ReasoningEffortDefault && e != providers.ReasoningEffortNone {
		p.ReasoningEffort = shared.ReasoningEffort(e)
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
			for _, image := range msg.ImageContents() {
				switch image.Type {
				case types.ImageTypeURL:
					contentParts = append(contentParts,
						openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
							URL: image.URL,
						}),
					)
				case types.ImageTypeBase64:
					// OpenAI supports data URI format
					dataURI := fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data)
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
		host:       host,
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
	emitted            bool
	// failed marks the response as failed so close() skips flushing
	// buffered (truncated) deltas.
	failed   bool
	thinking *thinkingStreamParser

	logger logger.Logger
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
		r.emitParsedDeltas(r.thinking.write(chunk.Delta.Content))
	}

	reasoningEmitted := false
	for _, reasoning := range deltaReasoningDetails(chunk.Delta) {
		if reasoning != "" {
			r.emit(providers.Delta{Reasoning: reasoning})
			reasoningEmitted = true
		}
	}
	if !reasoningEmitted {
		if reasoning := deltaExtraString(chunk.Delta, "reasoning_content"); reasoning != "" {
			r.emit(providers.Delta{Reasoning: reasoning})
			reasoningEmitted = true
		}
	}
	if !reasoningEmitted {
		if reasoning := deltaExtraString(chunk.Delta, "reasoning"); reasoning != "" {
			r.emit(providers.Delta{Reasoning: reasoning})
		}
	}
	if sig := deltaExtraString(chunk.Delta, "reasoning_content_signature"); sig != "" {
		r.emit(providers.Delta{ReasoningSignature: sig})
	}
}

// emitParsedDeltas forwards parsed stream deltas (Content and Reasoning)
// downstream, accumulating Content for token fallback calculation.
func (r *response) emitParsedDeltas(deltas []providers.Delta) {
	for _, delta := range deltas {
		r.accumulatedContent += delta.Content
		r.emit(delta)
	}
}

func (r *response) flushToolUse() {
	if r.incompleteTool.ID == "" {
		return
	}
	arguments := r.incompleteTool.Arguments
	toolError := ""
	if normalized, errMsg, ok := common.NormalizeToolUseArguments(arguments, r.incompleteTool.Name); !ok {
		if r.logger != nil {
			r.logger.Warnw("non-object tool_use arguments emitted; replacing with empty object",
				"tool", r.incompleteTool.Name, "raw", common.Truncate(arguments, 80))
		}
		arguments = normalized
		toolError = errMsg
	}
	r.emit(providers.Delta{
		ToolUse: []providers.ToolCall{{
			ID:        r.incompleteTool.ID,
			Name:      r.incompleteTool.Name,
			Arguments: arguments,
			Error:     toolError,
		}},
	})
	r.incompleteTool = struct {
		ID        string
		Name      string
		Arguments string
	}{}
}

func (r *response) updateUsage(chunk openai.CompletionUsage) {
	r.AddTokens(providers.Tokens{
		CompletionTokens:   chunk.CompletionTokens,
		PromptTokens:       chunk.PromptTokens,
		CachedPromptTokens: chunk.PromptTokensDetails.CachedTokens,
		TotalTokens:        chunk.TotalTokens,
	})
}

// applyTokenFallback fills in token counts using FuzzyTokens if API didn't return them
func (r *response) applyTokenFallback(requestMessages []types.Message) {
	overhead := session.EstimateRequestOverhead(r.request)
	tokens := r.Tokens()
	tokens.PromptTokens, tokens.CompletionTokens, tokens.TotalTokens =
		common.ApplyTokenFallback(tokens.PromptTokens, tokens.CompletionTokens, r.accumulatedContent, requestMessages, overhead)
	r.SetTokens(tokens)
}

func (r *response) emit(delta providers.Delta) {
	r.emitted = true
	r.Stream <- delta
}

// canRetry reports whether the response is safe to retry: once any delta has
// been emitted downstream, retrying would duplicate output.
func (r *response) canRetry() bool {
	return !r.emitted
}

func (r *response) resetForRetry() {
	r.SetTokens(providers.Tokens{})
	r.incompleteTool = struct {
		ID        string
		Name      string
		Arguments string
	}{}
	r.accumulatedContent = ""
	r.emitted = false
	r.thinking = newThinkingStreamParser()
}

func (r *response) fail(err error) {
	r.failed = true
	r.Err <- err
}

func (r *response) close() {
	if !r.failed {
		// Flush buffered deltas only for a successful stream; after a failure
		// the buffered text is a truncated fragment and must not be re-emitted
		// as Content.
		r.emitParsedDeltas(r.thinking.flush())
		r.flushToolUse()
	}
	close(r.Stream)
	close(r.Err)
}

func newResponse(req providers.Request) *response {
	return &response{
		CommonResponse: providers.NewCommonResponse(),
		request:        req,
		thinking:       newThinkingStreamParser(),
	}
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

type reasoningDetail struct {
	Text  string `json:"text"`
	Index int    `json:"index"`
}

// deltaReasoningDetails extracts reasoning text from the `reasoning_details`
// field that MiniMax uses for its OpenAI-compatible endpoint. The field may be
// an array of objects (each carrying `text` and an optional `index`), a single
// object, or a plain string.
func deltaReasoningDetails(delta openai.ChatCompletionChunkChoiceDelta) []string {
	raw, ok := deltaRawJSON(delta, "reasoning_details")
	if !ok {
		return nil
	}

	var details []reasoningDetail
	if err := json.Unmarshal(raw, &details); err == nil {
		return reasoningDetailTexts(details)
	}

	var texts []string
	if err := json.Unmarshal(raw, &texts); err == nil {
		return texts
	}

	var detail reasoningDetail
	if err := json.Unmarshal(raw, &detail); err == nil && detail.Text != "" {
		return []string{detail.Text}
	}

	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []string{text}
	}
	return nil
}

func reasoningDetailTexts(details []reasoningDetail) []string {
	// MiniMax tags each segment with a monotonically increasing index. Only
	// reorder when every segment carries a usable index; otherwise keep the
	// supplied order (indexless payloads fall through here).
	allIndexed := len(details) > 0
	ascending := true
	for i, detail := range details {
		if detail.Index < 0 {
			allIndexed = false
		}
		if i > 0 && details[i-1].Index > detail.Index {
			ascending = false
		}
	}
	if allIndexed && !ascending {
		sort.SliceStable(details, func(i, j int) bool { return details[i].Index < details[j].Index })
	}

	texts := make([]string, 0, len(details))
	for _, detail := range details {
		texts = append(texts, detail.Text)
	}
	return texts
}

// deltaRawJSON returns the raw JSON value for an unknown delta field, checking
// both openai-go's ExtraFields map and the delta's RawJSON fallback.
func deltaRawJSON(delta openai.ChatCompletionChunkChoiceDelta, key string) ([]byte, bool) {
	if field, ok := delta.JSON.ExtraFields[key]; ok && field.Valid() {
		return []byte(field.Raw()), true
	}

	raw := strings.TrimSpace(delta.RawJSON())
	if raw == "" {
		return nil, false
	}

	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return nil, false
	}
	rawValue, ok := payload[key]
	if !ok {
		return nil, false
	}
	return rawValue, true
}

func normalizeOpenAIToolMessages(messages []types.Message) []types.Message {
	messages = common.RepairToolHistory(messages)
	if len(messages) == 0 {
		return messages
	}

	normalized := make([]types.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		if msg.Role == types.RoleAssistant && len(msg.ToolCalls) > 0 {
			resultMessages := collectImmediateOpenAIToolResults(messages, i+1)
			assistantMsg, toolNames, exactMatch := normalizeOpenAIAssistantToolUseMessage(msg, resultMessages)
			normalized = append(normalized, assistantMsg)

			if len(resultMessages) > 0 {
				normalized = append(normalized, normalizeOpenAIToolResultMessages(resultMessages, toolNames, exactMatch)...)
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

// normalizeOpenAIAssistantToolUseMessage keeps the assistant tool_calls block
// only when it pairs exactly with the following tool results (same IDs, same
// order, all arguments valid JSON objects). Many OpenAI-compatible endpoints
// (GLM, MiniMax, vLLM, ...) reject partial downgrades harder than a fully
// textual replay, so any mismatch downgrades the whole batch.
func normalizeOpenAIAssistantToolUseMessage(msg types.Message, resultMessages []types.Message) (types.Message, map[string]string, bool) {
	toolUseIDs, toolNames := openAIToolUseIDsAndNames(msg)
	resultIDs := openAIToolResultIDs(resultMessages)
	exactMatch := exactOpenAIToolPairing(toolUseIDs, resultIDs) && openAIToolCallsHaveJSONObjectArguments(msg.ToolCalls)

	normalized := msg
	normalized.ToolCalls = make([]types.ToolCall, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		if exactMatch {
			normalized.ToolCalls = append(normalized.ToolCalls, tc)
			continue
		}
		normalized.Content = appendOpenAIFallbackText(normalized.Content, formatOpenAIToolUseFallback(tc))
	}
	return normalized, toolNames, exactMatch
}

func openAIToolCallsHaveJSONObjectArguments(toolCalls []types.ToolCall) bool {
	for _, tc := range toolCalls {
		if _, ok := common.ParseToolUseArguments(tc.Arguments); !ok {
			return false
		}
	}
	return true
}

func normalizeOpenAIToolResultMessages(messages []types.Message, toolNames map[string]string, exactMatch bool) []types.Message {
	normalized := make([]types.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.ToolResult == nil {
			normalized = append(normalized, msg)
			continue
		}
		if exactMatch {
			normalized = append(normalized, msg)
			continue
		}
		normalized = append(normalized, convertOpenAIToolResultToText(msg, toolNames))
	}
	return normalized
}

func openAIToolUseIDsAndNames(msg types.Message) ([]string, map[string]string) {
	ids := make([]string, 0, len(msg.ToolCalls))
	names := make(map[string]string, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		ids = append(ids, tc.ID)
		names[tc.ID] = tc.Name
	}
	return ids, names
}

func openAIToolResultIDs(messages []types.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.ToolResult == nil {
			continue
		}
		ids = append(ids, msg.ToolResult.CallID)
	}
	return ids
}

func exactOpenAIToolPairing(toolUseIDs, toolResultIDs []string) bool {
	if len(toolUseIDs) != len(toolResultIDs) {
		return false
	}
	for i := range toolUseIDs {
		if toolUseIDs[i] != toolResultIDs[i] {
			return false
		}
	}
	return len(toolUseIDs) > 0
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
