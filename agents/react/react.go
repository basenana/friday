package react

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Agent struct {
	name string
	desc string
	llm  openai.Client

	tools      []*tools.Tool
	toolCalled int

	option Option
	logger *zap.SugaredLogger
}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Describe() string {
	return a.desc
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	var (
		resp = agtapi.NewResponse()
	)
	if req.Memory == nil {
		req.Memory = memory.NewEmptyWithSummarize(req.SessionID, a.llm)
	}

	mem := req.Memory
	mem.AppendMessages(types.Message{UserMessage: req.UserMessage})

	ctx = memory.WithMemory(ctx, mem)
	a.logger.Infow("handle request", "message", req.UserMessage)
	go a.reactLoop(ctx, mem, req, resp)
	return resp
}

func (a *Agent) reactLoop(ctx context.Context, mem *memory.Memory, req *agtapi.Request, resp *agtapi.Response) {
	defer resp.Close()

	var (
		startAt     = time.Now()
		statusCode  code
		extraTry    = 0
		toolCalled  = 0
		supplements []types.Message
		stream      openai.Response
	)

	defer func() {
		a.logger.Infow("react loop finish",
			"toolUse", toolCalled, "extraTry", extraTry, "elapsed", time.Since(startAt).String())
	}()

	for {
		if toolCalled >= a.option.MaxToolCalls {
			a.logger.Warnw("too many tool calls exceeded")
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
			stream = a.llm.Completion(ctx, newLLMRequest(a.option.SystemPrompt, mem, a.tools))
			supplements, statusCode = a.handleLLMStream(ctx, stream, mem, req, resp)
		}

		if statusCode == 1 {
			return
		}

		if len(supplements) > 0 {
			for _, supplement := range supplements {
				if supplement.ToolContent != "" {
					toolCalled++
				}
			}
			mem.AppendMessages(supplements...)
			continue
		}

		/*
			extraTry implements a safeguard mechanism
			tracking retry attempts beyond expected levels in LLM processes
		*/
		extraTry++
		if extraTry >= a.option.MaxLoopTimes {
			a.logger.Warnw("too many loop times exceeded")
			return
		}
		if extraTry > 3 {
			a.logger.Warnw("the LLM did not terminate the loop as expected", "status", statusCode, "extraTry", extraTry)
		}
		switch statusCode {
		case 2:
			mem.AppendMessages(types.Message{UserMessage: `The tool is used in an incorrect format; please try using the tool again.`})
		default:
			mem.AppendMessages(types.Message{UserMessage: "If you believe the conversation is complete, use the tool to end the conversation."})
		}
	}
}

func (a *Agent) handleLLMStream(ctx context.Context, stream openai.Response, mem *memory.Memory, req *agtapi.Request, resp *agtapi.Response) ([]types.Message, code) {
	var (
		content      string
		reasoning    string
		messageCount int
		toolUse      []openai.ToolUse

		statusCode  = code(0) // not finish
		supplements []types.Message
		warnTicker  = time.NewTicker(time.Minute)

		extraArgs = req.OverwriteToolArgs
	)

	defer warnTicker.Stop()

WaitMessage:
	for {
		select {
		case <-ctx.Done():
			resp.Fail(ctx.Err())
			return supplements, 0
		case err := <-stream.Error():
			if err != nil {
				resp.Fail(err)
				return supplements, 0
			}
		case <-warnTicker.C:
			a.logger.Warnw("still waiting llm completed", "receivedMessage", messageCount)

		case msg, ok := <-stream.Message():
			if !ok {
				break WaitMessage
			}

			messageCount += 1 // check llm api is hang
			switch {
			case len(msg.Content) > 0:
				content += msg.Content
				agtapi.SendEvent(req, resp, types.NewContentEvent(msg.Content))

			case len(msg.ToolUse) > 0:
				for i := range msg.ToolUse {
					tool := msg.ToolUse[i]
					if strings.HasPrefix(tool.Name, "topic_finish_") {
						statusCode.update(1)
						continue WaitMessage
					}
					toolUse = append(toolUse, tool)
				}

			case len(msg.Reasoning) > 0:
				reasoning += msg.Reasoning
				agtapi.SendEvent(req, resp, types.NewReasoningEvent(msg.Reasoning))
			}
		}
	}

	a.logger.Infow("message finish",
		"fuzzyTokens", mem.Tokens(), "promptTokens", stream.Tokens().PromptTokens, "completionTokens", stream.Tokens().CompletionTokens)

	func() {
		/*
			WORKAROUND FOR STUPID LLM
		*/
		content = strings.TrimSpace(content)
		reasoning = strings.TrimSpace(reasoning)

		if content == "" {
			return
		}

		if strings.Contains(content, "topic_finish_") {
			a.logger.Warnw("topic_finish tool use incorrect", "content", content)
			statusCode.update(1)
		} else if strings.Contains(content, "<tool_use") {
			a.logger.Warnw("tool use incorrect", "content", content)
			statusCode.update(2)
		}
	}()

	if reasoning != "" {
		mem.AppendMessages(types.Message{AssistantReasoning: reasoning})
	}

	if len(toolUse) > 0 {
		toolCallMessages := a.doToolCalls(ctx, toolUse, extraArgs, reasoning, req, resp)
		if len(toolCallMessages) > 0 {
			supplements = append(supplements, toolCallMessages...)
		}
	}

	if len(content) > 0 {
		mem.AppendMessages(types.Message{AssistantMessage: content})
	}
	return supplements, statusCode
}

func (a *Agent) doToolCalls(ctx context.Context, toolUses []openai.ToolUse, extraArgs map[string]string, reasoning string, req *agtapi.Request, resp *agtapi.Response) []types.Message {
	var (
		result []types.Message
		update = make(chan []types.Message, len(toolUses))
		wg     = &sync.WaitGroup{}
	)

	for i := range toolUses {
		use := toolUses[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			// for long tool use such like an agent call
			update <- a.tryToolCall(ctx, use, extraArgs, reasoning, req, resp)
		}()
	}
	wg.Wait()
	close(update)

	for message := range update {
		if len(message) == 0 {
			continue
		}
		result = append(result, message...)
	}
	return result
}

func (a *Agent) tryToolCall(ctx context.Context, use openai.ToolUse, extraArgs map[string]string, reasoning string, req *agtapi.Request, resp *agtapi.Response) []types.Message {
	var (
		result  []types.Message
		buf     = &bytes.Buffer{}
		useMark = use.ID
	)

	if useMark == "" {
		useMark = use.Name
	}

	// request a tool call message
	// DeepSeek v3.2: if the model support using tool in reasoning, the reasoning need return
	result = append(result, types.Message{ToolCallID: useMark, ToolName: use.Name, ToolArguments: use.Arguments, AssistantReasoning: reasoning})

	td := a.getToolByName(use.Name)
	if td == nil {
		msg := fmt.Sprintf("tool %s not found", use.Name)
		result = append(result, types.Message{ToolCallID: useMark, ToolContent: msg})
		agtapi.SendEvent(req, resp, types.NewToolUseEvent(use.Name, use.Arguments, "", msg))
		return result
	}

	if use.Error != "" {
		result = append(result, types.Message{ToolCallID: useMark, ToolContent: use.Error})
		agtapi.SendEvent(req, resp, types.NewToolUseEvent(use.Name, use.Arguments, td.Description, use.Error))
		return result
	}

	toolUse := &ToolUse{GenID: use.ID, Name: use.Name, Arguments: use.Arguments}
	a.logger.Infow("using tool", "tool", toolUse.Name, "args", toolUse.Arguments)
	msg, err := toolCall(ctx, toolUse, extraArgs, td)
	if err != nil {
		result = append(result, types.Message{ToolCallID: toolUse.ID(), ToolContent: fmt.Sprintf("using tool failed: %s", err)})
		agtapi.SendEvent(req, resp, types.NewToolUseEvent(use.Name, use.Arguments, td.Description, err.Error()))
		return result
	}

	buf.Reset()
	buf.WriteString("The following are the tool execution results. " +
		"Please analyze further based on the tool's response, but do not directly return the original execution results to the user:")
	buf.WriteString(msg)

	result = append(result, types.Message{ToolCallID: toolUse.ID(), ToolContent: buf.String()})
	agtapi.SendEvent(req, resp, types.NewToolUseEvent(use.Name, use.Arguments, td.Description, ""))

	return result
}

func (a *Agent) getToolByName(name string) *tools.Tool {
	for _, tool := range a.tools {
		if tool.Name == name {
			return tool
		}
	}
	return nil
}

func New(name, desc string, llm openai.Client, option Option) *Agent {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_SYSTEM_PROMPT
	}
	if option.MaxLoopTimes == 0 {
		option.MaxLoopTimes = 5
	}
	if option.MaxToolCalls == 0 {
		option.MaxToolCalls = 20
	}

	agt := &Agent{
		name:   name,
		desc:   desc,
		llm:    llm,
		tools:  option.Tools,
		option: option,
		logger: logger.New("react").With(zap.String("name", name)),
	}

	for _, tool := range memory.ListStorageTools() {
		agt.tools = append(agt.tools, tool)
	}

	return agt
}

type Option struct {
	SystemPrompt string
	MaxLoopTimes int
	MaxToolCalls int

	Tools []*tools.Tool
}

/*
code : react loop status

	0 - not finish
	1 - finish
	2 - tool use issue
*/
type code int32

func (c *code) update(n int32) {
	if c == nil {
		return
	}
	if n > int32(*c) {
		*c = code(n)
	}
}
