package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
)

type Client struct {
	openai openai.Client
	model  Model
}

func (c *Client) Chat(ctx context.Context, userMessage string, session *Session) *Response {
	resp := newResponse()
	session.History = append(session.History, Message{UserMessage: userMessage})
	go c.replyLoop(ctx, session, resp)
	return resp
}

func (c *Client) replyLoop(ctx context.Context, session *Session, resp *Response) {
	defer resp.Close()
	p := c.chatCompletionNewParams(session)

	var err error
	for p != nil {
		stream := c.openai.Chat.Completions.NewStreaming(ctx, *p)
		if err = stream.Err(); err != nil {
			resp.Fail(err)
			return
		}

		p, err = c.handleStream(ctx, stream, resp, session)
		if err != nil {
			resp.Fail(err)
			return
		}
	}
}

func (c *Client) handleStream(ctx context.Context, stream *ssestream.Stream[openai.ChatCompletionChunk], resp *Response, session *Session) (*openai.ChatCompletionNewParams, error) {
	var (
		message          string
		toolCallMessages []Message
	)

	for stream.Next() {
		chunk := stream.Current()

		if len(chunk.Choices) == 0 {
			continue
		}

		ch := chunk.Choices[0]

		switch {
		case len(ch.Delta.Content) > 0:
			message += ch.Delta.Content
			resp.Stream(ch.Delta.Content)
		case len(ch.Delta.ToolCalls) > 0:
			for _, toolCall := range ch.Delta.ToolCalls {
				msg := c.toolCall(ctx, toolCall.ID, toolCall.Function.Name, toolCall.Function.Arguments, session)
				toolCallMessages = append(toolCallMessages, msg)
			}
		}
	}

	if message != "" {
		session.History = append(session.History, Message{
			AssistantMessage: message,
		})
	}

	if len(toolCallMessages) > 0 {
		session.History = append(session.History, toolCallMessages...)
		return c.chatCompletionNewParams(session), nil
	}

	return nil, stream.Err()
}

func (c *Client) toolCall(ctx context.Context, id string, name, argJson string, session *Session) Message {

	for _, t := range session.Tools {
		if t.Name != name {
			continue
		}

		arg := make(map[string]interface{})
		if err := json.Unmarshal([]byte(argJson), &arg); err != nil {
			return Message{ToolCallID: id, ToolContent: fmt.Sprintf("unmarshal json argument failed: %s", err)}
		}

		content, err := t.server.client.CallTool(ctx, name, arg)
		if err != nil {
			return Message{ToolCallID: id, ToolContent: fmt.Sprintf("call tool %s failed: %s", name, err)}
		}

		return Message{ToolCallID: id, ToolContent: content}
	}

	return Message{ToolCallID: id, ToolContent: fmt.Sprintf("tool %s not found", name)}
}

func (c *Client) chatCompletionNewParams(session *Session) *openai.ChatCompletionNewParams {
	p := openai.ChatCompletionNewParams{
		Messages:         []openai.ChatCompletionMessageParamUnion{},
		Model:            c.model.Name,
		Temperature:      param.NewOpt(c.model.Temperature),
		FrequencyPenalty: param.NewOpt(c.model.FrequencyPenalty),
		PresencePenalty:  param.NewOpt(c.model.PresencePenalty),
		TopP:             param.NewOpt(1.0),
		N:                param.NewOpt(int64(1)),
	}

	for _, msg := range session.History {

		switch {
		case msg.SystemMessage != "":
			p.Messages = append(p.Messages,
				openai.SystemMessage(msg.SystemMessage),
			)

		case msg.UserMessage != "":
			p.Messages = append(p.Messages,
				openai.UserMessage(msg.UserMessage),
			)

		case msg.AssistantMessage != "":
			p.Messages = append(p.Messages,
				openai.AssistantMessage(msg.AssistantMessage),
			)

		case msg.ToolCallID != "":
			p.Messages = append(p.Messages,
				openai.ToolMessage(msg.ToolContent, msg.ToolCallID),
			)

		}
	}

	if len(session.Tools) > 0 {
		for _, tool := range session.Tools {
			p.Tools = append(p.Tools, openai.ChatCompletionToolParam{
				Function: shared.FunctionDefinitionParam{
					Name:        tool.Name,
					Description: param.NewOpt(tool.Description),
					Parameters: map[string]interface{}{
						"type":       "object",
						"properties": tool.InputSchema.Properties,
					},
					//Strict:      param.NewOpt(true),
				},
				Type: "function",
			})
		}
	}
	return &p
}

func New(host, apiKey string, model Model) *Client {
	oc := openai.NewClient(
		option.WithBaseURL(host),
		option.WithAPIKey(apiKey),
	)
	return &Client{
		openai: oc,
		model:  model,
	}
}

type Model struct {
	Name             string
	Temperature      float64
	FrequencyPenalty float64
	PresencePenalty  float64
	Compatible       bool
}

type Response struct {
	stream chan string
	err    chan error
}

func (r *Response) Stream(msg string) {
	r.stream <- msg
}

func (r *Response) Message() <-chan string {
	return r.stream
}

func (r *Response) Fail(err error) {
	r.err <- err
}
func (r *Response) Error() <-chan error {
	return r.err
}

func (r *Response) Close() {
	close(r.stream)
	close(r.err)
}

func newResponse() *Response {
	return &Response{stream: make(chan string, 5), err: make(chan error, 1)}
}
