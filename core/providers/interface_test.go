package providers

import (
	"testing"

	"github.com/basenana/friday/core/types"
)

// baseRequest intentionally implements only Request, proving optional
// reasoning support does not widen the public base contract.
type baseRequest struct {
	history []types.Message
	tools   []ToolDefine
	system  string
	cache   string
}

func (r *baseRequest) Messages() []types.Message         { return r.history }
func (r *baseRequest) History() []types.Message          { return r.history }
func (r *baseRequest) ToolDefines() []ToolDefine         { return r.tools }
func (r *baseRequest) SystemPrompt() string              { return r.system }
func (r *baseRequest) PromptCacheKey() string            { return r.cache }
func (r *baseRequest) SetHistory(v []types.Message)      { r.history = v }
func (r *baseRequest) SetToolDefines(v []ToolDefine)     { r.tools = v }
func (r *baseRequest) SetSystemPrompt(v string)          { r.system = v }
func (r *baseRequest) SetPromptCacheKey(v string)        { r.cache = v }
func (r *baseRequest) AppendHistory(v ...types.Message)  { r.history = append(r.history, v...) }
func (r *baseRequest) AppendToolDefines(v ...ToolDefine) { r.tools = append(r.tools, v...) }
func (r *baseRequest) AppendSystemPrompt(v ...string) {
	for _, value := range v {
		r.system += value
	}
}

var _ Request = (*baseRequest)(nil)

func TestReasoningEffortIsOptionalRequestCapability(t *testing.T) {
	req := &baseRequest{}
	if got := RequestReasoningEffort(req); got != "" {
		t.Fatalf("base request effort = %q", got)
	}
	if SetRequestReasoningEffort(req, "high") {
		t.Fatal("base request unexpectedly reported reasoning capability")
	}
	if got := RequestDefaultReasoningEffort(req); got != "" {
		t.Fatalf("base request default effort = %q", got)
	}
	if SetRequestDefaultReasoningEffort(req, "medium") {
		t.Fatal("base request unexpectedly reported default reasoning capability")
	}
}

func TestCommonRequestSupportsDefaultReasoningEffort(t *testing.T) {
	req := NewRequest("system")
	if !SetRequestDefaultReasoningEffort(req, "medium") {
		t.Fatal("common request does not support default reasoning effort")
	}
	if got := RequestDefaultReasoningEffort(req); got != "medium" {
		t.Fatalf("default effort = %q, want medium", got)
	}
}
