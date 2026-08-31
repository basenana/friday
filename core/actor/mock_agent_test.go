package actor

import (
	"context"
	"strconv"
	"sync"
	"time"

	coreagents "github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

// mockAgent implements coreagents.Agent. Each Chat() invocation runs
// the configured script: emit some deltas, optionally call a tool,
// then finish. This lets tests assert the actor's event flow without
// pulling in a real LLM provider.
type mockAgent struct {
	mu        sync.Mutex
	scripts   []chatScript // queued; one consumed per Chat() call
	calls     int
	toolCalls []tools.Request // captured tool invocations
	requests  []*api.Request
}

type chatScript struct {
	deltas   []types.Delta     // deltas to push before closing
	toolCall *scriptedToolCall // optional: invoke a tool mid-stream
	delay    time.Duration     // optional pacing per delta
	// blockUntil, when non-nil, makes the Chat goroutine block until
	// the channel is closed or ctx is cancelled. Used to simulate a
	// long-running turn for shutdown tests.
	blockUntil <-chan struct{}
}

type scriptedToolCall struct {
	id        string
	name      string
	arguments map[string]any
}

func newMockAgent(scripts ...chatScript) *mockAgent {
	return &mockAgent{scripts: scripts}
}

func (m *mockAgent) Chat(ctx context.Context, req *api.Request) *api.Response {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	resp := api.NewResponse()
	// Capture the snapshot of tools available to the request so the
	// background goroutine can dispatch tool calls by name.
	toolset := toolIndex(req.Tools)

	script := m.nextScript()

	go func() {
		defer resp.Close()
		// Optional pre-stream block: simulates a long-running turn.
		// Exits when blockUntil closes or the request context cancels.
		if script.blockUntil != nil {
			select {
			case <-ctx.Done():
				return
			case <-script.blockUntil:
			}
		}
		// Optional tool invocation BEFORE streaming the answer.
		if script.toolCall != nil {
			if t, ok := toolset[script.toolCall.name]; ok {
				args := script.toolCall.arguments
				if args == nil {
					args = map[string]any{}
				}
				toolID := script.toolCall.id
				if toolID == "" {
					toolID = script.toolCall.name + "-1"
				}
				if req.Session != nil {
					req.Session.PublishEvent(types.Event{
						Type: types.EventToolStart,
						Data: map[string]string{
							"id":    toolID,
							"tool":  script.toolCall.name,
							"input": tools.Res2Str(args),
						},
					})
				}
				result, handlerErr := t.Handler(ctx, &tools.Request{Arguments: args, SessionID: req.Session.ID})
				m.recordToolCall(tools.Request{Arguments: args, SessionID: req.Session.ID})
				if req.Session != nil {
					output := ""
					success := handlerErr == nil
					if handlerErr != nil {
						output = handlerErr.Error()
					} else if result != nil {
						output = tools.Res2Str(result)
						success = !result.IsError
					}
					req.Session.PublishEvent(types.Event{
						Type: types.EventToolFinish,
						Data: map[string]string{
							"id":      toolID,
							"tool":    script.toolCall.name,
							"success": strconv.FormatBool(success),
							"output":  output,
						},
					})
				}
			}
		}
		for _, d := range script.deltas {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if script.delay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(script.delay):
				}
			}
			api.SendDelta(resp, d)
		}
	}()
	return resp
}

func (m *mockAgent) requestSnapshot() []*api.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*api.Request(nil), m.requests...)
}

func (m *mockAgent) nextScript() chatScript {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls < len(m.scripts) {
		s := m.scripts[m.calls]
		m.calls++
		return s
	}
	m.calls++
	return chatScript{}
}

func (m *mockAgent) recordToolCall(r tools.Request) {
	m.mu.Lock()
	m.toolCalls = append(m.toolCalls, r)
	m.mu.Unlock()
}

// toolIndex builds a name→*Tool lookup.
func toolIndex(ts []*tools.Tool) map[string]*tools.Tool {
	out := make(map[string]*tools.Tool, len(ts))
	for _, t := range ts {
		out[t.Name] = t
	}
	return out
}

// newTestActor builds an actor backed by mockAgent + a real session.
// callers can subscribe BEFORE Start() to avoid missing RUN_STARTED.
func newTestActor(m *mockAgent, opts ...Option) (*Actor, *session.Session) {
	sess := session.New("test-session", fakeProvider{})
	a := New(m, sess, opts...)
	return a, sess
}

var _ coreagents.Agent = (*mockAgent)(nil)
