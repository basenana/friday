package providers

import "github.com/basenana/friday/core/types"

type CommonResponse struct {
	Stream chan Delta
	Err    chan error
	Token  Tokens
}

func (r *CommonResponse) Message() <-chan Delta { return r.Stream }
func (r *CommonResponse) Error() <-chan error   { return r.Err }
func (r *CommonResponse) Tokens() Tokens        { return r.Token }

func NewCommonResponse() *CommonResponse {
	return &CommonResponse{Stream: make(chan Delta, 5), Err: make(chan error, 1)}
}

type commonRequest struct {
	systemPrompts []string
	tools         []ToolDefine
	history       []types.Message
}

func NewRequest(systemMessage string, history ...types.Message) Request {
	return &commonRequest{systemPrompts: []string{systemMessage}, history: history}
}

func (s *commonRequest) Messages() []types.Message {
	result := make([]types.Message, 0, len(s.history)+1)
	result = append(result, types.Message{SystemMessage: s.SystemPrompt()})
	result = append(result, s.history...)
	return result
}

func (s *commonRequest) History() []types.Message {
	return s.history
}

func (s *commonRequest) ToolDefines() []ToolDefine {
	return s.tools
}

func (s *commonRequest) SystemPrompt() string {
	var result string
	for _, p := range s.systemPrompts {
		result += p + "\n\n"
	}
	return result
}

func (s *commonRequest) SetHistory(history []types.Message) {
	s.history = history
}

func (s *commonRequest) SetToolDefines(tools []ToolDefine) {
	var filtered []ToolDefine
	existedTool := make(map[string]struct{})

	for _, t := range tools {
		_, exists := existedTool[t.GetName()]
		if exists {
			continue
		}
		filtered = append(filtered, t)
		existedTool[t.GetName()] = struct{}{}
	}
	s.tools = filtered
}

func (s *commonRequest) SetSystemPrompt(prompt string) {
	s.systemPrompts = []string{prompt}
}

func (s *commonRequest) AppendHistory(messages ...types.Message) {
	s.history = append(s.history, messages...)
}

func (s *commonRequest) AppendToolDefines(tools ...ToolDefine) {
	for i, t := range tools {
		var exists bool
		for _, existing := range s.tools {
			if existing.GetName() == t.GetName() {
				exists = true
				s.tools[i] = t
				break
			}
		}
		if !exists {
			s.tools = append(s.tools, t)
		}
	}
}

func (s *commonRequest) AppendSystemPrompt(prompts ...string) {
	s.systemPrompts = append(s.systemPrompts, prompts...)
}

type commonToolDefine struct {
	name        string
	description string
	parameters  map[string]any
}

func NewToolDefine(name, description string, parameters map[string]any) ToolDefine {
	return commonToolDefine{name: name, description: description, parameters: parameters}
}

func (s commonToolDefine) GetName() string               { return s.name }
func (s commonToolDefine) GetDescription() string        { return s.description }
func (s commonToolDefine) GetParameters() map[string]any { return s.parameters }
