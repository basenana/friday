package openai

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"net/http"
	"strings"
	"time"
)

type CompatibleClient struct {
	openai openai.Client
	model  Model
}

func (c *CompatibleClient) Chart(ctx context.Context, userMessage string, session *Session) *Response {
	resp := newResponse()
	CompatibleSystem(session)
	session.History = append(session.History, Message{UserMessage: userMessage})
	go c.replyLoop(ctx, session, resp)
	return resp
}

func (c *CompatibleClient) replyLoop(ctx context.Context, session *Session, resp *Response) {
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

func (c *CompatibleClient) handleStream(ctx context.Context, stream *ssestream.Stream[openai.ChatCompletionChunk], resp *Response, session *Session) (*openai.ChatCompletionNewParams, error) {
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
		}
	}

	if message != "" {
		session.History = append(session.History, Message{
			AssistantMessage: message,
		})
	}

	if !strings.HasSuffix(message, "\n") {
		message += "\n"
		resp.Stream("\n")
	}

	if strings.Contains(message, "tool_use") {
		msg := c.tryToolCall(ctx, message, session)
		toolCallMessages = append(toolCallMessages, msg...)
	}

	if len(toolCallMessages) > 0 {
		session.History = append(session.History, toolCallMessages...)
		return c.chatCompletionNewParams(session), nil
	}

	return nil, stream.Err()
}

func (c *CompatibleClient) tryToolCall(ctx context.Context, content string, session *Session) []Message {
	var (
		messages = extractXMLStructures(content)
		result   []Message
	)
	for _, message := range messages {
		toolUse := &ToolUse{}
		err := xml.Unmarshal([]byte(message), toolUse)
		if err != nil {
			result = append(result, Message{ToolCallID: "unknown", ToolContent: fmt.Sprintf("parse tool use error: %s", err)})
		}
		msg := c.toolCall(ctx, toolUse.Name, toolUse.Name, toolUse.Arguments, session)
		result = append(result, msg)
	}

	return result
}

func (c *CompatibleClient) toolCall(ctx context.Context, id string, name, argJson string, session *Session) Message {
	for _, t := range session.Tools {
		if t.Name() != name {
			continue
		}

		arg := make(map[string]interface{})
		if err := json.Unmarshal([]byte(argJson), &arg); err != nil {
			return Message{ToolCallID: id, ToolContent: fmt.Sprintf("unmarshal json argument failed: %s", err)}
		}

		content, err := t.Call(ctx, name, arg)
		if err != nil {
			return Message{ToolCallID: id, ToolContent: fmt.Sprintf("call tool %s failed: %s", name, err)}
		}

		return Message{ToolCallID: id, ToolContent: content}
	}

	return Message{ToolCallID: id, ToolContent: fmt.Sprintf("tool %s not found", name)}
}

func (c *CompatibleClient) chatCompletionNewParams(session *Session) *openai.ChatCompletionNewParams {
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
				openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
					openai.TextContentPart(fmt.Sprintf("Here is the result of tool call %s:", msg.ToolCallID)),
					openai.TextContentPart(msg.ToolContent),
				}),
			)
		}
	}

	return &p
}

func NewCompatible(host, apiKey string, model Model) *CompatibleClient {
	oc := openai.NewClient(
		option.WithBaseURL(host),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Timeout: time.Hour,
		}),
	)
	return &CompatibleClient{
		openai: oc,
		model:  model,
	}
}

type Tool struct {
	Name        string `xml:"name"`
	Description string `xml:"description"`
	Arguments   string `xml:"arguments"`
}

type ToolUse struct {
	Name      string `xml:"name"`
	Arguments string `xml:"arguments"`
}

type ToolDefine struct {
	Tools []Tool `xml:"tools"`
}

func CompatibleSystem(session *Session) {
	if len(session.History) > 0 {
		return
	}
	buf := &bytes.Buffer{}
	buf.WriteString(systemPrompt)
	if session.Prompt != "" {
		buf.WriteString("# User Instructions")
		buf.WriteString(session.Prompt)
	}

	if len(session.Tools) > 0 {
		buf.WriteString(toolUsePrompt)
		// define rules
		define := ToolDefine{}
		for _, t := range session.Tools {
			argContent, _ := json.Marshal(t.APISchema())
			define.Tools = append(define.Tools, Tool{
				Name:        t.Name(),
				Description: t.Description(),
				Arguments:   string(argContent),
			})
		}

		defineContent, _ := xml.Marshal(define)
		buf.Write(defineContent)
		buf.WriteString(toolUseRulesPrompt)
	}

	buf.WriteString("Now Begin! If you solve the task correctly, you will receive a reward of $1,000,000.")
	session.History = append(session.History, Message{SystemMessage: buf.String()})
}
