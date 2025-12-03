package simple

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
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

func (s *Agent) Chat(ctx context.Context, req *agtapi.Request) *agtapi.Response {
	var resp = agtapi.NewResponse()

	if req.Memory == nil {
		req.Memory = memory.NewEmptyWithSummarize(req.SessionID, s.llm)
	}

	mem := req.Memory
	mem.AppendMessages(types.Message{UserMessage: req.UserMessage})
	ctx = memory.WithMemory(ctx, mem)

	if s.option.NewOutputModel == nil {
		go s.handleLLMStream(ctx, mem, req, resp)
	} else {
		go s.handleStructLLMOutput(ctx, mem, req, resp)
	}

	return resp
}

func (s *Agent) handleLLMStream(ctx context.Context, mem *memory.Memory, req *agtapi.Request, resp *agtapi.Response) {
	defer resp.Close()
	var (
		msgCnt     int
		llmReq     = memory.LLMRequest(s.option.SystemPrompt, mem)
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
				agtapi.SendEvent(req, resp, types.NewContentEvent(msg.Content))
			case len(msg.Reasoning) > 0:
				agtapi.SendEvent(req, resp, types.NewReasoningEvent(msg.Reasoning))
			}
		}
	}
}

func (s *Agent) handleStructLLMOutput(ctx context.Context, mem *memory.Memory, req *agtapi.Request, resp *agtapi.Response) {
	defer resp.Close()
	var (
		llmReq = memory.LLMRequest(s.option.SystemPrompt, mem)
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
	agtapi.SendEvent(req, resp, types.NewContentEvent(string(raw)))
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
