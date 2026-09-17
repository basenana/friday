package agents

import (
	"fmt"
	"testing"
)

type mutableSpecProvider struct{ specs []*AgentSpec }

func (p *mutableSpecProvider) List() []*AgentSpec { return p.specs }

func TestExpertProviderRebuildsAndKeepsLastGoodGeneration(t *testing.T) {
	specs := &mutableSpecProvider{specs: []*AgentSpec{{Name: "writer", Description: "writes", SystemPrompt: "Write."}}}
	factory := NewClientFactory(&forkableClient{fakeClient: fakeClient{name: "primary"}})
	provider, err := NewExpertProvider(specs, factory, "workspace", nil, func(spec *AgentSpec) error {
		if spec.Model == "invalid" {
			return fmt.Errorf("invalid model")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.List(); len(got) != 1 || got[0].Name != "writer" {
		t.Fatalf("initial experts = %#v", got)
	}
	specs.specs = []*AgentSpec{{Name: "reviewer", Description: "reviews", SystemPrompt: "Review."}}
	if got := provider.List(); len(got) != 1 || got[0].Name != "reviewer" {
		t.Fatalf("reloaded experts = %#v", got)
	}
	specs.specs = []*AgentSpec{{Name: "broken", Description: "broken", SystemPrompt: "Break.", Model: "invalid"}}
	if got := provider.List(); len(got) != 1 || got[0].Name != "reviewer" {
		t.Fatalf("last-good experts = %#v", got)
	}
}
