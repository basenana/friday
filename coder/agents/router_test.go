package agents

import (
	"context"
	"testing"

	coreagents "github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/subagents"
)

type recordingAgent struct {
	calls int
	text  string
}

var _ coreagents.Agent = (*recordingAgent)(nil)

func (a *recordingAgent) Chat(_ context.Context, req *api.Request) *api.Response {
	a.calls++
	a.text = req.UserMessage
	resp := api.NewResponse()
	resp.Close()
	return resp
}

func TestRouterSelectionIsRequestScoped(t *testing.T) {
	primary := &recordingAgent{}
	writer := &recordingAgent{}
	router := NewRouter(primary, []subagents.ExpertAgent{{Name: "writer", Agent: writer}})

	router.Chat(context.Background(), &api.Request{UserMessage: "special", Metadata: map[string]string{RouteMetadataKey: "WRITER"}})
	router.Chat(context.Background(), &api.Request{UserMessage: "normal"})
	if writer.calls != 1 || writer.text != "special" {
		t.Fatalf("writer calls=%d text=%q", writer.calls, writer.text)
	}
	if primary.calls != 1 || primary.text != "normal" {
		t.Fatalf("primary calls=%d text=%q", primary.calls, primary.text)
	}
}

func TestRouterMissingAgentReturnsError(t *testing.T) {
	router := NewRouter(&recordingAgent{}, nil)
	resp := router.Chat(context.Background(), &api.Request{Metadata: map[string]string{RouteMetadataKey: "missing"}})
	select {
	case err := <-resp.Error():
		if err == nil {
			t.Fatal("expected missing-agent error")
		}
	default:
		t.Fatal("expected synchronous missing-agent error")
	}
	select {
	case _, ok := <-resp.Deltas():
		if ok {
			t.Fatal("expected closed delta stream")
		}
	default:
		t.Fatal("expected closed delta stream")
	}
}
