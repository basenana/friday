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
	resp := newResponse(request)
	go func() {
		defer resp.close()
		var (
			p       = c.chatCompletionNewParams(request)
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

func (c *client) CompletionNonStreaming(ctx context.Context, request providers.Request) (string, error) {
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

func (c *client) StructuredPredict(ctx context.Context, request providers.Request, model any) error {
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

	messages := request.Messages()
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
			p.Messages = append(p.Messages, assistantMessageParam(msg))

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
	tp := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
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

func assistantMessageParam(msg types.Message) openai.ChatCompletionMessageParamUnion {
	if len(msg.ToolCalls) == 0 && msg.Reasoning == "" {
		return openai.AssistantMessage(msg.Content)
	}

	tmsg := &openai.ChatCompletionAssistantMessageParam{}
	if msg.Content != "" || (len(msg.ToolCalls) == 0 && msg.Reasoning != "") {
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
	if msg.Reasoning != "" {
		tmsg.SetExtraFields(map[string]any{"reasoning_content": msg.Reasoning})
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
