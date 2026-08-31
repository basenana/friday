package anthropics

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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
	"github.com/invopop/jsonschema"
	"golang.org/x/time/rate"
)

type Model struct {
	Name               string
	Temperature        *float64
	MaxTokens          *int64
	ReasoningEffort    string
	StrictMode         bool
	QPM                int64
	Proxy              string
	ContextWindow      int64
	InsecureSkipVerify bool
}

type client struct {
	anthropic  anthropic.Client
	model      Model
	apiLimiter *rate.Limiter
	logger     logger.Logger
	// host is the configured base URL, used to detect third-party
	// Anthropic-compatible endpoints that need vendor-specific fields.
	host string
}

// reasoningOpts returns per-request options for thinking-mode control.
// ReasoningEffortNone disables thinking via the top-level "reasoning" field,
// which the Anthropic SDK params do not model. The field is only understood
// by some Anthropic-compatible gateways; the real API rejects it with 400,
// so it is only attached when the base URL is not api.anthropic.com.
func (c *client) reasoningOpts() []option.RequestOption {
	if c.model.ReasoningEffort != providers.ReasoningEffortNone {
		return nil
	}
	if !isThirdPartyHost(c.host, "api.anthropic.com") {
		return nil
	}
	return []option.RequestOption{
		option.WithJSONSet("reasoning", map[string]string{"effort": "none"}),
	}
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
	if c.model.MaxTokens == nil {
		return 0
	}
	return *c.model.MaxTokens
}

func (c *client) ModelName() string {
	return c.model.Name
}

func (c *client) Completion(ctx context.Context, request providers.Request) providers.Response {
	c.logger.Infow("llm processing...")
	ctx, span := tracing.Start(ctx, "llm.anthropic.completion",
		tracing.WithAttributes(tracing.String("model", c.model.Name)),
	)
	resp := newResponse(request)
	resp.logger = c.logger
	go func() {
		defer span.End()
		defer resp.close()
		var (
			params  = c.messageCreateParams(request)
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

		stream := c.anthropic.Messages.NewStreaming(ctx, *params, c.reasoningOpts()...)

		for stream.Next() {
			event := stream.Current()
			resp.handleEvent(event)
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
	ctx, span := tracing.Start(ctx, "llm.anthropic.completion_sync",
		tracing.WithAttributes(tracing.String("model", c.model.Name)),
	)
	defer span.End()
	defer func() { tracing.DeferStatus(span, &retErr) }()

	c.logger.Infow("llm processing...")
	var (
		params  = c.messageCreateParams(request)
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

	message, err := c.anthropic.Messages.New(ctx, *params, c.reasoningOpts()...)
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

	if len(message.Content) == 0 {
		return "", fmt.Errorf("no completion content returned")
	}

	for _, content := range message.Content {
		if content.Type == "text" {
			return content.Text, nil
		}
	}

	return "", fmt.Errorf("no text content returned")
}

func (c *client) StructuredPredict(ctx context.Context, request providers.Request, model any) (retErr error) {
	ctx, span := tracing.Start(ctx, "llm.anthropic.structured_predict",
		tracing.WithAttributes(tracing.String("model", c.model.Name)),
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

func (c *client) messageCreateParams(request providers.Request) *anthropic.MessageNewParams {
	maxTokens := int64(4096)
	if c.model.MaxTokens != nil {
		maxTokens = *c.model.MaxTokens
	}
	systemPrompt := strings.TrimSpace(request.SystemPrompt())

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model.Name),
		MaxTokens: maxTokens,
	}

	if c.model.Temperature != nil {
		params.Temperature = anthropic.Float(*c.model.Temperature)
	}
	if e := c.model.ReasoningEffort; e != "" && e != providers.ReasoningEffortDefault && e != providers.ReasoningEffortNone {
		// The Anthropic API only accepts low/medium/high efforts; clamp the
		// higher generic levels (xhigh/max) down to high.
		if e == providers.ReasoningEffortXHigh || e == providers.ReasoningEffortMax {
			if c.logger != nil {
				c.logger.Warnw("reasoning effort not supported by Anthropic; clamping to high",
					"model", c.model.Name, "effort", e)
			}
			e = providers.ReasoningEffortHigh
		}
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(e)}
	}

	messages := common.RepairToolHistory(request.Messages())
	for _, msg := range messages {
		switch msg.Role {
		case types.RoleSystem:
			params.System = append(params.System, anthropic.TextBlockParam{
				Text: msg.Content,
			})

		case types.RoleUser:
			// Build content blocks
			var contentBlocks []anthropic.ContentBlockParamUnion

			// Add text content
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}

			// Add image content
			for _, image := range msg.ImageContents() {
				switch image.Type {
				case types.ImageTypeURL:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlock(anthropic.URLImageSourceParam{
							URL:  image.URL,
							Type: "url",
						}),
					)
				case types.ImageTypeBase64:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlockBase64(image.MediaType, image.Data),
					)
				}
			}

			params.Messages = append(params.Messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: contentBlocks,
			})

		case types.RoleAgent:
			// Build content blocks
			var contentBlocks []anthropic.ContentBlockParamUnion

			// Add text content
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}

			// Add image content
			for _, image := range msg.ImageContents() {
				switch image.Type {
				case types.ImageTypeURL:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlock(anthropic.URLImageSourceParam{
							URL:  image.URL,
							Type: "url",
						}),
					)
				case types.ImageTypeBase64:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlockBase64(image.MediaType, image.Data),
					)
				}
			}

			params.Messages = append(params.Messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: contentBlocks,
			})

		case types.RoleAssistant:
			var contentBlocks []anthropic.ContentBlockParamUnion
			// Thinking block must precede text/tool_use blocks per Anthropic API requirements
			contentBlocks = append(contentBlocks, anthropicThinkingBlocks(msg)...)
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				if block, ok := buildHistoricalToolUseBlock(tc); ok {
					contentBlocks = append(contentBlocks, block)
					continue
				}
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(formatInvalidHistoricalToolCall(tc)))
			}
			if len(contentBlocks) == 0 {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}
			params.Messages = append(params.Messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: contentBlocks,
			})

		case types.RoleTool:
			if msg.ToolResult != nil {
				params.Messages = append(params.Messages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewToolResultBlock(msg.ToolResult.CallID, msg.ToolResult.Content, false)},
				})
			}
		}
	}

	// Normalize historical tool calls/results into Anthropic's required shape:
	// one assistant tool_use message followed immediately by one user message
	// containing the paired tool_result blocks.
	params.Messages = normalizeAnthropicToolMessages(params.Messages)

	tools := request.ToolDefines()
	sortedTools := make([]providers.ToolDefine, len(tools))
	copy(sortedTools, tools)
	sort.Slice(sortedTools, func(i, j int) bool {
		return sortedTools[i].GetName() < sortedTools[j].GetName()
	})
	for _, t := range sortedTools {
		// GetParameters returns full schema with type/properties/required
		// Anthropic SDK expects just the properties part
		paramsMap := t.GetParameters()
		properties := make(map[string]any)
		var required []string

		if props, ok := paramsMap["properties"].(map[string]any); ok {
			properties = props
		}
		if req, ok := paramsMap["required"].([]any); ok {
			required = make([]string, len(req))
			for i, r := range req {
				if s, ok := r.(string); ok {
					required[i] = s
				}
			}
		}

		inputSchema := anthropic.ToolInputSchemaParam{
			Properties: properties,
			Required:   required,
			Type:       "object",
		}
		toolUnion := anthropic.ToolUnionParamOfTool(inputSchema, t.GetName())
		toolUnion.OfTool.Description = anthropic.String(t.GetDescription())
		params.Tools = append(params.Tools, toolUnion)
	}

	if request.PromptCacheKey() != "" {
		// Cache breakpoints: place them on stable prefixes so hooks that append
		// trailing messages don't invalidate the entire cache. Cache the last
		// system block, the last tool definition, and a message breakpoint
		// several positions from the end (so newly added trailing messages
		// don't shift the breakpoint each turn).
		const cacheTrailMessages = 3
		if len(params.System) > 0 {
			params.System[len(params.System)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		if len(params.Tools) > 0 {
			lastTool := &params.Tools[len(params.Tools)-1]
			if lastTool.OfTool != nil {
				lastTool.OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
		}
		setAnthropicMessageCacheBreakpoint(params.Messages, cacheTrailMessages)
	}

	// Anthropic requires at least one non-system message. Some internal callers
	// build prompt-only requests via NewRequest(prompt), which would otherwise
	// serialize as a system-only payload and fail at /v1/messages.
	if len(params.Messages) == 0 && systemPrompt != "" {
		params.System = nil
		params.Messages = append(params.Messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(systemPrompt)},
		})
	}

	return &params
}

func buildHistoricalToolUseBlock(tc types.ToolCall) (anthropic.ContentBlockParamUnion, bool) {
	if tc.ID == "" || tc.Name == "" {
		return anthropic.ContentBlockParamUnion{}, false
	}

	// Anthropic requires tool_use `input` to be a JSON object; arrays/scalars
	// would serialize into an invalid block and fail the whole request.
	args, ok := common.ParseToolUseArguments(tc.Arguments)
	if !ok {
		return anthropic.ContentBlockParamUnion{}, false
	}

	toolUse := anthropic.ToolUseBlockParam{
		ID:    tc.ID,
		Input: args,
		Name:  tc.Name,
	}
	return anthropic.ContentBlockParamUnion{OfToolUse: &toolUse}, true
}

func formatInvalidHistoricalToolCall(tc types.ToolCall) string {
	name := tc.Name
	if name == "" {
		name = "unknown_tool"
	}
	args := strings.TrimSpace(tc.Arguments)
	if args == "" {
		return fmt.Sprintf("[historical invalid tool call omitted: %s]", name)
	}
	return fmt.Sprintf("[historical invalid tool call omitted: %s(%s)]", name, args)
}

func formatOrphanedHistoricalToolResult(result *types.ToolResult, toolName string) string {
	if result == nil {
		return "[historical tool result omitted]"
	}
	if toolName == "" {
		toolName = "unknown_tool"
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		return fmt.Sprintf("[historical tool result omitted for invalid tool call: %s]", toolName)
	}
	return fmt.Sprintf("[historical tool result for invalid tool call %s] %s", toolName, content)
}

func New(host, apiKey string, model Model) providers.Client {
	return newClient(host, apiKey, model)
}

func newClient(host, apiKey string, model Model) *client {
	tp := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: model.InsecureSkipVerify}}
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

	anthropicClient := anthropic.NewClient(
		option.WithBaseURL(host),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(cli),
	)

	if model.QPM == 0 {
		model.QPM = 20
	}

	return &client{
		anthropic:  anthropicClient,
		model:      model,
		apiLimiter: rate.NewLimiter(rate.Limit(float64(model.QPM)/60), int(model.QPM/2)),
		logger:     logger.New("anthropics"),
		host:       host,
	}
}

var _ providers.Client = (*client)(nil)

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

	currentThinking  bool
	currentSignature string
	currentRedacted  string
	emitted          bool

	logger logger.Logger
}

func (r *response) handleEvent(event anthropic.MessageStreamEventUnion) {
	switch event.Type {
	case "message_start":
		msg := event.AsMessageStart()
		tokens := r.Tokens()
		tokens.PromptTokens = msg.Message.Usage.InputTokens +
			msg.Message.Usage.CacheReadInputTokens +
			msg.Message.Usage.CacheCreationInputTokens
		tokens.CachedPromptTokens = msg.Message.Usage.CacheReadInputTokens
		tokens.CacheCreationTokens = msg.Message.Usage.CacheCreationInputTokens
		r.SetTokens(tokens)

	case "message_delta":
		delta := event.AsMessageDelta()
		tokens := r.Tokens()
		tokens.CompletionTokens += delta.Usage.OutputTokens
		// Some Anthropic-compatible providers (e.g. GLM bigmodel) report zero
		// usage in message_start and only deliver real values (incl. cache
		// fields) in message_delta. Backfill when message_start gave us nothing.
		if tokens.PromptTokens == 0 {
			tokens.PromptTokens = delta.Usage.InputTokens +
				delta.Usage.CacheReadInputTokens +
				delta.Usage.CacheCreationInputTokens
			tokens.CachedPromptTokens = delta.Usage.CacheReadInputTokens
			tokens.CacheCreationTokens = delta.Usage.CacheCreationInputTokens
		}
		r.SetTokens(tokens)

	case "message_stop":
		// message stopped

	case "content_block_start":
		block := event.AsContentBlockStart()
		switch block.ContentBlock.Type {
		case "thinking":
			r.currentThinking = true
			r.currentSignature = block.ContentBlock.Signature
		case "redacted_thinking":
			r.currentThinking = false
			r.currentSignature = ""
			r.currentRedacted = block.ContentBlock.Data
		default:
			if block.ContentBlock.ID == "" {
				break
			}
			r.incompleteTool.ID = block.ContentBlock.ID
			r.incompleteTool.Name = block.ContentBlock.Name
		}

	case "content_block_delta":
		delta := event.AsContentBlockDelta()
		switch delta.Delta.Type {
		case "text_delta":
			r.accumulatedContent += delta.Delta.Text
			r.emit(providers.Delta{Content: delta.Delta.Text})
		case "input_json_delta":
			r.incompleteTool.Arguments += delta.Delta.PartialJSON
		case "thinking_delta":
			r.emit(providers.Delta{Reasoning: delta.Delta.Thinking})
		case "signature_delta":
			if r.currentThinking {
				r.currentSignature += delta.Delta.Signature
			}
		}

	case "content_block_stop":
		if r.currentThinking && r.currentSignature != "" {
			r.emit(providers.Delta{ReasoningSignature: r.currentSignature})
		}
		if r.currentRedacted != "" {
			r.emit(providers.Delta{RedactedThinking: r.currentRedacted})
		}
		r.currentThinking = false
		r.currentSignature = ""
		r.currentRedacted = ""
		r.flushToolUse()
	}
}

func anthropicThinkingBlocks(msg types.Message) []anthropic.ContentBlockParamUnion {
	var blocks []anthropic.ContentBlockParamUnion
	if msg.Reasoning != "" && msg.ReasoningSignature != "" {
		blocks = append(blocks, anthropic.NewThinkingBlock(msg.ReasoningSignature, msg.Reasoning))
	}
	if msg.RedactedThinking != "" {
		blocks = append(blocks, anthropic.NewRedactedThinkingBlock(msg.RedactedThinking))
	}
	return blocks
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
	r.currentThinking = false
	r.currentSignature = ""
	r.currentRedacted = ""
	r.emitted = false
}

func (r *response) fail(err error) { r.Err <- err }

func (r *response) close() {
	if r.currentThinking && r.currentSignature != "" {
		r.emit(providers.Delta{ReasoningSignature: r.currentSignature})
	}
	if r.currentRedacted != "" {
		r.emit(providers.Delta{RedactedThinking: r.currentRedacted})
	}
	r.currentThinking = false
	r.currentSignature = ""
	r.currentRedacted = ""
	r.flushToolUse()
	close(r.Stream)
	close(r.Err)
}

func newResponse(req providers.Request) *response {
	return &response{CommonResponse: providers.NewCommonResponse(), request: req}
}

var _ providers.Response = (*response)(nil)

func normalizeAnthropicToolMessages(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if len(messages) == 0 {
		return messages
	}

	normalized := make([]anthropic.MessageParam, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		if msg.Role == anthropic.MessageParamRoleAssistant && messageHasToolUse(msg) {
			resultMessages := collectImmediateToolResultMessages(messages, i+1)
			assistantMsg, toolNames, exactMatch := normalizeAssistantToolUseMessage(msg, resultMessages)
			normalized = append(normalized, assistantMsg)

			if len(resultMessages) > 0 {
				if mergedResultMsg, ok := mergeToolResultMessages(resultMessages, toolNames, exactMatch); ok {
					normalized = append(normalized, mergedResultMsg)
				}
				i += len(resultMessages)
			}
			continue
		}

		if msg.Role == anthropic.MessageParamRoleUser && messageHasToolResult(msg) {
			normalized = append(normalized, convertToolResultMessageToText(msg, nil))
			continue
		}

		normalized = append(normalized, msg)
	}

	return normalized
}

func collectImmediateToolResultMessages(messages []anthropic.MessageParam, start int) []anthropic.MessageParam {
	var collected []anthropic.MessageParam
	for i := start; i < len(messages); i++ {
		if messages[i].Role != anthropic.MessageParamRoleUser || !messageHasToolResult(messages[i]) {
			break
		}
		collected = append(collected, messages[i])
	}
	return collected
}

func normalizeAssistantToolUseMessage(msg anthropic.MessageParam, resultMessages []anthropic.MessageParam) (anthropic.MessageParam, map[string]string, bool) {
	toolUseIDs, toolNames := anthropicToolUseIDsAndNames(msg)
	resultIDs := anthropicToolResultIDs(resultMessages)
	exactMatch := exactToolPairing(toolUseIDs, resultIDs)

	normalized := msg
	normalized.Content = make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
	for _, block := range msg.Content {
		if block.OfToolUse == nil {
			normalized.Content = append(normalized.Content, block)
			continue
		}

		toolUse := block.OfToolUse
		if exactMatch {
			normalized.Content = append(normalized.Content, block)
			continue
		}

		normalized.Content = append(normalized.Content, anthropic.NewTextBlock(formatAnthropicToolUseFallback(toolUse)))
	}
	return normalized, toolNames, exactMatch
}

func mergeToolResultMessages(messages []anthropic.MessageParam, toolNames map[string]string, exactMatch bool) (anthropic.MessageParam, bool) {
	merged := anthropic.MessageParam{
		Role: anthropic.MessageParamRoleUser,
	}

	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.OfToolResult == nil {
				merged.Content = append(merged.Content, block)
				continue
			}

			toolResult := block.OfToolResult
			if exactMatch {
				merged.Content = append(merged.Content, block)
				continue
			}

			merged.Content = append(merged.Content, anthropic.NewTextBlock(
				formatAnthropicToolResultFallback(toolResult, toolNames[toolResult.ToolUseID]),
			))
		}
	}

	return merged, len(merged.Content) > 0
}

func anthropicToolUseIDsAndNames(msg anthropic.MessageParam) ([]string, map[string]string) {
	var ids []string
	names := make(map[string]string)
	for _, block := range msg.Content {
		if block.OfToolUse == nil {
			continue
		}
		ids = append(ids, block.OfToolUse.ID)
		names[block.OfToolUse.ID] = block.OfToolUse.Name
	}
	return ids, names
}

func anthropicToolResultIDs(messages []anthropic.MessageParam) []string {
	var ids []string
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.OfToolResult == nil {
				continue
			}
			ids = append(ids, block.OfToolResult.ToolUseID)
		}
	}
	return ids
}

func exactToolPairing(toolUseIDs, toolResultIDs []string) bool {
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

func convertToolResultMessageToText(msg anthropic.MessageParam, toolNames map[string]string) anthropic.MessageParam {
	converted := msg
	converted.Content = make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
	for _, block := range msg.Content {
		if block.OfToolResult == nil {
			converted.Content = append(converted.Content, block)
			continue
		}

		converted.Content = append(converted.Content, anthropic.NewTextBlock(
			formatAnthropicToolResultFallback(block.OfToolResult, toolNames[block.OfToolResult.ToolUseID]),
		))
	}
	return converted
}

func messageHasToolUse(msg anthropic.MessageParam) bool {
	for _, block := range msg.Content {
		if block.OfToolUse != nil {
			return true
		}
	}
	return false
}

func messageHasToolResult(msg anthropic.MessageParam) bool {
	for _, block := range msg.Content {
		if block.OfToolResult != nil {
			return true
		}
	}
	return false
}

func formatAnthropicToolUseFallback(toolUse *anthropic.ToolUseBlockParam) string {
	if toolUse == nil {
		return "[tool call result omitted]"
	}

	name := toolUse.Name
	if name == "" {
		name = "unknown_tool"
	}
	args, err := json.Marshal(toolUse.Input)
	if err != nil || string(args) == "" || string(args) == "null" {
		return fmt.Sprintf("[tool call %s result omitted]", name)
	}
	return fmt.Sprintf("[tool call %s(%s) result omitted]", name, string(args))
}

func formatAnthropicToolResultFallback(toolResult *anthropic.ToolResultBlockParam, toolName string) string {
	if toolResult == nil {
		return "[historical tool result omitted]"
	}
	return formatOrphanedHistoricalToolResult(&types.ToolResult{
		CallID:  toolResult.ToolUseID,
		Content: anthropicToolResultText(toolResult),
	}, toolName)
}

func anthropicToolResultText(toolResult *anthropic.ToolResultBlockParam) string {
	if toolResult == nil {
		return ""
	}

	var parts []string
	for _, block := range toolResult.Content {
		if text := block.GetText(); text != nil {
			parts = append(parts, *text)
			continue
		}
		if blockType := block.GetType(); blockType != nil {
			parts = append(parts, fmt.Sprintf("[non-text tool result block: %s]", *blockType))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// setAnthropicMessageCacheBreakpoint marks a cache breakpoint on the last
// content block of the message at index (len-messages-1-trailMessages). If
// that message has no cacheable block, walk backward until one is found.
// This keeps the cache prefix stable as new messages are appended.
func setAnthropicMessageCacheBreakpoint(messages []anthropic.MessageParam, trailMessages int) {
	cacheIdx := len(messages) - trailMessages - 1
	if cacheIdx < 0 {
		return
	}
	for msgIdx := cacheIdx; msgIdx >= 0; msgIdx-- {
		if setAnthropicCacheControlOnMessage(&messages[msgIdx]) {
			return
		}
	}
}

func setAnthropicCacheControlOnMessage(msg *anthropic.MessageParam) bool {
	for blockIdx := len(msg.Content) - 1; blockIdx >= 0; blockIdx-- {
		if setAnthropicCacheControlOnBlock(&msg.Content[blockIdx]) {
			return true
		}
	}
	return false
}

func setAnthropicCacheControlOnBlock(block *anthropic.ContentBlockParamUnion) bool {
	cacheControl := anthropic.NewCacheControlEphemeralParam()

	switch {
	case block.OfText != nil:
		block.OfText.CacheControl = cacheControl
	case block.OfImage != nil:
		block.OfImage.CacheControl = cacheControl
	case block.OfSearchResult != nil:
		block.OfSearchResult.CacheControl = cacheControl
	case block.OfToolUse != nil:
		block.OfToolUse.CacheControl = cacheControl
	case block.OfToolResult != nil:
		block.OfToolResult.CacheControl = cacheControl
	case block.OfServerToolUse != nil:
		block.OfServerToolUse.CacheControl = cacheControl
	default:
		return false
	}

	return true
}
