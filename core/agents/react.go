package agents

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

type react struct {
	llm    providers.Client
	tools  []*tools.Tool
	option Option
	logger logger.Logger
}

func (a *react) Chat(ctx context.Context, req *api.Request) *api.Response {
	resp := api.NewResponse()

	sess := req.Session
	if sess == nil {
		sess = session.New(types.NewID(), a.llm)
	}

	err := sess.RunHooks(ctx, types.SessionHookBeforeAgent, session.HookPayload{AgentRequest: req})
	if err != nil {
		resp.Fail(err)
		return resp
	}

	sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: req.UserMessage, Image: req.Image})
	sess.PublishEvent(types.Event{
		Type: types.EventAgentStart,
		Data: map[string]string{"message": logger.FirstLine(req.UserMessage)},
	})
	a.logger.Infow("handle request", "message", logger.FirstLine(req.UserMessage), "session", sess.ID)
	go a.reactLoop(ctx, sess, resp, req.Tools)
	return resp
}

func (a *react) reactLoop(ctx context.Context, sess *session.Session, resp *api.Response, reqTools []*tools.Tool) {
	defer resp.Close()

	ctx, span := tracing.Start(ctx, "agent.react.chat",
		tracing.WithAttributes(
			tracing.String("session.id", sess.ID),
			tracing.String("session.root_id", sess.Root.ID),
		),
	)
	defer span.End()

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
		startAt   = time.Now()
		loopTimes = 0
		keepRun   bool
		err       error
	)

	defer func() {
		elapsed := time.Since(startAt).String()
		span.SetAttributes(
			tracing.IntVal("loop_times", loopTimes),
			tracing.String("elapsed", elapsed),
		)
		a.logger.Infow("react loop finish",
			"loopTimes", loopTimes, "maxLoopTimes", a.option.MaxLoopTimes, "session", sess.ID, "elapsed", elapsed)
		sess.PublishEvent(types.Event{
			Type: types.EventAgentFinish,
			Data: map[string]string{"loop_times": strconv.Itoa(loopTimes), "elapsed": elapsed},
		})
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			keepRun, err = a.doAct(ctx, sess, resp, mergedTools, a.option.MaxLoopTimes-loopTimes)
		}
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "exceed max message tokens") {
				compactErr := sess.CompactHistory(ctx)
				if compactErr == nil {
					continue
				}
				a.logger.Warnw("failed to compact history", "error", compactErr.Error())
			}
			resp.Fail(err)
			return
		}

		if !keepRun {
			return
		}

		loopTimes++
		if loopTimes > a.option.MaxLoopTimes {
			a.logger.Warnw("too many loop times exceeded", "session", sess.ID)
			return
		}
	}
}

func (a *react) doAct(ctx context.Context, sess *session.Session, resp *api.Response, toolList []*tools.Tool, budget int) (bool, error) {
	ctx, span := tracing.Start(ctx, "agent.react.act",
		tracing.WithAttributes(
			tracing.String("session.id", sess.ID),
			tracing.String("session.root_id", sess.Root.ID),
			tracing.IntVal("budget", budget),
		),
	)
	defer span.End()

	sess.PublishEvent(types.Event{
		Type: types.EventLoopStart,
		Data: map[string]string{"budget": strconv.Itoa(budget)},
	})

	var (
		content      string
		reasoning    string
		agentMessage string
		messageCount int
		toolUse      []providers.ToolCall
		err          error

		keepRun    = false
		llmReq     = newLLMRequest(a.option.SystemPrompt, sess, toolList)
		warnTicker = time.NewTicker(time.Minute)
	)

	defer warnTicker.Stop()

	// before_model hooks
	err = sess.RunHooks(ctx, types.SessionHookBeforeModel, session.HookPayload{ModelRequest: llmReq})
	if err != nil {
		return false, err
	}

	if budget == 0 {
		llmReq.AppendHistory(types.Message{Role: types.RoleAgent, Content: "Your execution budget is exhausted. " +
			"This is your final response. Please provide a comprehensive summary including: " +
			"1) Task objective, 2) Progress made, 3) Key findings, 4) Remaining issues. " +
			"After this response, the session will end. From now on, every character you output will become part of the final report:"})
		}
	sess.PublishEvent(types.Event{Type: types.EventModelStart})
	stream := a.llm.Completion(ctx, llmReq)

WaitMessage:
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case err = <-stream.Error():
			if err != nil {
				return false, err
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
		"cachedPromptTokens", stream.Tokens().CachedPromptTokens,
		"completionTokens", stream.Tokens().CompletionTokens, "budget", budget, "session", sess.ID)
	span.SetAttributes(
		tracing.Int("prompt_tokens", stream.Tokens().PromptTokens),
		tracing.Int("completion_tokens", stream.Tokens().CompletionTokens),
		tracing.IntVal("tool_calls", len(toolUse)),
	)

	// Record token checkpoint when LLM returns actual usage data.
	sess.PublishEvent(types.Event{
		Type: types.EventModelFinish,
		Data: map[string]string{
			"prompt_tokens":     strconv.FormatInt(stream.Tokens().PromptTokens, 10),
			"completion_tokens": strconv.FormatInt(stream.Tokens().CompletionTokens, 10),
			"tool_calls":        strconv.Itoa(len(toolUse)),
		},
	})
	// Providers that don't return PromptTokens will fall back to
	// fuzzy estimation via EstimateHistoryTokens in Session.Tokens().
	if stream.Tokens().PromptTokens > 0 {
		ctxState := sess.EnsureContextState()
		ctxState.TokenCheckpoint = session.TokenCheckpoint{
			Index:        sess.HistoryLen(),
			PromptTokens: stream.Tokens().PromptTokens,
		}
	}

	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)

	if strings.Contains(content, "<tool_use") {
		a.logger.Warnw("tool use incorrect", "content", content, "session", sess.ID)
		agentMessage += "The tool is used in an incorrect format; please try using the tool again.\n"
	}

	// after_model hooks
	appl := &providers.Apply{ToolUse: toolUse}
	err = sess.RunHooks(ctx, types.SessionHookAfterModel, session.HookPayload{ModelRequest: llmReq, ModelApply: appl})
	if err != nil {
		return false, err
	}
	toolUse = canonicalizeToolCalls(appl.ToolUse)

	if reasoning != "" || len(content) > 0 || len(toolUse) > 0 {
		toolCalls := make([]types.ToolCall, 0, len(toolUse))
		for _, use := range toolUse {
			toolCalls = append(toolCalls, types.ToolCall{
				ID:        use.ID,
				Name:      use.Name,
				Arguments: use.Arguments,
			})
		}
		msg := &types.Message{
			Role:      types.RoleAssistant,
			Content:   content,
			Reasoning: reasoning,
			ToolCalls: toolCalls,
			Tokens:    stream.Tokens().CompletionTokens,
		}
		sess.AppendMessage(msg)
	}
	if agentMessage != "" {
		keepRun = true
		sess.AppendMessage(&types.Message{Role: types.RoleAgent, Content: agentMessage})
	}

	if len(toolUse) > 0 {
		keepRun = true
		a.doToolCalls(ctx, sess, toolUse, reasoning, toolList)
	}
	if appl.Continue {
		keepRun = true
	}
	if appl.Abort {
		keepRun = false
	}
	return keepRun, nil
}

func (a *react) doToolCalls(ctx context.Context, sess *session.Session, toolUses []providers.ToolCall, reasoning string, toolList []*tools.Tool) {
	ctx, span := tracing.Start(ctx, "tools.batch",
		tracing.WithAttributes(
			tracing.String("session.id", sess.ID),
			tracing.String("session.root_id", sess.Root.ID),
			tracing.IntVal("tool_count", len(toolUses)),
		),
	)
	defer span.End()

	var (
		result []*types.Message
		update = make(chan toolExecutionResult, len(toolUses))
		wg     = &sync.WaitGroup{}
	)

	for i := range toolUses {
		use := toolUses[i]
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			update <- toolExecutionResult{
				Index:    idx,
				Call:     use,
				Messages: a.tryToolCall(ctx, sess, use, reasoning, toolList),
			}
		}()
	}
	wg.Wait()
	close(update)

	outcomes := make([]toolExecutionResult, len(toolUses))
	for outcome := range update {
		outcomes[outcome.Index] = outcome
	}

	var executions []session.ToolExecution
	for _, outcome := range outcomes {
		if len(outcome.Messages) == 0 {
			continue
		}

		for _, msg := range outcome.Messages {
			if msg != nil && msg.Role == types.RoleTool {
				result = append(result, msg)
			}
		}

		executions = append(executions, session.ToolExecution{
			Call:     outcome.Call,
			Messages: cloneMessages(outcome.Messages),
		})
	}

	sess.AppendMessage(result...)
	if err := sess.RunHooks(ctx, types.SessionHookAfterTool, session.HookPayload{Executions: executions}); err != nil {
		a.logger.Errorw("failed to run after tool hooks", "error", err)
	}
}

func (a *react) tryToolCall(ctx context.Context, sess *session.Session, use providers.ToolCall, reasoning string, toolList []*tools.Tool) []*types.Message {
	ctx, span := tracing.Start(ctx, "tools.invoke",
		tracing.WithAttributes(
			tracing.String("tool.name", use.Name),
			tracing.String("tool.id", use.ID),
			tracing.String("session.id", sess.ID),
			tracing.String("session.root_id", sess.Root.ID),
		),
	)
	defer span.End()

	// Audit subscribers require the raw tool input/output for traceability.
	// External event consumers are responsible for masking, storage, and transport safety.
	sess.PublishEvent(types.Event{
		Type: types.EventToolStart,
		Data: map[string]string{
			"id":    use.ID,
			"tool":  use.Name,
			"input": use.Arguments,
		},
	})

	var (
		result  []*types.Message
		useMark = use.ID
	)

	if useMark == "" {
		useMark = use.Name
	}

	// Tool call message (assistant role with tool calls)
	result = append(result, &types.Message{
		Role:      types.RoleAssistant,
		Reasoning: reasoning,
		ToolCalls: []types.ToolCall{{ID: useMark, Name: use.Name, Arguments: use.Arguments}},
	})

	td := getToolByName(toolList, use.Name)
	if td == nil {
		msg := fmt.Sprintf("tool %s not found", use.Name)
		span.SetStatus(tracing.StatusError, msg)
		result = append(result, &types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: useMark, Content: msg}})
		a.logger.Warnw(msg, "tool", use.Name, "session", sess.ID)
		// Intentionally forward the full tool result for audit use cases.
		// External subscribers must enforce their own security controls.
		sess.PublishEvent(types.Event{
			Type: types.EventToolFinish,
			Data: map[string]string{"id": use.ID, "tool": use.Name, "success": "false", "output": msg},
		})
		return result
	}

	if use.Error != "" {
		span.SetStatus(tracing.StatusError, use.Error)
		result = append(result, &types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: useMark, Content: use.Error}})
		a.logger.Warnw("try tool call error", "tool", use.Name, "error", use.Error, "session", sess.ID)
		// Intentionally forward the full tool result for audit use cases.
		// External subscribers must enforce their own security controls.
		sess.PublishEvent(types.Event{
			Type: types.EventToolFinish,
			Data: map[string]string{"id": use.ID, "tool": use.Name, "success": "false", "output": use.Error},
		})
		return result
	}

	toolUse := &ToolUse{GenID: use.ID, Name: use.Name, Arguments: use.Arguments}
	a.logger.Infow("using tool", "tool", toolUse.Name, "args", toolUse.Arguments, "session", sess.ID)
	msg, isSucceed, err := toolCall(ctx, sess, toolUse, td)
	if err != nil {
		span.RecordError(err)
		errMsg := fmt.Sprintf("using tool failed: %s", err)
		result = append(result, &types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: toolUse.ID(), Content: errMsg}})
		a.logger.Warnw("using tool failed", "tool", use.Name, "error", err, "session", sess.ID)
		// Intentionally forward the full tool result for audit use cases.
		// External subscribers must enforce their own security controls.
		sess.PublishEvent(types.Event{
			Type: types.EventToolFinish,
			Data: map[string]string{"id": use.ID, "tool": use.Name, "success": "false", "output": errMsg},
		})
		return result
	}
	span.SetStatus(tracing.StatusOK, "")

	result = append(result, &types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: toolUse.ID(), Content: msg, Success: isSucceed}})
	// Intentionally forward the full tool result for audit use cases.
	// External subscribers must enforce their own security controls.
	sess.PublishEvent(types.Event{
		Type: types.EventToolFinish,
		Data: map[string]string{"id": use.ID, "tool": use.Name, "success": strconv.FormatBool(isSucceed), "output": msg},
	})
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

func canonicalizeToolCalls(toolUses []providers.ToolCall) []providers.ToolCall {
	result := make([]providers.ToolCall, 0, len(toolUses))
	usedIDs := make(map[string]int, len(toolUses))
	for _, use := range toolUses {
		baseID := use.ID
		if baseID == "" {
			baseID = (&ToolUse{Name: use.Name, Arguments: use.Arguments}).ID()
		}
		usedIDs[baseID]++
		if usedIDs[baseID] == 1 {
			use.ID = baseID
		} else {
			use.ID = fmt.Sprintf("%s-%d", baseID, usedIDs[baseID])
		}
		result = append(result, use)
	}
	return result
}

func cloneMessages(msgs []*types.Message) []types.Message {
	result := make([]types.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		result = append(result, *msg)
	}
	return result
}

type toolExecutionResult struct {
	Index    int
	Call     providers.ToolCall
	Messages []*types.Message
}

func New(llm providers.Client, option Option) Agent {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_SYSTEM_PROMPT
	}
	if option.MaxLoopTimes == 0 {
		option.MaxLoopTimes = 100
	}

	agt := &react{
		llm:    llm,
		tools:  option.Tools,
		option: option,
		logger: logger.New("react"),
	}

	return agt
}

type Option struct {
	SystemPrompt string
	MaxLoopTimes int

	Tools []*tools.Tool
}
