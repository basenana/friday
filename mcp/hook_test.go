package mcp

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/tools"
)

type changingToolProvider struct{ tools []*tools.Tool }

func (p *changingToolProvider) Tools() []*tools.Tool { return p.tools }

func TestHookSortsAndReadsProviderEveryTurn(t *testing.T) {
	provider := &changingToolProvider{tools: []*tools.Tool{tools.NewTool("zeta"), tools.NewTool("alpha")}}
	hook := NewHook(provider)
	first := &api.Request{}
	if err := hook.BeforeAgent(context.Background(), nil, first); err != nil {
		t.Fatal(err)
	}
	if len(first.Tools) != 2 || first.Tools[0].Name != "alpha" || first.Tools[1].Name != "zeta" {
		t.Fatalf("first tools = %#v", first.Tools)
	}
	provider.tools = []*tools.Tool{tools.NewTool("beta")}
	second := &api.Request{}
	if err := hook.BeforeAgent(context.Background(), nil, second); err != nil {
		t.Fatal(err)
	}
	if len(second.Tools) != 1 || second.Tools[0].Name != "beta" {
		t.Fatalf("second tools = %#v", second.Tools)
	}
}
