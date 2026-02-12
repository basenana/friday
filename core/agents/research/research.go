package research

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	llm    openai.Client
	worker agents.Agent
	opt    Option
	logger logger.Logger
}

var _ agents.Agent = &Agent{}

func (a *Agent) Chat(ctx context.Context, req *api.Request) *api.Response {
	var (
		resp = api.NewResponse()
		sess = req.Session
	)

	if sess == nil {
		sess = session.New(types.NewID(), a.llm)
		sess.RegisterHook(planning.New(a.llm, planning.Option{}))
	}

	mergedTools := make([]*tools.Tool, 0)
	for _, t := range a.opt.ResearchTools {
		mergedTools = append(mergedTools, t)
	}
	for _, t := range req.Tools {
		mergedTools = append(mergedTools, t)
	}

	sess.AppendMessage(&types.Message{UserMessage: req.UserMessage})
	leader := newResearchLeader(a, req.Session, mergedTools)
	go func() {
		defer resp.Close()
		if err := a.leaderRun(ctx, leader, req.UserMessage, sess, resp); err != nil {
			a.logger.Warnw("run task failed, skip and next", "err", err)
		}
		if err := a.doSummary(ctx, sess, resp); err != nil {
			a.logger.Errorw("do summary failed", "err", err)
			return
		}
	}()

	return resp
}

func (a *Agent) leaderRun(ctx context.Context, leader agents.Agent, task string, sess *session.Session, resp *api.Response) error {
	var (
		stream     = leader.Chat(ctx, &api.Request{Session: sess, UserMessage: task})
		contentBuf = &bytes.Buffer{}
		startAt    = time.Now()
		err        error
	)
	a.logger.Infow("run research", "task", task)

Waiting:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err = <-stream.Error():
			if err != nil {
				return err
			}
		case delta, ok := <-stream.Deltas():
			if !ok {
				break Waiting
			}
			if delta.Content != "" {
				api.SendDelta(resp, types.Delta{Reasoning: delta.Content})
				contentBuf.WriteString(delta.Content)
			}
		}
	}

	content := strings.TrimSpace(contentBuf.String())
	if err != nil {
		if content == "" {
			content = "Error: " + err.Error()
		} else {
			content += "\nError: not finish, " + err.Error()
		}
	}
	a.logger.Infow("research finish", "task", task, "escape", time.Since(startAt).String())
	if content != "" {
		sess.AppendMessage(&types.Message{AssistantMessage: content})
	}

	return err
}

func (a *Agent) doSummary(ctx context.Context, sess *session.Session, resp *api.Response) error {
	a.logger.Infow("run summary")
	agt := agents.New(a.llm, agents.Option{SystemPrompt: a.opt.SummaryPrompt})
	userMessage := SUMMARYRE_USER_PROMPT

	history := sess.History
	if len(history) == 0 {
		return fmt.Errorf("session history is empty")
	}
	userMessage = strings.ReplaceAll(userMessage, "{user_task}", history[0].UserMessage)
	stream := agt.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: userMessage,
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
		case delta, ok := <-stream.Deltas():
			if !ok {
				return nil
			}
			if delta.Content != "" {
				api.SendDelta(resp, delta)
			}
		}
	}
}

func New(llm openai.Client, opt Option) *Agent {
	if opt.LeaderPrompt == "" {
		opt.LeaderPrompt = LEAD_PROMPT
	}
	if opt.SummaryPrompt == "" {
		opt.SummaryPrompt = SUMMARYRE_SYSTEM_PROMPT
	}
	if opt.MaxResearchLoopTimes == 0 {
		opt.MaxResearchLoopTimes = 50
	}

	if opt.SystemPrompt != "" {
		opt.LeaderPrompt = promptWithUserRequirements(opt.SystemPrompt, opt.LeaderPrompt)
		opt.SummaryPrompt = promptWithUserRequirements(opt.SystemPrompt, opt.SummaryPrompt)
	}

	if opt.Worker == nil {
		opt.Worker = NewDefaultWorker(llm, opt)
	}

	agt := &Agent{
		llm:    llm,
		worker: opt.Worker,
		opt:    opt,
		logger: logger.New("research"),
	}

	return agt
}

type Option struct {
	LeaderPrompt         string
	SummaryPrompt        string
	SystemPrompt         string
	MaxResearchLoopTimes int
	Worker               agents.Agent
	ResearchTools        []*tools.Tool
}
