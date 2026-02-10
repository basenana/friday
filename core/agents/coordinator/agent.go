package coordinator

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/agents/react"
	agtapi "github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	react     *react.Agent
	summary   *react.Agent
	llm       openai.Client
	subAgents []ExpertAgent
	option    Option
	logger    logger.Logger
}

func (a *Agent) Name() string {
	return a.react.Name()
}

func (a *Agent) Describe() string {
	return a.react.Describe()
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	resp := agtapi.NewResponse()

	sess := req.Session
	if sess == nil {
		sess = session.New(types.NewID(), a.llm)
	}

	ctx = agtapi.NewContext(ctx, sess,
		agtapi.WithResponse(resp),
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

func (a *Agent) runSocialContact(ctx context.Context, req *agtapi.Request, resp *agtapi.Response) error {
	nextReq := &agtapi.Request{
		Session:     req.Session,
		UserMessage: req.UserMessage,
	}
	stream := a.react.Chat(ctx, nextReq)
	if err := agtapi.CopyResponse(ctx, stream, resp); err != nil {
		a.logger.Warnw("merge contact response error", "error", err)
		return err
	}
	return nil
}

func (a *Agent) runReport(ctx context.Context, req *agtapi.Request, resp *agtapi.Response) error {
	nextReq := &agtapi.Request{
		Session:     req.Session,
		UserMessage: a.option.SummaryReportPrompt,
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
		case delta, ok := <-stream.Deltas():
			if !ok {
				return nil
			}
			if delta.Content != "" {
				agtapi.SendDelta(resp, delta)
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
		agt        ExpertAgent
		answer     string
		parentSess = agtapi.SessionFromContext(ctx)
	)
	if parentSess == nil {
		return "Error: no session found in context"
	}
	sess := parentSess.Fork() // fork session for subagents

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

	stream := agt.Chat(ctx, &agtapi.Request{
		Session: sess,
		UserMessage: strings.Join(
			[]string{
				"Title: " + title,
				text,
			}, "\n"),
	})

WaitReply:
	for {
		select {
		case <-ctx.Done():
			break WaitReply
		case delta, ok := <-stream.Deltas():
			if !ok {
				break WaitReply
			}
			if delta.Content != "" {
				answer += delta.Content
			}
		case err := <-stream.Error():
			if err != nil {
				return fmt.Sprintf("Agent: %s: failed to read content: %s", agentName, err)
			}
		}
	}

	if answer == "" {
		a.logger.Warnw("no answer reply")
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
		llm:       llm,
		subAgents: opt.SubAgents,
		option:    opt,
		logger:    logger.New("coordinator").With("name", name),
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
	Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response
}
