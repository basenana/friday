package react

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	name string
	desc string
	llm  openai.Client

	tools      []*tools.Tool
	toolCalled int

	option Option
	logger logger.Logger
}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Describe() string {
	return a.desc
}

func (a *Agent) Chat(ctx context.Context, req *api.Request) *api.Response {
	resp := api.NewResponse()

	sess := req.Session
	if sess == nil {
		sess = session.New(generateID(), a.llm)
	}

	ctx = api.NewContext(ctx, sess,
		api.WithResponse(resp),
	)

	sess.AppendMessage(&types.Message{UserMessage: req.UserMessage})

	a.logger.Infow("handle request", "message", logger.FirstLine(req.UserMessage), "session", sess.ID)
	go a.reactLoop(ctx, sess, resp, req.Tools)
	return resp
}

func (a *Agent) reactLoop(ctx context.Context, sess *session.Session, resp *api.Response, reqTools []*tools.Tool) {
	defer resp.Close()

	// Merge agent tools with request tools (request tools take precedence)
	mergedTools := make([]*tools.Tool, 0, len(a.tools)+len(reqTools))
	toolNames := make(map[string]bool)

	// Add request tools first (higher precedence)
	for _, t := range reqTools {
		mergedTools = append(mergedTools, t)
		toolNames[t.Name] = true
	}
	// Add agent tools that aren't in request tools
	for _, t := range a.tools {
		if !toolNames[t.Name] {
			mergedTools = append(mergedTools, t)
		}
	}

	var (
		startAt     = time.Now()
		usage       = &loopUsage{Limit: a.option.MaxLoopTimes, ToolLimit: a.option.MaxToolCalls}
		statusCode  code
		supplements []types.Message
		stream      openai.Response
		err         error
	)

	defer func() {
		a.logger.Infow("react loop finish",
			"toolUse", usage.ToolUse, "extraTry", usage.Try, "session", sess.ID, "elapsed", time.Since(startAt).String())
	}()

	for {
		if usage.ToolUse >= usage.ToolLimit {
			a.logger.Warnw("too many tool calls exceeded", "session", sess.ID)
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
			err = sess.RunHooks(ctx, types.SessionHookBeforeModel)
			if err != nil {
				resp.Fail(err)
				return
			}
			stream = a.llm.Completion(ctx, newLLMRequest(a.option.SystemPrompt, sess, mergedTools))

			err = sess.RunHooks(ctx, types.SessionHookAfterModel)
			if err != nil {
				resp.Fail(err)
				return
			}

			supplements, statusCode = a.handleLLMStream(ctx, stream, sess, resp, usage, mergedTools)
		}

		if statusCode == 1 {
			return
		}

		if len(supplements) > 0 {
			for _, supplement := range supplements {
				if supplement.ToolContent != "" {
					usage.ToolUse++
				}
			}
			for i := range supplements {
				sess.AppendMessage(&supplements[i])
			}
			continue
		}

		usage.Try++
		if usage.Try >= usage.Limit {
			a.logger.Warnw("too many loop times exceeded", "session", sess.ID)
			return
		}
		if usage.Try > 3 {
			a.logger.Warnw("the LLM did not terminate the loop as expected", "status", statusCode, "extraTry", usage.Try, "session", sess.ID)
		}
		switch statusCode {
		case 2:
			sess.AppendMessage(&types.Message{AgentMessage: `The tool is used in an incorrect format; please try using the tool again.`})
		default:
			sess.AppendMessage(&types.Message{AgentMessage: "If you believe the conversation is complete, use the tool to end the conversation."})
		}
	}
}

func (a *Agent) handleLLMStream(ctx context.Context, stream openai.Response, sess *session.Session, resp *api.Response, usage *loopUsage, toolList []*tools.Tool) ([]types.Message, code) {
	var (
		content      string
		reasoning    string
		messageCount int
		toolUse      []openai.ToolUse

		statusCode  = code(0)
		supplements []types.Message
		warnTicker  = time.NewTicker(time.Minute)
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
			a.logger.Warnw("still waiting llm completed", "receivedMessage", messageCount, "session", sess.ID)

		case msg, ok := <-stream.Message():
			if !ok {
				break WaitMessage
			}

			messageCount += 1
			switch {
			case len(msg.Content) > 0:
				content += msg.Content
				api.SendDelta(resp, types.Delta{Content: msg.Content})

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
				api.SendDelta(resp, types.Delta{Reasoning: msg.Reasoning})
			}
		}
	}

	a.logger.Infow("message finish",
		"fuzzyTokens", sess.Tokens(), "promptTokens", stream.Tokens().PromptTokens,
		"completionTokens", stream.Tokens().CompletionTokens, "session", sess.ID)

	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)

	if content == "" {
		return supplements, statusCode
	}

	if strings.Contains(content, "topic_finish_") {
		a.logger.Warnw("topic_finish tool use incorrect", "content", content, "session", sess.ID)
		statusCode.update(1)
	} else if strings.Contains(content, "<tool_use") {
		a.logger.Warnw("tool use incorrect", "content", content, "session", sess.ID)
		statusCode.update(2)
	}

	if reasoning != "" {
		sess.AppendMessage(&types.Message{AssistantReasoning: reasoning})
	}

	if len(toolUse) > 0 {
		toolCallMessages := a.doToolCalls(ctx, sess, toolUse, reasoning, usage, toolList)
		if len(toolCallMessages) > 0 {
			supplements = append(supplements, toolCallMessages...)
		}
	}

	if len(content) > 0 {
		sess.AppendMessage(&types.Message{AssistantMessage: content})
	}
	return supplements, statusCode
}

func (a *Agent) doToolCalls(ctx context.Context, sess *session.Session, toolUses []openai.ToolUse, reasoning string, usage *loopUsage, toolList []*tools.Tool) []types.Message {
	var (
		result       []types.Message
		update       = make(chan []types.Message, len(toolUses))
		toolUseCount = usage.ToolUse
		wg           = &sync.WaitGroup{}
	)

	for i := range toolUses {
		use := toolUses[i]
		wg.Add(1)
		go func(tc int) {
			defer wg.Done()
			update <- a.tryToolCall(ctx, sess, use, reasoning, tc, toolList)
		}(toolUseCount + i + 1)
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

func (a *Agent) tryToolCall(ctx context.Context, sess *session.Session, use openai.ToolUse, reasoning string, toolUseCount int, toolList []*tools.Tool) []types.Message {
	var (
		result    []types.Message
		extraArgs = api.OverwriteToolArgsFromContext(ctx)
		useMark   = use.ID
	)

	if useMark == "" {
		useMark = use.Name
	}

	result = append(result, types.Message{ToolCallID: useMark, ToolName: use.Name, ToolArguments: use.Arguments, AssistantReasoning: reasoning})

	td := getToolByName(toolList, use.Name)
	if td == nil {
		msg := fmt.Sprintf("tool %s not found", use.Name)
		result = append(result, types.Message{ToolCallID: useMark, ToolContent: msg})
		a.logger.Warnw(msg, "tool", use.Name, "session", sess.ID)
		return result
	}

	if use.Error != "" {
		result = append(result, types.Message{ToolCallID: useMark, ToolContent: use.Error})
		a.logger.Warnw("try tool call error", "tool", use.Name, "error", use.Error, "session", sess.ID)
		return result
	}

	toolUse := &ToolUse{GenID: use.ID, Name: use.Name, Arguments: use.Arguments}
	a.logger.Infow("using tool", "tool", toolUse.Name, "args", toolUse.Arguments, "session", sess.ID)
	msg, err := toolCall(ctx, sess, toolUse, extraArgs, td, toolUseCount)
	if err != nil {
		result = append(result, types.Message{ToolCallID: toolUse.ID(), ToolContent: fmt.Sprintf("using tool failed: %s", err)})
		a.logger.Warnw("using tool failed", "tool", use.Name, "error", err, "session", sess.ID)
		return result
	}

	result = append(result, types.Message{ToolCallID: toolUse.ID(), ToolContent: msg})
	return result
}

func getToolByName(toolList []*tools.Tool, name string) *tools.Tool {
	for _, tool := range toolList {
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
		logger: logger.New("react").With("name", name),
	}

	return agt
}

type Option struct {
	SystemPrompt string
	MaxLoopTimes int
	MaxToolCalls int

	Tools []*tools.Tool
}

type code int32

func (c *code) update(n int32) {
	if c == nil {
		return
	}
	if n > int32(*c) {
		*c = code(n)
	}
}

type loopUsage struct {
	Try       int
	Limit     int
	ToolUse   int
	ToolLimit int
}

func generateID() string {
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}
