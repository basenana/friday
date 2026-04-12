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
	"github.com/basenana/friday/core/types"
	"github.com/invopop/jsonschema"
	"golang.org/x/time/rate"
)

type Model struct {
	Name          string
	Temperature   *float64
	MaxTokens     *int64
	StrictMode    bool
	QPM           int64
	Proxy         string
	ContextWindow int64
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
	resp := newResponse()
	go func() {
		defer resp.close()
		var (
			params  = c.messageCreateParams(request)
			startAt = time.Now()
			err     error
		)

		defer func() {
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

func (c *client) CompletionNonStreaming(ctx context.Context, request providers.Request) (string, error) {
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

func (c *client) StructuredPredict(ctx context.Context, request providers.Request, model any) error {
	messages := request.Messages()
	if len(messages) == 0 || messages[0].Content == "" {
		return fmt.Errorf("user request is empty")
	}
	prompt := DEFAULT_STRUCTURED_PREDICT_PROMPT
	prompt = strings.ReplaceAll(prompt, "{insert_user_request_here}", messages[0].Content)
	schemaRaw, _ := json.Marshal(jsonschema.Reflect(model))
	prompt = strings.ReplaceAll(prompt, "{insert_json_schema_here}", string(schemaRaw))

	jsonbody, err := c.CompletionNonStreaming(ctx, providers.NewRequest(prompt))
	if err != nil {
		c.logger.Errorw("get completion error", "err", err)
		return err
	}

	err = common.ExtractJSON(jsonbody, model)
	if err != nil {
		c.logger.Errorw("failed to extract json", "content", jsonbody, "err", err)
		return err
	}
	return nil
}

func (c *client) messageCreateParams(request providers.Request) *anthropic.MessageNewParams {
	maxTokens := int64(4096)
	if c.model.MaxTokens != nil {
		maxTokens = *c.model.MaxTokens
	}

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
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				toolUse := anthropic.ToolUseBlockParam{
					ID:   tc.ID,
					Name: tc.Name,
				}
				if tc.Arguments != "" {
					var args any
					if err := json.Unmarshal([]byte(tc.Arguments), &args); err == nil {
						toolUse.Input = args
					}
				}
				contentBlocks = append(contentBlocks, anthropic.ContentBlockParamUnion{OfToolUse: &toolUse})
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

	return &params
}

func New(host, apiKey string, model Model) providers.Client {
	return newClient(host, apiKey, model)
}

func newClient(host, apiKey string, model Model) *client {
	tp := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
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

	incompleteTool struct {
		ID        string
		Name      string
		Arguments string
	}

	// accumulatedContent tracks the content for token fallback calculation
	accumulatedContent string
}

func (r *response) handleEvent(event anthropic.MessageStreamEventUnion) {
	switch event.Type {
	case "message_start":
		msg := event.AsMessageStart()
		r.Token.PromptTokens = msg.Message.Usage.InputTokens

	case "message_delta":
		delta := event.AsMessageDelta()
		r.Token.CompletionTokens += delta.Usage.OutputTokens

	case "message_stop":
		// message stopped

	case "content_block_start":
		block := event.AsContentBlockStart()
		if block.ContentBlock.ID != "" {
			r.incompleteTool.ID = block.ContentBlock.ID
			r.incompleteTool.Name = block.ContentBlock.Name
		}

	case "content_block_delta":
		delta := event.AsContentBlockDelta()
		if delta.Delta.Type == "text_delta" {
			r.accumulatedContent += delta.Delta.Text
			r.Stream <- providers.Delta{Content: delta.Delta.Text}
		} else if delta.Delta.Type == "input_json_delta" {
			r.incompleteTool.Arguments += delta.Delta.PartialJSON
		}

	case "content_block_stop":
		r.flushToolUse()
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

// applyTokenFallback fills in token counts using FuzzyTokens if API didn't return them
func (r *response) applyTokenFallback(requestMessages []types.Message) {
	r.Token.PromptTokens, r.Token.CompletionTokens, r.Token.TotalTokens =
		common.ApplyTokenFallback(r.Token.PromptTokens, r.Token.CompletionTokens, r.accumulatedContent, requestMessages)
}

func (r *response) fail(err error) { r.Err <- err }

func (r *response) close() {
	r.flushToolUse()
	close(r.Stream)
	close(r.Err)
}

func newResponse() *response {
	return &response{CommonResponse: providers.NewCommonResponse()}
}

var _ providers.Response = (*response)(nil)

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rate_limit") || strings.Contains(err.Error(), "429")
}
