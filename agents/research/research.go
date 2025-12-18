package research

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/basenana/friday/agents"
	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/planning"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/agents/simple"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Agent struct {
	name        string
	desc        string
	llm         openai.Client
	opt         Option
	leaderTools []*tools.Tool
	logger      *zap.SugaredLogger
}

var _ agents.Agent = &Agent{}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Describe() string {
	return a.desc
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	var (
		resp        = agtapi.NewResponse()
		planningAgt = planning.New("planning", "", a.llm, planning.Option{Tools: a.opt.PlanningTools})
	)

	if req.Session == nil {
		req.Session = types.NewDummySession()
	}

	if req.Memory == nil {
		req.Memory = memory.NewEmpty(req.Session.ID, memory.WithSummarize(a.llm))
	}

	ctx = agtapi.NewContext(ctx, req.Session,
		agtapi.WithMemory(req.Memory),
		agtapi.WithResponse(resp),
	)

	req.Memory.AppendMessages(types.Message{UserMessage: req.UserMessage})

	buf := &bytes.Buffer{}
	if a.opt.SystemPrompt != "" {
		buf.WriteString(a.opt.SystemPrompt)
		buf.WriteString("\n")
	}
	buf.WriteString(a.opt.LeaderPrompt)

	var leaderTools []*tools.Tool
	leaderTools = append(leaderTools, planningAgt.PlanningTools()...)
	leaderTools = append(leaderTools, a.leaderTools...)
	leader := a.newReAct("leader", "", promptWithMoreInfo(buf.String()), leaderTools)
	go func() {
		defer resp.Close()
	Retry:
		if err := a.doPlanning(ctx, FIRST_PLANNING_PROMPT, planningAgt, req, resp); err != nil {
			a.logger.Errorw("failed to planning", "error", err)
			return
		}

		if len(planningAgt.TodoList()) == 0 {
			req.Memory.AppendMessages(types.Message{
				AgentMessage: "You haven't generated any to-do items, and you'll lose your job. You're being given one last chance."})
			goto Retry
		}

		if err := a.doResearch(ctx, leader, planningAgt, req, resp); err != nil {
			a.logger.Errorw("do research failed", "err", err)
			return
		}
		if err := a.doSummary(ctx, req, resp); err != nil {
			a.logger.Errorw("do summary failed", "err", err)
			return
		}
	}()

	return resp
}

func (a *Agent) doPlanning(ctx context.Context, userMessage string, planningAgt *planning.Agent, req *agtapi.Request, resp *agtapi.Response) error {
	var startAt = time.Now()
	a.logger.Infow("run planning")

	userMessage = strings.ReplaceAll(userMessage, "{user_task}", req.UserMessage)
	var (
		content string
		stream  = planningAgt.Chat(ctx, &agtapi.Request{
			UserMessage: userMessage,
			Memory:      req.Memory.Copy(),
		})
		err error
	)

	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			goto Finish
		case evt, ok := <-stream.Events():
			if !ok {
				goto Finish
			}
			if evt.Delta != nil && evt.Delta.Content != "" {
				content += evt.Delta.Content
				agtapi.SendEventToResponse(ctx, types.NewReasoningEvent(evt.Delta.Content))
			}
		case err := <-stream.Error():
			if err != nil {
				resp.Fail(err)
				a.logger.Errorw("do planning error", "err", err)
				return err
			}
		}
	}

Finish:
	a.logger.Infow("planning finish", "escape", time.Since(startAt).String())
	for _, item := range planningAgt.TodoList() {
		a.logger.Infof("planning: [DONE=%v] %s", item.IsFinish, item.Describe)
	}
	return err
}

func (a *Agent) doResearch(ctx context.Context, leader *react.Agent, planningAgt *planning.Agent, req *agtapi.Request, resp *agtapi.Response) error {
	var (
		failCount    = 0
		runTaskCount = 0
		allFinish    = false
	)
	for !allFinish {
		runTaskCount++
		if err := a.runTask(ctx, leader, runTaskPrompt(planningAgt.TodoList()), req); err != nil {
			a.logger.Warnw("run task failed, skip and next", "err", err)
			if failCount += 1; failCount >= 5 {
				return err
			}
		}

		for _, item := range planningAgt.TodoList() {
			a.logger.Infof("research: [DONE=%v] %s", item.IsFinish, item.Describe)
		}

		if runTaskCount >= a.opt.MaxResearchLoopTimes {
			a.logger.Warnw("research loop limit exceeded")
			return nil
		}

		allFinish = planningAgt.AllFinish()
		if !allFinish {
			req.Memory.AppendMessages(types.Message{AgentMessage: "Warning message: There are currently unfinished items in your todo list. " +
				"If you believe you have completed them, you need to use a tool to update the todo list status."})
			a.logger.Infow("researching not finish, try again later", "researchTimes", runTaskCount, "researchLoopLimit", a.opt.MaxResearchLoopTimes)
		}
	}
	a.logger.Infow("all finish, close researching")
	return nil
}

func (a *Agent) runTask(ctx context.Context, leader *react.Agent, task string, req *agtapi.Request) error {
	var (
		stream = leader.Chat(ctx, &agtapi.Request{
			UserMessage: task,
			Memory:      req.Memory,
		})
		content string
		err     error
		startAt = time.Now()
	)
	a.logger.Infow("run research", "task", task)

	content, err = agtapi.ReadAllContent(ctx, stream)
	content = strings.TrimSpace(content)
	if err != nil {
		if content == "" {
			content = "Error: " + err.Error()
		} else {
			content += "\nError: not finish, " + err.Error()
		}
	}
	a.logger.Infow("research finish", "task", task, "escape", time.Since(startAt).String())
	if content != "" {
		req.Memory.AppendMessages(
			types.Message{AssistantMessage: content},
		)
	}

	return err
}

func (a *Agent) doSummary(ctx context.Context, req *agtapi.Request, resp *agtapi.Response) error {
	a.logger.Infow("run summary")
	agt := simple.New("summary", "", a.llm, simple.Option{SystemPrompt: a.opt.SummaryPrompt})
	userMessage := SUMMARYRE_USER_PROMPT

	userMessage = strings.ReplaceAll(userMessage, "{user_task}", req.UserMessage)
	stream := agt.Chat(ctx, &agtapi.Request{
		UserMessage: userMessage,
		Memory:      req.Memory,
	})
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
			if evt.Delta != nil && evt.Delta.Content != "" {
				agtapi.SendEventToResponse(ctx, types.NewAnsEvent(evt.Delta.Content))
			}
		}
	}
}

func (a *Agent) newReAct(name, desc, systemPrompt string, toolList []*tools.Tool) *react.Agent {
	return react.New(name, desc, a.llm, react.Option{
		SystemPrompt: systemPrompt,
		MaxLoopTimes: 4,
		MaxToolCalls: 30,
		Tools:        toolList,
	})
}

func New(name, desc string, llm openai.Client, opt Option) *Agent {
	if opt.LeaderPrompt == "" {
		opt.LeaderPrompt = LEAD_PROMPT
	}
	if opt.SubAgentPrompt == "" {
		opt.SubAgentPrompt = SUBAGENT_PROMPT
	}
	if opt.CitationPrompt == "" {
		opt.CitationPrompt = CITATION_PROMPT
	}
	if opt.SummaryPrompt == "" {
		opt.SummaryPrompt = SUMMARYRE_SYSTEM_PROMPT
	}
	if opt.MaxResearchLoopTimes == 0 {
		opt.MaxResearchLoopTimes = 5
	}

	agt := &Agent{
		name:   name,
		desc:   desc,
		llm:    llm,
		opt:    opt,
		logger: logger.New("research"),
	}
	agt.leaderTools = append(agt.leaderTools, newResearchTool(agt)...)

	return agt
}

type Option struct {
	LeaderPrompt         string
	SubAgentPrompt       string
	SystemPrompt         string
	CitationPrompt       string
	SummaryPrompt        string
	MaxResearchLoopTimes int
	Tools                []*tools.Tool
	PlanningTools        []*tools.Tool
}
