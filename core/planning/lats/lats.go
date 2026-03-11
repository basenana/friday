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

	"github.com/basenana/friday/core/agents"
	agtapi "github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	task string
	root *SearchNode

	llm    providers.Client
	worker agents.Agent

	option Option
	logger logger.Logger
}

func (a *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	var (
		resp = agtapi.NewResponse()
	)

	sess := req.Session
	if sess == nil {
		sess = session.New(types.NewID(), a.llm)
	}

	if a.root == nil {
		a.task = req.UserMessage
		a.root = newRoot(a.task, sess)
	}

	a.logger.Infow("handle request", "message", logger.FirstLine(req.UserMessage), "session", sess.ID)
	go func() {
		defer resp.Close()
		for {
			ans, finish, err := a.runStep(ctx)
			if err != nil {
				resp.Fail(err)
				return
			}

			if finish {
				a.sendFinalAnswer(ctx, ans, sess)
				return
			}
		}
	}()
	return resp
}

func (a *Agent) runStep(ctx context.Context) (string, bool, error) {
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
		crtNode.Expend(n, nil)
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

			reasoning, err := a.runCandidate(batchCtx, node)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				if reasoning == "" {
					a.logger.Errorw("runCandidate failed", "err", "reasoning is empty")
					return
				}
				a.logger.Infow("[TREE] run candidate failed, but got part reasoning", "err", err, "reasoning", reasoning)
			}

			e, err := a.evaluate(batchCtx, node, reasoning)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				a.logger.Errorw("evaluate failed", "err", err)
				return
			}

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
		err = a.llm.StructuredPredict(runCtx, providers.NewRequest(buf.String()), cand)
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

func (a *Agent) runCandidate(ctx context.Context, node *SearchNode) (string, error) {
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

	stream := a.worker.Chat(runCtx, &agtapi.Request{
		Session:     node.sess,
		UserMessage: message,
	})

WaitingRun:
	for {
		select {
		case <-ctx.Done():
			return reasoning, ctx.Err()
		case err := <-stream.Error():
			if err != nil {
				return reasoning, err
			}
		case delta, ok := <-stream.Deltas():
			if !ok {
				break WaitingRun
			}
			if delta.Content != "" {
				reasoning += delta.Content
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
			err = a.llm.StructuredPredict(ctx, providers.NewRequest(prompt), eva)
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

func (a *Agent) sendFinalAnswer(ctx context.Context, ans string, sess *session.Session) {
	if sess != nil {
		sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: ans})
	}
}

func New(llm providers.Client, worker agents.Agent, opt Option) *Agent {
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
		llm:    llm,
		worker: worker,
		option: opt,
		logger: logger.New("lats"),
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
	content, err := xml.Marshal(ch)
	if err != nil {
		return "<error>failed to marshal conversation history</error>"
	}
	return string(content)
}
