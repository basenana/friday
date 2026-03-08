package simple

import (
	"context"
	"encoding/json"
	"time"

	agtapi "github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type Agent struct {
	name        string
	description string
	llm         providers.Client
	option      Option
	logger      logger.Logger
}

func (s *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	var (
		resp = agtapi.NewResponse()
	)

	sess := req.Session
	if sess == nil {
		sess = session.New(types.NewID(), s.llm)
	}

	err := sess.RunHooks(ctx, types.SessionHookBeforeAgent, session.HookPayload{AgentRequest: req})
	if err != nil {
		resp.Fail(err)
		return resp
	}

	sess.AppendMessage(&types.Message{UserMessage: req.UserMessage})
	s.logger.Infow("handle request", "session", req.Session.ID, "userMessage", req.UserMessage)

	if s.option.NewOutputModel == nil {
		go s.handleLLMStream(ctx, sess, resp)
	} else {
		go s.handleStructLLMOutput(ctx, sess, resp)
	}

	return resp
}

func (s *Agent) handleLLMStream(ctx context.Context, sess *session.Session, resp *agtapi.Response) {
	defer resp.Close()
	var (
		msgCnt     int
		llmReq     = providers.NewRequest(s.option.SystemPrompt, sess.History...)
		stream     = s.llm.Completion(ctx, llmReq)
		warnTicker = time.NewTicker(time.Minute)
	)

	defer warnTicker.Stop()
WaitMessage:
	for {
		select {
		case <-ctx.Done():
			resp.Fail(ctx.Err())
			return
		case err := <-stream.Error():
			if err != nil {
				resp.Fail(err)
				return
			}
		case <-warnTicker.C:
			s.logger.Warnw("still waiting llm completed", "receivedMessage", msgCnt, "session", sess.ID)

		case msg, ok := <-stream.Message():
			if !ok {
				break WaitMessage
			}

			msgCnt++ // check api hang
			switch {
			case len(msg.Content) > 0:
				agtapi.SendDelta(resp, types.Delta{Content: msg.Content})
			case len(msg.Reasoning) > 0:
				agtapi.SendDelta(resp, types.Delta{Reasoning: msg.Reasoning})
			}
		}
	}
}

func (s *Agent) handleStructLLMOutput(ctx context.Context, sess *session.Session, resp *agtapi.Response) {
	defer resp.Close()
	var (
		llmReq = providers.NewRequest(s.option.SystemPrompt, sess.History...)
		model  = s.option.NewOutputModel()
		err    = s.llm.StructuredPredict(ctx, llmReq, model)
	)

	if err != nil {
		resp.Fail(err)
		return
	}

	raw, err := json.Marshal(model)
	if err != nil {
		resp.Fail(err)
		return
	}
	agtapi.SendDelta(resp, types.Delta{Content: string(raw)})
}

func New(llm providers.Client, opt Option) *Agent {
	return &Agent{
		llm:    llm,
		option: opt,
		logger: logger.New("simple"),
	}
}

type Option struct {
	SystemPrompt   string
	NewOutputModel func() any
}
