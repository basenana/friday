package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"

	"github.com/basenana/friday/core/logger"
)

// Deprecated: compatibleClient is not used. Do not use for new integrations.
type compatibleClient struct {
	*client
	logger logger.Logger
}

func (c *compatibleClient) Completion(ctx context.Context, request providers.Request) providers.Response {
	resp := newCompatibleResponse()
	go func() {
		defer resp.close()
		var (
			p       = c.chatCompletionNewParams(request)
			startAt = time.Now()
			err     error
		)

		defer func() {
			c.logger.Infow("[LLM-CALL] completion-with-streaming finish", "elapsed", time.Since(startAt).String())
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

			//c.logger.Infow("new choices found", "chunk", chunk)
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
	}()
	return resp
}

func (c *compatibleClient) chatCompletionNewParams(request providers.Request) *openai.ChatCompletionNewParams {
	p := &openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{},
		Model:    c.model.Name,
		TopP:     param.NewOpt(1.0),
		N:        param.NewOpt(int64(1)),
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
			p.Messages = append(p.Messages, assistantMessageParam(msg, false))

		case types.RoleTool:
			if msg.ToolResult != nil {
				tur := &ToolUseResult{Name: msg.ToolResult.CallID, Result: msg.ToolResult.Content}
				content, err := xml.Marshal(tur)
				if err == nil {
					p.Messages = append(p.Messages,
						openai.ToolMessage(string(content), msg.ToolResult.CallID),
					)
				} else {
					p.Messages = append(p.Messages,
						openai.ToolMessage(msg.ToolResult.Content, msg.ToolResult.CallID),
					)
				}
			}
		}
	}

	// rewrite system prompt
	toolList := sortedToolDefines(request.ToolDefines())
	if len(toolList) > 0 {
		p.Tools = nil

		buf := &bytes.Buffer{}
		messages = normalizeOpenAIToolMessages(request.Messages())
		if len(messages) == 0 || messages[0].Role != types.RoleSystem {
			c.logger.Warnw("no system prompt found")
			return p
		}

		system := messages[0]
		buf.WriteString(system.Content)
		buf.WriteString("\n")

		buf.WriteString(DEFAULT_TOOL_USE_PROMPT)
		buf.WriteString("<available_tools>\n")
		buf.WriteString("Above example were using notional tools that might not exist for you. You only have access to these tools:\n")
		toolDefine := &ToolDefinePrompt{}
		for _, tool := range toolList {
			argContent, _ := json.Marshal(tool.GetParameters())
			toolDefine.Tools = append(toolDefine.Tools, ToolPrompt{
				Name:        tool.GetName(),
				Description: tool.GetDescription(),
				Arguments:   string(argContent),
			})
		}
		defineContent, _ := xml.Marshal(toolDefine)
		buf.Write(defineContent)
		buf.WriteString("\n")
		buf.WriteString("</available_tools>\n")

		p.Messages[0] = openai.SystemMessage(buf.String())
	}

	return p
}

// Deprecated: NewCompatible is not used. Do not use for new integrations.
func NewCompatible(host, apiKey string, model Model) providers.Client {
	return &compatibleClient{
		client: newClient(host, apiKey, model),
		logger: logger.New("openai.compatible"),
	}
}
