package simple

import (
	"context"
	"encoding/json"
	"time"

	agtapi2 "github.com/basenana/friday/core/agents/agtapi"
	"github.com/basenana/friday/core/memory"
	"github.com/basenana/friday/core/providers/openai"
	"go.uber.org/zap"

	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
)

type Agent struct {
	name        string
	description string
	llm         openai.Client
	option      Option
	logger      *zap.SugaredLogger
}

func (s *Agent) Name() string {
	return s.name
}

func (s *Agent) Describe() string {
	return s.description
}

func (s *Agent) Chat(ctx context.Context, req *agtapi2.Request) *agtapi2.Response {
	var (
		resp = agtapi2.NewResponse()
	)

	if req.Session == nil {
		req.Session = types.NewDummySession()
	}

	if req.Memory == nil {
		req.Memory = memory.NewEmpty(req.Session.ID)
	}

	ctx = agtapi2.NewContext(ctx, req.Session,
		agtapi2.WithMemory(req.Memory),
		agtapi2.WithResponse(resp),
	)

	mem := req.Memory
	mem.AppendMessages(types.Message{UserMessage: req.UserMessage})

	s.logger.Infow("handle request", "session", req.Session.ID, "userMessage", req.UserMessage)

	if s.option.NewOutputModel == nil {
		go s.handleLLMStream(ctx, mem, resp)
	} else {
		go s.handleStructLLMOutput(ctx, mem, resp)
	}

	return resp
}

func (s *Agent) handleLLMStream(ctx context.Context, mem *memory.Memory, resp *agtapi2.Response) {
	defer resp.Close()
	var (
		msgCnt     int
		llmReq     = openai.NewSimpleRequest(s.option.SystemPrompt, mem.History()...)
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
			s.logger.Warnw("still waiting llm completed", "receivedMessage", msgCnt)

		case msg, ok := <-stream.Message():
			if !ok {
				break WaitMessage
			}

			msgCnt++ // check api hang
			switch {
			case len(msg.Content) > 0:
				agtapi2.SendEvent(resp, types.NewContentEvent(msg.Content))
			case len(msg.Reasoning) > 0:
				agtapi2.SendEvent(resp, types.NewReasoningEvent(msg.Reasoning))
			}
		}
	}
}

func (s *Agent) handleStructLLMOutput(ctx context.Context, mem *memory.Memory, resp *agtapi2.Response) {
	defer resp.Close()
	var (
		llmReq = openai.NewSimpleRequest(s.option.SystemPrompt, mem.History()...)
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
	agtapi2.SendEvent(resp, types.NewContentEvent(string(raw)))
}

func New(name, desc string, llm openai.Client, opt Option) *Agent {
	return &Agent{
		name:        name,
		description: desc,
		llm:         llm,
		option:      opt,
		logger:      logger.New("simple").With(zap.String("name", name)),
	}
}

type Option struct {
	SystemPrompt   string
	NewOutputModel func() any
}
