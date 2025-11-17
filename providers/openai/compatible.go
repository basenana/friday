package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"time"

	"github.com/basenana/friday/utils/logger"
	"github.com/openai/openai-go"
	"go.uber.org/zap"
)

type CompatibleClient struct {
	*client
	logger *zap.SugaredLogger
}

func (c *CompatibleClient) Completion(ctx context.Context, request Request) Response {
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

func (c *CompatibleClient) chatCompletionNewParams(request Request) *openai.ChatCompletionNewParams {
	p := c.client.chatCompletionNewParams(request)

	// rewrite system prompt
	toolList := request.ToolDefines()
	if len(toolList) > 0 {
		p.Tools = nil

		buf := &bytes.Buffer{}
		messages := request.History()
		if len(messages) == 0 || messages[0].SystemMessage == "" {
			c.logger.Warnw("no system prompt found")
			return p
		}

		system := messages[0]
		buf.WriteString(system.SystemMessage)
		buf.WriteString("\n")

		buf.WriteString(DEFAULT_TOOL_USE_PROMPT)
		toolDefine := &ToolDefinePrompt{}
		for _, tool := range toolList {
			argContent, _ := json.Marshal(tool.Parameters)
			toolDefine.Tools = append(toolDefine.Tools, ToolPrompt{
				Name:        tool.Name,
				Description: tool.Description,
				Arguments:   string(argContent),
			})
		}
		defineContent, _ := xml.Marshal(toolDefine)
		buf.Write(defineContent)
		buf.WriteString("\n")

		p.Messages[0] = openai.SystemMessage(buf.String())
	}

	return p
}

func NewCompatible(host, apiKey string, model Model) Client {
	return &CompatibleClient{
		client: newClient(host, apiKey, model),
		logger: logger.New("openai.compatible"),
	}
}
