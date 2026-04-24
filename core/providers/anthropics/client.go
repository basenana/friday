package anthropics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
}

func (c *client) ContextWindow() int64 {
	return c.model.ContextWindow
}

func (c *client) Completion(ctx context.Context, request providers.Request) providers.Response {
	c.logger.Infow("llm processing...")
	ctx, span := tracing.Start(ctx, "llm.anthropic.completion",
		tracing.WithAttributes(tracing.String("model", c.model.Name)),
	)
	resp := newResponse(request)
	go func() {
		defer span.End()
		defer resp.close()
		var (
			params  = c.messageCreateParams(request)
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

		stream := c.anthropic.Messages.NewStreaming(ctx, *params)

		for stream.Next() {
			event := stream.Current()
			resp.handleEvent(event)
		}

		if err = stream.Err(); err != nil {
			if isRateLimitError(err) {
				time.Sleep(time.Second * 10)
				c.logger.Warn("rate limited, trying again")
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

Retry:
	if err = c.apiLimiter.Wait(ctx); err != nil {
		c.logger.Errorw("new completion error", "err", err)
		return "", err
	}
	if time.Since(startAt).Seconds() > 1 {
		c.logger.Infow("client-side llm api throttled", "wait", time.Since(startAt).String())
	}

	message, err := c.anthropic.Messages.New(ctx, *params)
	if err != nil {
		if isRateLimitError(err) {
			time.Sleep(time.Second * 10)
			c.logger.Warn("rate limited, trying again")
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
	validToolUseIDs := make(map[string]struct{})
	invalidToolUseNames := make(map[string]string)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model.Name),
		MaxTokens: maxTokens,
	}

	if c.model.Temperature != nil {
		params.Temperature = anthropic.Float(*c.model.Temperature)
	}

	messages := request.Messages()
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
			if msg.Image != nil {
				switch msg.Image.Type {
				case types.ImageTypeURL:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlock(anthropic.URLImageSourceParam{
							URL:  msg.Image.URL,
							Type: "url",
						}),
					)
				case types.ImageTypeBase64:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlockBase64(msg.Image.MediaType, msg.Image.Data),
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
			if msg.Image != nil {
				switch msg.Image.Type {
				case types.ImageTypeURL:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlock(anthropic.URLImageSourceParam{
							URL:  msg.Image.URL,
							Type: "url",
						}),
					)
				case types.ImageTypeBase64:
					contentBlocks = append(contentBlocks,
						anthropic.NewImageBlockBase64(msg.Image.MediaType, msg.Image.Data),
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
					validToolUseIDs[tc.ID] = struct{}{}
					continue
				}
				if tc.ID != "" {
					invalidToolUseNames[tc.ID] = tc.Name
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
				if _, ok := validToolUseIDs[msg.ToolResult.CallID]; !ok {
					params.Messages = append(params.Messages, anthropic.MessageParam{
						Role: anthropic.MessageParamRoleUser,
						Content: []anthropic.ContentBlockParamUnion{
							anthropic.NewTextBlock(formatOrphanedHistoricalToolResult(msg.ToolResult, invalidToolUseNames[msg.ToolResult.CallID])),
						},
					})
					continue
				}
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

	if request.PromptCacheKey() != "" {
		if len(params.System) > 0 {
			params.System[len(params.System)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		if len(params.Messages) > 0 {
			lastMsg := &params.Messages[len(params.Messages)-1]
			if len(lastMsg.Content) > 0 {
				lastBlock := &lastMsg.Content[len(lastMsg.Content)-1]
				switch {
				case lastBlock.OfText != nil:
					lastBlock.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
				case lastBlock.OfToolResult != nil:
					lastBlock.OfToolResult.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
			}
		}
	}

	tools := request.ToolDefines()
	for _, t := range tools {
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

	var args any
	if tc.Arguments == "" || json.Unmarshal([]byte(tc.Arguments), &args) != nil {
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
}

func (r *response) handleEvent(event anthropic.MessageStreamEventUnion) {
	switch event.Type {
	case "message_start":
		msg := event.AsMessageStart()
		r.Token.PromptTokens = msg.Message.Usage.InputTokens +
			msg.Message.Usage.CacheReadInputTokens +
			msg.Message.Usage.CacheCreationInputTokens
		r.Token.CachedPromptTokens = msg.Message.Usage.CacheReadInputTokens

	case "message_delta":
		delta := event.AsMessageDelta()
		r.Token.CompletionTokens += delta.Usage.OutputTokens

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
			r.Stream <- providers.Delta{Content: delta.Delta.Text}
		case "input_json_delta":
			r.incompleteTool.Arguments += delta.Delta.PartialJSON
		case "thinking_delta":
			r.Stream <- providers.Delta{Reasoning: delta.Delta.Thinking}
		case "signature_delta":
			if r.currentThinking {
				r.currentSignature += delta.Delta.Signature
			}
		}

	case "content_block_stop":
		if r.currentThinking && r.currentSignature != "" {
			r.Stream <- providers.Delta{ReasoningSignature: r.currentSignature}
		}
		if r.currentRedacted != "" {
			r.Stream <- providers.Delta{RedactedThinking: r.currentRedacted}
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

// applyTokenFallback fills in token counts using FuzzyTokens if API didn't return them
func (r *response) applyTokenFallback(requestMessages []types.Message) {
	overhead := session.EstimateRequestOverhead(r.request)
	r.Token.PromptTokens, r.Token.CompletionTokens, r.Token.TotalTokens =
		common.ApplyTokenFallback(r.Token.PromptTokens, r.Token.CompletionTokens, r.accumulatedContent, requestMessages, overhead)
}

func (r *response) fail(err error) { r.Err <- err }

func (r *response) close() {
	if r.currentThinking && r.currentSignature != "" {
		r.Stream <- providers.Delta{ReasoningSignature: r.currentSignature}
	}
	if r.currentRedacted != "" {
		r.Stream <- providers.Delta{RedactedThinking: r.currentRedacted}
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

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rate_limit") || strings.Contains(err.Error(), "429")
}

func normalizeAnthropicToolMessages(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if len(messages) == 0 {
		return messages
	}

	normalized := make([]anthropic.MessageParam, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		if msg.Role == anthropic.MessageParamRoleAssistant && messageHasToolUse(msg) {
			resultMessages := collectImmediateToolResultMessages(messages, i+1)
			assistantMsg, pairedToolNames := normalizeAssistantToolUseMessage(msg, resultMessages)
			normalized = append(normalized, assistantMsg)

			if len(resultMessages) > 0 {
				if mergedResultMsg, ok := mergeToolResultMessages(resultMessages, pairedToolNames); ok {
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

func normalizeAssistantToolUseMessage(msg anthropic.MessageParam, resultMessages []anthropic.MessageParam) (anthropic.MessageParam, map[string]string) {
	resultIDs := make(map[string]struct{})
	for _, resultMsg := range resultMessages {
		for _, block := range resultMsg.Content {
			if block.OfToolResult != nil {
				resultIDs[block.OfToolResult.ToolUseID] = struct{}{}
			}
		}
	}

	pairedToolNames := make(map[string]string)
	normalized := msg
	normalized.Content = make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
	for _, block := range msg.Content {
		if block.OfToolUse == nil {
			normalized.Content = append(normalized.Content, block)
			continue
		}

		toolUse := block.OfToolUse
		if _, ok := resultIDs[toolUse.ID]; ok {
			pairedToolNames[toolUse.ID] = toolUse.Name
			normalized.Content = append(normalized.Content, block)
			continue
		}

		normalized.Content = append(normalized.Content, anthropic.NewTextBlock(formatAnthropicToolUseFallback(toolUse)))
	}
	return normalized, pairedToolNames
}

func mergeToolResultMessages(messages []anthropic.MessageParam, pairedToolNames map[string]string) (anthropic.MessageParam, bool) {
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
			if _, ok := pairedToolNames[toolResult.ToolUseID]; ok {
				merged.Content = append(merged.Content, block)
				continue
			}

			merged.Content = append(merged.Content, anthropic.NewTextBlock(
				formatAnthropicToolResultFallback(toolResult, pairedToolNames[toolResult.ToolUseID]),
			))
		}
	}

	return merged, len(merged.Content) > 0
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
