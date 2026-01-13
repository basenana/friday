package coordinator

import (
	"context"
	"fmt"
	"strings"

	agtapi2 "github.com/basenana/friday/core/agents/agtapi"
	"github.com/basenana/friday/core/agents/react"
	"github.com/basenana/friday/core/memory"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/tools"
	types2 "github.com/basenana/friday/core/types"
	"go.uber.org/zap"

	"github.com/basenana/friday/utils/logger"
)

type Agent struct {
	react     *react.Agent
	summary   *react.Agent
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

func (a *Agent) Chat(ctx context.Context, req *agtapi2.Request) *agtapi2.Response {
	var (
		resp = agtapi2.NewResponse()
	)

	if req.Session == nil {
		req.Session = types2.NewDummySession()
	}

	if req.Memory == nil {
		req.Memory = memory.NewEmpty(req.Session.ID)
	}

	ctx = agtapi2.NewContext(ctx, req.Session,
		agtapi2.WithMemory(req.Memory),
		agtapi2.WithResponse(resp),
	)

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

func (a *Agent) runSocialContact(ctx context.Context, req *agtapi2.Request, resp *agtapi2.Response) error {
	nextReq := &agtapi2.Request{
		UserMessage: req.UserMessage,
		Memory:      req.Memory,
	}
	stream := a.react.Chat(ctx, nextReq)
	if err := agtapi2.CopyResponse(ctx, stream, resp); err != nil {
		a.logger.Warnw("merge contact response error", "error", err)
		return err
	}
	return nil
}

func (a *Agent) runReport(ctx context.Context, req *agtapi2.Request, resp *agtapi2.Response) error {
	nextReq := &agtapi2.Request{
		Session:     req.Session,
		UserMessage: a.option.SummaryReportPrompt,
		Memory:      req.Memory,
	}
	a.logger.Infow("start summary report", "sessionID", req.Session.ID)
	stream := a.summary.Chat(ctx, nextReq)
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
				agtapi2.SendEvent(resp, types2.NewAnsEvent(evt.Delta.Content))
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

	var (
		toAgentNames = strings.Split(toAgentNamesList, ",")
		waiter       = make(chan string, len(toAgentNames))
	)

	defer close(waiter)
	for _, toAgentName := range toAgentNames {
		if toAgentName == "" {
			waiter <- ""
			continue
		}
		go func(aname string) {
			reply := a.mailWithSubAgent(ctx, aname, title, text)
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

func (a *Agent) mailWithSubAgent(ctx context.Context, agentName, title, text string) string {
	var (
		agt             ExpertAgent
		answer, content string
		mem             = agtapi2.MemoryFromContext(ctx).Copy() // fork memory for subagents
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

	stream := agt.Chat(ctx, &agtapi2.Request{
		Session: mem.Session(),
		UserMessage: strings.Join(
			[]string{
				"Title: " + title,
				text,
			}, "\n"),
		Memory: mem,
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
			agtapi2.SendEventToResponse(ctx, &evt, "subagent", agentName)
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

func New(name, desc string, llm openai.Client, opt Option) *Agent {
	if opt.CoordinatePrompt == "" {
		opt.CoordinatePrompt = COORDINATE_PROMPT
	}
	if opt.SummaryReportPrompt == "" {
		opt.SummaryReportPrompt = DEFAULT_SUMMARY_PROMPT
	}

	agt := &Agent{
		subAgents: opt.SubAgents,
		option:    opt,
		logger:    logger.New("coordinator").With(zap.String("name", name)),
	}

	var agentTools []*tools.Tool
	agentTools = append(agentTools, prebuildMailTool(agt)...)
	agentTools = append(agentTools, opt.Tools...)
	agt.react = react.New(name, desc, llm, react.Option{
		SystemPrompt: initSystemPrompt(opt),
		MaxLoopTimes: 5,
		Tools:        agentTools,
	})
	agt.summary = react.New(name, desc, llm, react.Option{
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
	Chat(ctx context.Context, req *agtapi2.Request) *agtapi2.Response
}
