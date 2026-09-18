package api

import (
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Request struct {
	Session      *session.Session
	UserMessage  string
	AgentMessage string
	Image        *types.ImageContent
	Images       []types.ImageContent
	ImageURLs    []string
	Metadata     map[string]string
	Tools        []*tools.Tool
}

func (r *Request) GetUserMessage() string {
	return r.UserMessage
}

func (r *Request) SetUserMessage(msg string) {
	r.UserMessage = msg
}

func (r *Request) GetAgentMessage() string {
	return r.AgentMessage
}

func (r *Request) SetAgentMessage(msg string) {
	r.AgentMessage = msg
}

// InputMessage returns the role and content of the turn-starting input.
// AgentMessage takes precedence when both compatibility fields are populated.
func (r *Request) InputMessage() (types.MessageRole, string) {
	if r.AgentMessage != "" {
		return types.RoleAgent, r.AgentMessage
	}
	return types.RoleUser, r.UserMessage
}

func (r *Request) GetTools() []*tools.Tool {
	return r.Tools
}

func (r *Request) AppendTools(tool ...*tools.Tool) {
	r.Tools = append(r.Tools, tool...)
}

type Response struct {
	delta chan types.Delta
	err   chan error
}

func (r *Response) Deltas() <-chan types.Delta {
	return r.delta
}

func (r *Response) Fail(err error) {
	r.err <- err
}

func (r *Response) Error() <-chan error {
	return r.err
}

func (r *Response) Close() {
	close(r.delta)
	close(r.err)
}

func NewResponse() *Response {
	return &Response{delta: make(chan types.Delta, 5), err: make(chan error, 1)}
}

func SendDelta(resp *Response, delta types.Delta, extraKV ...string) {
	resp.delta <- delta
}
