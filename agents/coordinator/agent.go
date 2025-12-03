package coordinator

import (
	"context"
	"fmt"
	"github.com/basenana/friday/types"
	"strings"

	"go.uber.org/zap"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/agents/simple"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/utils/logger"
)

type Agent struct {
	react     *react.Agent
	simple    *simple.Agent
	llm       openai.Client
	subAgents []ExpertAgent
	option    Option
	logger    *zap.SugaredLogger
}

func (a *Agent) Name() string {
	return a.react.Name()
}

func (a *Agent) Describe() string {
	return a.react.Describe()
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	resp := agtapi.NewResponse()

	if req.Memory == nil {
		req.Memory = memory.NewEmptyWithSummarize(req.SessionID, a.llm)
	}

	ctx = memory.WithMemory(ctx, req.Memory)
	ctx = withRequest(ctx, &request{req, resp})
	go func() {
		defer resp.Close()
		if err := a.runSocialContact(ctx, req, resp); err != nil {
			a.logger.Warnw("run social contact failed", "error", err)
			return
		}
		if err := a.runReport(ctx, req, resp); err != nil {
			a.logger.Warnw("run reporting failed", "error", err)
			return
		}
	}()

	return resp
}

func (a *Agent) runSocialContact(ctx context.Context, req *agtapi.Request, resp *agtapi.Response) error {
	nextReq := &agtapi.Request{
		UserMessage:       req.UserMessage,
		SessionID:         req.SessionID,
		Memory:            req.Memory.Copy(),
		ExtraKV:           req.ExtraKV,
		OverwriteToolArgs: req.OverwriteToolArgs,
	}
	stream := a.react.Chat(ctx, nextReq)
	if err := mergeResponse(ctx, nextReq, stream, resp); err != nil {
		a.logger.Warnw("merge contact response error", "error", err)
		return err
	}
	return nil
}

func (a *Agent) runReport(ctx context.Context, req *agtapi.Request, resp *agtapi.Response) error {
	nextReq := &agtapi.Request{
		UserMessage:       a.option.SummaryReportPrompt,
		SessionID:         req.SessionID,
		Memory:            req.Memory.Copy(),
		ExtraKV:           req.ExtraKV,
		OverwriteToolArgs: req.OverwriteToolArgs,
	}
	a.logger.Infow("start summary report", "sessionID", req.SessionID)
	stream := a.simple.Chat(ctx, nextReq)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-stream.Error():
			if err != nil {
				resp.Fail(err)
				return err
			}
		case evt, ok := <-stream.Events():
			if !ok {
				return nil
			}
			if evt.Delta == nil {
				continue
			}
			if evt.Delta.Content != "" {
				agtapi.SendEvent(nextReq, resp, types.NewAnsEvent(evt.Delta.Content))
			}
		}
	}
}

func (a *Agent) handleMailWithSubAgentsTool(ctx context.Context, request *tools.Request) (*tools.Result, error) {
	toAgentNamesList, ok := request.Arguments["to_agent_names"].(string)
	if !ok {
		return nil, fmt.Errorf("missing required parameter: to_agent_names")
	}
	title, ok := request.Arguments["title"].(string)
	if !ok {
		return nil, fmt.Errorf("missing required parameter: title")
	}
	text, ok := request.Arguments["text"].(string)
	if !ok {
		return nil, fmt.Errorf("missing required parameter: text")
	}

	req := requestFromContext(ctx)
	if req == nil {
		a.logger.Warnw("no request from context")
		return nil, fmt.Errorf("missing agent request")
	}

	toAgentNames := strings.Split(toAgentNamesList, ",")
	waiter := make(chan string, len(toAgentNames))
	defer close(waiter)
	for _, toAgentName := range toAgentNames {
		if toAgentName == "" {
			waiter <- ""
			continue
		}
		go func(aname string) {
			reply := a.mailWithSubAgent(ctx, req.req, req.resp, aname, title, text)
			req.recordMail(aname, title, text, reply)
			waiter <- reply
		}(toAgentName)
	}

	var replies []string
	for i := 0; i < len(toAgentNames); i++ {
		reply := <-waiter
		if reply != "" {
			replies = append(replies, reply)
		}
	}

	a.logger.Infow(fmt.Sprintf("reply mail %s from agents %s", title, toAgentNamesList))
	return tools.NewToolResultText(strings.Join(replies, "\n\n")), nil
}

func (a *Agent) mailWithSubAgent(ctx context.Context, req *agtapi.Request, outsideResp *agtapi.Response, agentName, title, text string) string {
	var (
		agt             ExpertAgent
		answer, content string
	)

	for i, ea := range a.subAgents {
		if fuzzyMatching(ea.Name(), agentName) {
			agt = a.subAgents[i]
			break
		}
	}
	if agt == nil {
		a.logger.Warnw("no agent named " + agentName)
		return fmt.Sprintf("Agent %s not found", agentName)
	}

	ekv := make([]string, 0, len(req.ExtraKV)+2)
	for _, kv := range req.ExtraKV {
		ekv = append(ekv, kv)
	}
	ekv = append(ekv, "subagent")
	ekv = append(ekv, agentName)

	stream := agt.Chat(ctx, &agtapi.Request{
		UserMessage: strings.Join(
			[]string{
				NEW_TASK_PROMPT,
				"Title: " + title,
				text,
			}, "\n"),
		SessionID:         req.SessionID,
		Memory:            req.Memory,
		ExtraKV:           ekv,
		OverwriteToolArgs: req.OverwriteToolArgs,
	})

WaitReply:
	for {
		select {
		case <-ctx.Done():
			break WaitReply
		case evt, ok := <-stream.Events():
			if !ok {
				break WaitReply
			}
			if evt.Answer != nil {
				answer += evt.Answer.Report
				continue // ignore sub answer
			}
			if evt.Delta != nil && evt.Delta.Content != "" {
				content = evt.Delta.Content
			}
			agtapi.SendEvent(req, outsideResp, &evt)
		case err := <-stream.Error():
			if err != nil {
				return fmt.Sprintf("Agent: %s: failed to read content: %s", agentName, err)
			}
		}
	}

	if answer == "" {
		a.logger.Warnw("no answer reply, using content")
		answer = content
	}

	return strings.Join([]string{
		fmt.Sprintf("From: %s", agt.Name()),
		fmt.Sprintf("Title: Re:%s", title),
		"",
		answer,
	}, "\n")
}

func prebuildMailTool(agt *Agent) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("send_text_mail",
			tools.WithDescription("Send text emails to other agents so that they can request assistance."),
			tools.WithString("to_agent_names",
				tools.Required(),
				tools.Description("The recipient's Agent name, if multiple, separated by English letters."),
			),
			tools.WithString("title",
				tools.Required(),
				tools.Description("A brief summary of the request issues"),
			),
			tools.WithString("text",
				tools.Required(),
				tools.Description("Please provide a concise description of the problem you are requesting a solution to, along with suggestions for troubleshooting and the expected response."),
			),
			tools.WithToolHandler(agt.handleMailWithSubAgentsTool),
		),
	}
}

func mergeResponse(ctx context.Context, req *agtapi.Request, from *agtapi.Response, to *agtapi.Response) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-from.Events():
			if !ok {
				return nil
			}
			agtapi.SendEvent(req, to, &evt)
		case err := <-from.Error():
			if err != nil {
				to.Fail(err)
				return err
			}
		}
	}
}

func New(name, desc string, llm openai.Client, opt Option) *Agent {
	if opt.CoordinatePrompt == "" {
		opt.CoordinatePrompt = COORDINATE_PROMPT
	}
	if opt.SummaryReportPrompt == "" {
		opt.SummaryReportPrompt = DEFAULT_SUMMARYRE_PORTPROMPT
	}

	agt := &Agent{
		llm:       llm,
		subAgents: opt.SubAgents,
		option:    opt,
		logger:    logger.New("coordinator"),
	}

	var agentTools []*tools.Tool
	agentTools = append(agentTools, prebuildMailTool(agt)...)
	agentTools = append(agentTools, opt.Tools...)
	agt.react = react.New(name, desc, llm, react.Option{
		SystemPrompt: initSystemPrompt(opt),
		MaxLoopTimes: 5,
		Tools:        agentTools,
	})
	agt.simple = simple.New(name, desc, llm, simple.Option{
		SystemPrompt: opt.SystemPrompt,
	})
	return agt
}

type Option struct {
	SystemPrompt        string
	CoordinatePrompt    string
	SummaryReportPrompt string
	Tools               []*tools.Tool
	SubAgents           []ExpertAgent
}

type ExpertAgent interface {
	Name() string
	Describe() string
	Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response
}
