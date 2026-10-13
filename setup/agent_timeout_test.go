package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers/fallback"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/workspace"
)

// stuckAgent returns a stream that never terminates and ignores its context,
// so only the wrapper deadline can end the turn.
type stuckAgent struct{}

func (stuckAgent) Chat(_ context.Context, _ *api.Request) *api.Response {
	return api.NewResponse()
}

// scriptedAgent streams fixed deltas and closes cleanly.
type scriptedAgent struct {
	deltas []string
}

func (a *scriptedAgent) Chat(_ context.Context, _ *api.Request) *api.Response {
	resp := api.NewResponse()
	go func() {
		defer resp.Close()
		for _, d := range a.deltas {
			api.SendDelta(resp, types.Delta{Content: d})
		}
	}()
	return resp
}

func TestTimeoutAgentPassThrough(t *testing.T) {
	inner := &scriptedAgent{}
	if got := newTimeoutAgent(inner, 0); got != agents.Agent(inner) {
		t.Fatal("zero timeout should return the wrapped agent unchanged")
	}
	if got := newTimeoutAgent(inner, -time.Second); got != agents.Agent(inner) {
		t.Fatal("negative timeout should return the wrapped agent unchanged")
	}
}

func TestTimeoutAgentForwardsDeltas(t *testing.T) {
	wrapped := newTimeoutAgent(&scriptedAgent{deltas: []string{"a", "b", "c"}}, time.Hour)
	resp := wrapped.Chat(context.Background(), &api.Request{})

	var got []string
	deadline := time.After(2 * time.Second)
Loop:
	for {
		select {
		case err := <-resp.Error():
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		case d, ok := <-resp.Deltas():
			if !ok {
				break Loop
			}
			got = append(got, d.Content)
		case <-deadline:
			t.Fatal("stream did not terminate")
		}
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("deltas = %v, want [a b c]", got)
	}
}

func TestTimeoutAgentExpiry(t *testing.T) {
	wrapped := newTimeoutAgent(stuckAgent{}, 100*time.Millisecond)
	start := time.Now()
	resp := wrapped.Chat(context.Background(), &api.Request{})

	select {
	case err := <-resp.Error():
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not fail at the deadline")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expiry took %v, want prompt failure", elapsed)
	}
	select {
	case _, ok := <-resp.Deltas():
		if ok {
			t.Fatal("delta received after deadline failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deltas channel did not close after failure")
	}
}

func TestTimeoutAgentRespectsParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := newTimeoutAgent(stuckAgent{}, time.Hour)
	resp := wrapped.Chat(ctx, &api.Request{})
	cancel()

	select {
	case err := <-resp.Error():
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not fail on parent cancellation")
	}
}

func TestNewAgentWrapsAgentWithTurnTimeout(t *testing.T) {
	base := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false
	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatal(err)
	}
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &recordingProviderClient{}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "recording"}})

	cfg.Agent.TurnTimeout = "1h"
	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), WithWorkdir(base))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := agentCtx.Agent.(*timeoutAgent); !ok {
		t.Fatalf("agent = %T, want *timeoutAgent", agentCtx.Agent)
	}
	agentCtx.Close()

	cfg.Agent.TurnTimeout = ""
	agentCtx, err = NewAgent(mgr, cfg, WithModelPool(pool), WithWorkdir(base))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := agentCtx.Agent.(*timeoutAgent); ok {
		t.Fatalf("agent = %T, want unwrapped without turn_timeout", agentCtx.Agent)
	}
	agentCtx.Close()
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
}
