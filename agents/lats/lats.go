package lats

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/react"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
)

type Agent struct {
	name string
	desc string

	task string
	root *SearchNode

	llm   openai.Client
	react *react.Agent

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
	if req.Memory == nil {
		req.Memory = memory.NewEmptyWithSummarize(req.SessionID, a.llm)
	}
	ctx = memory.WithMemory(ctx, req.Memory)

	if a.root == nil {
		a.task = req.UserMessage
		a.root = newRoot(a.option.SystemPrompt, a.task, req.Memory.Copy())
	}
	resp := agtapi.NewResponse()

	go func() {
		a.logger.Infow("handle request", "message", req.UserMessage)
		defer resp.Close()
		for {
			ans, finish, err := a.runStep(ctx, req, resp)
			if err != nil {
				resp.Fail(err)
				return
			}

			if finish {
				a.sendFinalAnswer(ctx, ans, req.Memory, req, resp)
				return
			}
		}
	}()
	return resp
}

func (a *Agent) runStep(ctx context.Context, req *agtapi.Request, resp *agtapi.Response) (string, bool, error) {
	crtNode := a.root.GetBestNode()
	a.logger.Infow("[TREE] selecting node to expand", "node", crtNode.Latest())

	candidates, err := a.extendCandidates(ctx, crtNode)
	if err != nil {
		a.logger.Errorw("extend candidates failed", "err", err)
		return "", false, err
	}

	if len(candidates) == 0 {
		a.logger.Info("no candidates found, retry")
		return "", false, nil
	}

	var nextMove []*SearchNode
	for _, candidate := range candidates {
		a.logger.Infow("[TREE] generated new reasoning step", "candidate", candidate)
		n := newNode(candidate)
		n.info = &types.Stage{ID: n.id, Describe: candidate, Status: types.Submitted}
		crtNode.Expend(n, nil)
		agtapi.SendEvent(req, resp, types.NewStageUpdateEvent(*n.info))
		nextMove = append(nextMove, n)
	}

	var (
		wg                    = sync.WaitGroup{}
		finishCollect         = make(chan struct{})
		solutionQueue         = make(chan *SearchNode, 5)
		ans                   *SearchNode
		bestAns               int
		parallel              = make(chan struct{}, a.option.MaxParallel)
		batchCtx, cancelBatch = context.WithCancel(ctx)
	)
	defer close(parallel)
	defer cancelBatch()

	go func() {
		for s := range solutionQueue {
			if s.evaluation.Score > bestAns {
				bestAns = s.evaluation.Score
				ans = s
			}
		}
		close(finishCollect)
	}()

	for _, child := range nextMove {
		wg.Add(1)
		parallel <- struct{}{}
		go func(node *SearchNode) {
			defer func() {
				<-parallel
				wg.Done()
			}()

			node.info.Status = types.Working
			agtapi.SendEvent(req, resp, types.NewStageUpdateEvent(*node.info))
			defer func() {
				agtapi.SendEvent(req, resp, types.NewStageUpdateEvent(*node.info))
			}()

			reasoning, err := a.runCandidate(batchCtx, node, req, resp)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					node.info.Status = types.Canceled
					return
				}
				if reasoning == "" {
					node.info.Status = types.Unknown
					a.logger.Errorw("runCandidate failed", "err", "reasoning is empty")
					return
				}
				node.info.Message = err.Error()
				a.logger.Infow("[TREE] run candidate failed, but got part reasoning", "err", err, "reasoning", reasoning)
			}

			e, err := a.evaluate(batchCtx, node, reasoning)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					node.info.Status = types.Canceled
					return
				}
				node.info.Status = types.Failed
				node.info.Message = err.Error()
				a.logger.Errorw("evaluate failed", "err", err)
				return
			}
			agtapi.SendEvent(req, resp, types.NewReasoningEvent(""),
				"evaluation", e.Reasoning,
				"stage", node.info.ID, "score", fmt.Sprint(e.Score), "is_done", fmt.Sprint(e.IsDone))

			a.logger.Infow("[TREE] node evaluate finish", "score", e.Score, "isDone", e.IsDone, "reasoning", e.Reasoning)
			nn := newNode(reasoning)
			node.Expend(nn, e)
			if nn.evaluation.IsDone {
				a.logger.Infow("[TREE] found solution node", "node", nn.Latest())
				solutionQueue <- nn

				if a.option.FastMode {
					cancelBatch()
				}
			}

			node.info.Status = types.Completed
		}(child)
	}
	wg.Wait()
	close(solutionQueue)
	<-finishCollect

	if ans != nil {
		a.logger.Infow("got final response", "answer", ans.Latest(), "rollouts", len(crtNode.reasoning))
		return ans.Latest(), true, nil
	}

	if len(crtNode.reasoning)-1 >= a.option.MaxRollouts {
		a.logger.Warnw("[TREE] rollout limit reached", "rollouts", len(crtNode.reasoning))
		return crtNode.Latest(), true, nil
	}

	return "", false, nil
}

func (a *Agent) extendCandidates(ctx context.Context, node *SearchNode) ([]string, error) {
	buf := &bytes.Buffer{}
	if a.option.SystemPrompt != "" {
		buf.WriteString(a.option.SystemPrompt)
		buf.WriteString("\n")
	}
	prompt := a.option.ExpansionPrompt
	prompt = strings.ReplaceAll(prompt, "{num_candidates}", fmt.Sprintf("%d", a.option.Expansions))
	prompt = strings.ReplaceAll(prompt, "{query}", a.task)
	prompt = strings.ReplaceAll(prompt, "{conversation_history}", conversationHistoryMessages(node.reasoning...))

	buf.WriteString(prompt)

	var (
		cand         = &Candidates{}
		runCtx, canF = context.WithTimeout(ctx, time.Minute)
		err          error
	)
	defer canF()
	for i := 0; i < 3; i++ {
		err = a.llm.StructuredPredict(runCtx, openai.NewSimpleRequest(buf.String()), cand)
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, err
	}

	if len(cand.Candidates) > a.option.Expansions {
		cand.Candidates = cand.Candidates[:a.option.Expansions]
	}

	return cand.Candidates, nil
}

func (a *Agent) runCandidate(ctx context.Context, node *SearchNode, req *agtapi.Request, resp *agtapi.Response) (string, error) {
	var (
		runCtx, canF = context.WithTimeout(ctx, time.Minute*2)
		message      = node.Latest()
		reasoning    string
	)
	defer canF()

	a.logger.Infow("[TREE-NODE] start run candidate", "node", message)
	nowAt := time.Now()
	defer func() {
		a.logger.Infow("[TREE-NODE] run candidate finish", "cost", time.Since(nowAt))
	}()

	nextReq := &agtapi.Request{
		UserMessage:       message,
		SessionID:         req.SessionID,
		Memory:            node.memory,
		ExtraKV:           make([]string, 0, len(req.ExtraKV)+4),
		OverwriteToolArgs: req.OverwriteToolArgs,
	}
	nextReq.ExtraKV = append(nextReq.ExtraKV, req.ExtraKV...)
	nextReq.ExtraKV = append(nextReq.ExtraKV, []string{"stage", node.info.ID}...)
	stream := a.react.Chat(runCtx, nextReq)

WaitingRun:
	for {
		select {
		case <-ctx.Done():
			return reasoning, ctx.Err()
		case err := <-stream.Error():
			if err != nil {
				return reasoning, err
			}
		case evt, ok := <-stream.Events():
			if !ok {
				break WaitingRun
			}
			if evt.Delta != nil {
				if msg := evt.Delta; msg.Content != "" {
					agtapi.SendEvent(nextReq, resp, types.NewReasoningEvent(msg.Content))
					reasoning += msg.Content
				}
				continue
			}
			if evt.Data != nil {
				agtapi.SendEvent(nextReq, resp, &evt)
			}
		}
	}
	if reasoning == "" {
		a.logger.Warn("[TREE-NODE] candidate finished, but has no messages")
	}
	return reasoning, nil
}

func (a *Agent) evaluate(ctx context.Context, node *SearchNode, reasoning string) (*Evaluation, error) {
	var (
		prompt = a.option.ReflectionPrompt
		nowAt  = time.Now()
		eva    = &Evaluation{Score: 1}
		buf    = &bytes.Buffer{}
		err    error
	)

	if a.option.SystemPrompt != "" {
		buf.WriteString(a.option.SystemPrompt)
		buf.WriteString("\n")
	}
	prompt = strings.ReplaceAll(prompt, "{query}", a.task)
	prompt = strings.ReplaceAll(prompt, "{conversation_history}", conversationHistoryMessages(append(node.reasoning, reasoning)...))
	buf.WriteString(prompt)

	a.logger.Infow("start run evaluate")
	defer func() {
		a.logger.Infow("run evaluate finish", "cost", time.Since(nowAt))
	}()

	ctx, canF := context.WithTimeout(ctx, time.Minute)
	defer canF()

	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			err = a.llm.StructuredPredict(ctx, openai.NewSimpleRequest(prompt), eva)
		}
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, err
	}

	return eva, nil
}

func (a *Agent) sendFinalAnswer(ctx context.Context, ans string, mem *memory.Memory, req *agtapi.Request, resp *agtapi.Response) {
	agtapi.SendEvent(req, resp, types.NewAnsEvent(ans))
	if mem != nil {
		mem.AppendMessages(types.Message{AssistantMessage: ans})
	}
}

func New(name, desc string, llm openai.Client, reAct *react.Agent, opt Option) *Agent {
	if opt.Expansions == 0 {
		opt.Expansions = 2
	}
	if opt.MaxRollouts == 0 {
		opt.MaxRollouts = 5
	}
	if opt.MaxParallel == 0 {
		opt.MaxParallel = 5
	}
	if opt.ExpansionPrompt == "" {
		opt.ExpansionPrompt = DEFAULT_CANDIDATES_PROMPT
	}
	if opt.ReflectionPrompt == "" {
		opt.ReflectionPrompt = DEFAULT_REFLECTION_PROMPT
	}

	return &Agent{
		name:   name,
		desc:   desc,
		llm:    llm,
		react:  reAct,
		option: opt,
		logger: logger.New("lats").With(zap.String("name", name)),
	}
}

type Option struct {
	Expansions  int
	MaxRollouts int
	MaxParallel int
	FastMode    bool

	SystemPrompt     string
	ExpansionPrompt  string
	ReflectionPrompt string

	Tools []*tools.Tool
}

func conversationHistoryMessages(messages ...string) string {
	ch := &ConversationHistory{Messages: messages}
	content, _ := xml.Marshal(ch)
	return string(content)
}
