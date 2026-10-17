package setup

import (
	"context"
	"time"

	"git.hypo.xyz/agents/friday/core/agents"
	"git.hypo.xyz/agents/friday/core/api"
)

// timeoutAgent wraps an Agent so every top-level Chat call runs under a
// wall-clock deadline. The deadline covers all LLM calls, tool execution,
// and subagent work in the turn; cancellation propagates down the context.
// A zero timeout is a pass-through.
type timeoutAgent struct {
	agent   agents.Agent
	timeout time.Duration
}

func newTimeoutAgent(agent agents.Agent, timeout time.Duration) agents.Agent {
	if timeout <= 0 {
		return agent
	}
	return &timeoutAgent{agent: agent, timeout: timeout}
}

func (t *timeoutAgent) Chat(ctx context.Context, req *api.Request) *api.Response {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	resp := t.agent.Chat(ctx, req)

	wrapped := api.NewResponse()
	go func() {
		defer cancel()
		defer wrapped.Close()
		for {
			select {
			case <-ctx.Done():
				// Keep draining the inner response so its producer never
				// blocks, then fail loud with the deadline error.
				go drainResponse(resp)
				wrapped.Fail(ctx.Err())
				return
			case err := <-resp.Error():
				if err == nil {
					continue
				}
				wrapped.Fail(err)
				return
			case d, ok := <-resp.Deltas():
				if !ok {
					return
				}
				api.SendDelta(wrapped, d)
			}
		}
	}()
	return wrapped
}

// drainResponse empties a Response that is no longer being forwarded so its
// producer never blocks on a full channel.
func drainResponse(resp *api.Response) {
	for range resp.Deltas() {
	}
	for range resp.Error() {
	}
}
