package codebase

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/promptcontext"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

func TestProjectConversationHistoryKeepsOnlyIndependentUserAssistantText(t *testing.T) {
	image := &types.ImageContent{Type: types.ImageTypeURL, URL: "https://example.invalid/image"}
	input := []types.Message{
		{Role: types.RoleAgent, Content: "project instructions"},
		{Role: types.RoleUser, Content: "  user question  ", Metadata: map[string]string{"private": "metadata"}, Image: image, Tokens: 20},
		{Role: types.RoleAssistant, Content: "assistant answer", Reasoning: "thinking", ReasoningSignature: "signature", ToolCalls: []types.ToolCall{{ID: "call", Name: "read"}}, Time: time.Unix(123, 0)},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call", Content: "tool output", Success: true}},
		{Role: types.RoleAssistant, Content: "  ", ToolCalls: []types.ToolCall{{ID: "empty", Name: "search"}}},
	}

	got := projectConversationHistory(input)
	want := []types.Message{
		{Role: types.RoleUser, Content: "  user question  "},
		{Role: types.RoleAssistant, Content: "assistant answer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projected history=%+v, want %+v", got, want)
	}
	got[0].Content = "changed"
	if input[1].Content != "  user question  " || input[1].Metadata["private"] != "metadata" || input[2].Reasoning != "thinking" {
		t.Fatalf("projection mutated source: %+v", input)
	}
}

func TestAutomaticContextProjectionPreservesMainRequestCacheAndHistory(t *testing.T) {
	root := coresession.New("root", nil)
	root.EnsureContextState().TokenCheckpoint = coresession.TokenCheckpoint{Index: 7, PromptTokens: 1234}
	root.EnsureContextState().PromptBudget = coresession.PromptBudget{ContextWindow: 128000, HardThreshold: 90000}
	checkpointBefore := root.EnsureContextState().TokenCheckpoint
	budgetBefore := root.EnsureContextState().PromptBudget
	original := []types.Message{
		{Role: types.RoleUser, Content: "question", Metadata: map[string]string{"keep": "yes"}},
		{Role: types.RoleAgent, Content: "request-local instructions"},
		{Role: types.RoleAssistant, Content: "answer", Reasoning: "private reasoning"},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{Content: "tool noise"}},
	}
	var gathered []types.Message
	h := &Hook{root: root, gather: func(_ context.Context, _ *coresession.Session, history []types.Message, _ uint64) (string, error) {
		gathered = history
		if len(history) > 0 {
			history[0].Content = "mutated Context copy"
		}
		return "evidence", nil
	}}
	if err := h.BeforeAgent(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	req := providers.NewRequest("", original...)
	req.SetPromptCacheKey("session:root")
	if err := h.BeforeModel(context.Background(), root, req); err != nil {
		t.Fatal(err)
	}
	if req.PromptCacheKey() != "session:root" {
		t.Fatalf("prompt cache key changed to %q", req.PromptCacheKey())
	}
	if len(gathered) != 2 || gathered[0].Role != types.RoleUser || gathered[1].Role != types.RoleAssistant {
		t.Fatalf("gathered history=%+v", gathered)
	}
	history := req.History()
	if len(history) != len(original)+1 || !reflect.DeepEqual(history[1:], original) {
		t.Fatalf("main request history changed beyond evidence injection: %+v", history)
	}
	if len(root.GetHistory()) != 0 {
		t.Fatalf("root Session history changed: %+v", root.GetHistory())
	}
	if got := root.EnsureContextState().TokenCheckpoint; got != checkpointBefore {
		t.Fatalf("token checkpoint changed: got %+v want %+v", got, checkpointBefore)
	}
	if got := root.EnsureContextState().PromptBudget; got != budgetBefore {
		t.Fatalf("prompt budget changed: got %+v want %+v", got, budgetBefore)
	}
}

func TestHookFreezesOneContextResultForRootAndForks(t *testing.T) {
	root := coresession.New("root", nil)
	var calls atomic.Int32
	h := &Hook{root: root, gather: func(context.Context, *coresession.Session, []types.Message, uint64) (string, error) {
		calls.Add(1)
		return "evidence", nil
	}}
	root.RegisterHook(h)
	if err := h.BeforeAgent(context.Background(), root, nil); err != nil {
		t.Fatal(err)
	}
	req := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "question"})
	if err := h.BeforeModel(context.Background(), root, req); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || req.History()[0].Metadata["friday.codebase"] != "context" {
		t.Fatalf("calls=%d history=%+v", calls.Load(), req.History())
	}
	second := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "question"})
	if err := h.BeforeModel(context.Background(), root, second); err != nil {
		t.Fatal(err)
	}
	fork := root.Fork()
	forkReq := providers.NewRequest("", fork.GetHistory()...)
	if err := h.BeforeModel(context.Background(), fork, forkReq); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || forkReq.History()[0].Content == "" {
		t.Fatalf("calls=%d fork=%+v", calls.Load(), forkReq.History())
	}
	if len(root.GetHistory()) != 0 {
		t.Fatal("evidence leaked into persistent history")
	}
}

func TestHookConcurrentForksWaitForFrozenEvidence(t *testing.T) {
	root := coresession.New("root", nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h := &Hook{root: root, gather: func(context.Context, *coresession.Session, []types.Message, uint64) (string, error) {
		calls.Add(1)
		close(started)
		<-release
		return "shared", nil
	}}
	_ = h.BeforeAgent(context.Background(), root, nil)
	rootReq := providers.NewRequest("")
	done := make(chan error, 1)
	go func() { done <- h.BeforeModel(context.Background(), root, rootReq) }()
	<-started
	fork := root.Fork()
	forkReq := providers.NewRequest("")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := h.BeforeModel(context.Background(), fork, forkReq); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-time.After(20 * time.Millisecond):
	case <-done:
		t.Fatal("root completed before release")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if calls.Load() != 1 || len(forkReq.History()) != 1 {
		t.Fatalf("calls=%d history=%v", calls.Load(), forkReq.History())
	}
}

func TestHookLatePriorTurnDoesNotOverwriteCurrentGeneration(t *testing.T) {
	root := coresession.New("root", nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h := &Hook{root: root, gather: func(context.Context, *coresession.Session, []types.Message, uint64) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return "old evidence", nil
		}
		return "new evidence", nil
	}}
	_ = h.BeforeAgent(context.Background(), root, nil)
	oldDone := make(chan error, 1)
	go func() { oldDone <- h.BeforeModel(context.Background(), root, providers.NewRequest("")) }()
	<-started
	_ = h.BeforeAgent(context.Background(), root, nil)
	current := providers.NewRequest("")
	if err := h.BeforeModel(context.Background(), root, current); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}
	later := providers.NewRequest("")
	if err := h.BeforeModel(context.Background(), root, later); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.History()[0].Content, "new evidence") || !strings.Contains(later.History()[0].Content, "new evidence") {
		t.Fatalf("current generation overwritten: current=%q later=%q", current.History()[0].Content, later.History()[0].Content)
	}
}

func TestIndexRequestKeepsApprovedPlanSeparateFromFallibleEvidence(t *testing.T) {
	req := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "persistent history"})
	promptcontext.SetBlock(req, promptcontext.ProjectInstructions, "project instructions")
	hook := requestInputHook{text: "repository and conversation evidence", approvedPlan: "approved plan"}
	if err := hook.BeforeModel(context.Background(), nil, req); err != nil {
		t.Fatal(err)
	}
	history := req.History()
	if len(history) != 3 {
		t.Fatalf("history=%+v", history)
	}
	if history[0].Metadata["friday.codebase"] != "index" || history[0].Content != "repository and conversation evidence" {
		t.Fatalf("fallible evidence=%+v", history[0])
	}
	if !strings.Contains(history[1].Content, "project instructions") || !strings.Contains(history[1].Content, "approved plan") {
		t.Fatalf("built-in context=%+v", history[1])
	}
	if strings.Contains(history[0].Content, "approved plan") {
		t.Fatalf("approved plan merged into fallible evidence: %q", history[0].Content)
	}
}

func TestEvidencePrecedesBuiltinContextAndTruncatesWithinBudget(t *testing.T) {
	root := coresession.New("root", nil)
	root.EnsureContextState().PromptBudget.HardThreshold = 150
	req := providers.NewRequest("")
	promptcontext.SetBlock(req, promptcontext.ProjectInstructions, "project instructions")
	promptcontext.SetBlock(req, promptcontext.ApprovedPlan, "approved plan")
	injectEvidence(root, req, strings.Repeat("证据", 1000))

	history := req.History()
	if len(history) != 2 || history[0].Metadata["friday.codebase"] != "context" {
		t.Fatalf("history=%+v", history)
	}
	if !strings.Contains(history[1].Content, "project instructions") || !strings.Contains(history[1].Content, "approved plan") {
		t.Fatalf("built-in context moved or lost: %+v", history[1])
	}
	if !utf8.ValidString(history[0].Content) || !strings.HasSuffix(history[0].Content, "[truncated]\n[/Codebase Context]") {
		t.Fatalf("invalid truncated evidence: %q", history[0].Content)
	}
	used := coresession.EstimateHistoryTokens(history[1:])
	if got, max := len(history[0].Content), int((150-used)*4); got > max {
		t.Fatalf("evidence bytes=%d exceed budget-derived max=%d", got, max)
	}
}

func TestHookInjectsActiveQueryToolForRootAndFork(t *testing.T) {
	root := coresession.New("root", nil)
	var queries atomic.Int32
	h := &Hook{
		root:    root,
		enabled: func() bool { return true },
		query: func(_ context.Context, _ *coresession.Session, query string, _ uint64, maxChars int64) (string, error) {
			queries.Add(1)
			return query + " bounded", nil
		},
	}
	rootReq := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root, rootReq); err != nil {
		t.Fatal(err)
	}
	if len(rootReq.Tools) != 1 || rootReq.Tools[0].Name != codebaseContextQueryToolName {
		t.Fatalf("root tools=%+v", rootReq.Tools)
	}
	forkReq := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root.Fork(), forkReq); err != nil {
		t.Fatal(err)
	}
	if len(forkReq.Tools) != 1 || forkReq.Tools[0].Name != codebaseContextQueryToolName {
		t.Fatalf("fork tools=%+v", forkReq.Tools)
	}
	result, err := rootReq.Tools[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "architecture"}, MaxOutputChars: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || queries.Load() != 1 || result.Content[0].(tools.TextContent).Text != "architecture bounded" {
		t.Fatalf("result=%+v queries=%d", result, queries.Load())
	}
}

func TestHookIsInvisibleWhenCodebaseDisabled(t *testing.T) {
	root := coresession.New("root", nil)
	h := &Hook{root: root, enabled: func() bool { return false }}
	agentReq := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root, agentReq); err != nil {
		t.Fatal(err)
	}
	if len(agentReq.Tools) != 0 {
		t.Fatalf("disabled hook injected tools: %+v", agentReq.Tools)
	}
	modelReq := providers.NewRequest("")
	if err := h.BeforeModel(context.Background(), root, modelReq); err != nil {
		t.Fatal(err)
	}
	if len(modelReq.History()) != 0 || strings.Contains(modelReq.SystemPrompt(), "codebase_context_query") {
		t.Fatalf("disabled hook changed request: history=%+v system=%q", modelReq.History(), modelReq.SystemPrompt())
	}
}

func TestHookLimitsActiveQueriesPerRootTurn(t *testing.T) {
	root := coresession.New("root", nil)
	h := &Hook{root: root, enabled: func() bool { return true }, query: func(context.Context, *coresession.Session, string, uint64, int64) (string, error) { return "ok", nil }}
	req := &api.Request{}
	if err := h.BeforeAgent(context.Background(), root, req); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxCodebaseQueriesPerTurn; i++ {
		result, err := req.Tools[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "question"}})
		if err != nil || result.IsError {
			t.Fatalf("query %d result=%+v err=%v", i, result, err)
		}
	}
	result, err := req.Tools[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"query": "one too many"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("fourth query was accepted: %+v", result)
	}
}

func TestHookPropagatesRootCancellation(t *testing.T) {
	root := coresession.New("root", nil)
	h := &Hook{root: root, gather: func(ctx context.Context, _ *coresession.Session, _ []types.Message, _ uint64) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	_ = h.BeforeAgent(context.Background(), root, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.BeforeModel(ctx, root, providers.NewRequest("")); err != context.Canceled {
		t.Fatalf("err=%v", err)
	}
}
