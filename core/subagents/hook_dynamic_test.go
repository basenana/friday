package subagents

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
)

type changingAgentProvider struct{ agents []ExpertAgent }

func (p *changingAgentProvider) List() []ExpertAgent { return p.agents }

func TestHookSortsAndReadsAgentProviderEveryTurn(t *testing.T) {
	provider := &changingAgentProvider{agents: []ExpertAgent{{Name: "writer", Description: "writes"}, {Name: "analyst", Description: "analyzes"}}}
	hook := NewHook(nil, Option{AgentProvider: provider})
	request := &api.Request{}
	if err := hook.BeforeAgent(context.Background(), session.New("root", nil), request); err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "run_task" {
		t.Fatalf("tools = %#v", request.Tools)
	}
	enum := runTaskAgentEnum(t, request)
	if len(enum) != 2 || enum[0] != "analyst" || enum[1] != "writer" {
		t.Fatalf("sorted enum = %#v", enum)
	}
	provider.agents = []ExpertAgent{{Name: "reviewer", Description: "reviews"}}
	request = &api.Request{}
	if err := hook.BeforeAgent(context.Background(), session.New("root-2", nil), request); err != nil {
		t.Fatal(err)
	}
	enum = runTaskAgentEnum(t, request)
	if len(enum) != 1 || enum[0] != "reviewer" {
		t.Fatalf("refreshed enum = %#v", enum)
	}
}

func runTaskAgentEnum(t *testing.T, request *api.Request) []string {
	t.Helper()
	tasks := request.Tools[0].InputSchema.Properties["tasks"].(map[string]any)
	items := tasks["items"].(map[string]any)
	properties := items["properties"].(map[string]any)
	return properties["agent_name"].(map[string]any)["enum"].([]string)
}
