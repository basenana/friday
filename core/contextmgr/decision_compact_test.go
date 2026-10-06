package contextmgr

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type recordingDecisionProvider struct {
	mu       sync.Mutex
	requests []providers.DecisionRequest
	response providers.DecisionResponse
	err      error
	fn       func(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error)
}

func (p *recordingDecisionProvider) Evaluate(ctx context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if p.fn != nil {
		return p.fn(ctx, req)
	}
	return p.response, p.err
}

func (p *recordingDecisionProvider) snapshot() []providers.DecisionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]providers.DecisionRequest(nil), p.requests...)
}

func decisionCompactHistory() []types.Message {
	largeArgs := `{"path":"` + strings.Repeat("a", 1200) + `"}`
	return []types.Message{
		{Role: types.RoleUser, Content: "old goal"},
		{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{
			{ID: "call-1", Name: "fs_read", Arguments: largeArgs},
			{ID: "call-2", Name: "fs_read", Arguments: `{"path":"important"}`},
		}},
		{Role: types.RoleTool, Content: "SHADOW-SECRET-ONE", ToolResult: &types.ToolResult{CallID: "call-1", Content: "SECRET-ONE-" + strings.Repeat("x", 1200), Success: true}},
		{Role: types.RoleTool, Content: "SHADOW-SECRET-TWO", ToolResult: &types.ToolResult{CallID: "call-2", Content: "SECRET-TWO-" + strings.Repeat("y", 1200), Success: false, Status: "error"}},
		{Role: types.RoleAssistant, Content: "old answer"},
		{Role: types.RoleUser, Content: "goal two"}, {Role: types.RoleAssistant, Content: "two"},
		{Role: types.RoleUser, Content: "goal three"}, {Role: types.RoleAssistant, Content: "three"},
		{Role: types.RoleUser, Content: "goal four"}, {Role: types.RoleAssistant, Content: "four"},
		{Role: types.RoleUser, Content: strings.Repeat("界", 600)}, {Role: types.RoleAssistant, Content: "five"},
	}
}

func decisionAnswers(values map[string]float64) providers.DecisionResponse {
	answers := make(map[string]providers.DecisionAnswer, len(values))
	for key, value := range values {
		answers[key] = providers.NoulAnswer{Noul: value}
	}
	return providers.DecisionResponse{Answers: answers}
}

func runDecisionCompact(t *testing.T, provider providers.DecisionProvider) (*session.Session, providers.Request) {
	t.Helper()
	sess := session.New("decision-session", nil, session.WithHistory(decisionCompactHistory()...))
	mgr := New(nil, Config{
		ContextWindow:      2000,
		SoftThresholdRatio: 0.10,
		HardThresholdRatio: 2,
		MaxToolResultChars: 20,
		DecisionProvider:   provider,
	})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	return sess, req
}

func TestDecisionMicroCompactBuildsQuestionsWithoutToolResultBodies(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 1, "result_t1": 1,
		"call_t2": 1, "result_t2": 1,
	})}
	_, _ = runDecisionCompact(t, provider)

	requests := provider.snapshot()
	if len(requests) != 1 {
		t.Fatalf("Evaluate calls = %d, want 1", len(requests))
	}
	got := requests[0]
	for _, key := range []string{"call_t1", "result_t1", "call_t2", "result_t2"} {
		q, ok := got.Questions[key].(providers.NoulQuestion)
		if !ok || q.Instructions == nil || q.Criteria == nil || q.Criteria.True == nil || q.Criteria.False == nil {
			t.Fatalf("question %q = %#v", key, got.Questions[key])
		}
	}
	encoded, err := json.Marshal(got.State)
	if err != nil {
		t.Fatal(err)
	}
	stateJSON := string(encoded)
	if strings.Contains(stateJSON, "SECRET-ONE") || strings.Contains(stateJSON, "SECRET-TWO") {
		t.Fatalf("state leaked tool result content: %s", stateJSON)
	}
	state, ok := got.State.(decisionCompactState)
	if !ok {
		t.Fatalf("state type = %T", got.State)
	}
	if len([]rune(state.Goal)) == 0 || len([]rune(state.Goal)) > 500+1+len([]rune("goal three"))+1+len([]rune("goal four")) {
		t.Fatalf("goal rune length = %d", len([]rune(state.Goal)))
	}
	if !strings.Contains(state.Goal, strings.Repeat("界", 500)) || strings.Contains(state.Goal, strings.Repeat("界", 501)) {
		t.Fatalf("goal was not capped to 500 runes: %q", state.Goal)
	}
}

func TestDecisionMicroCompactDropsCallAndResultAsAPair(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 0.1, "result_t1": 0.1,
		"call_t2": 0.1, "result_t2": 0.9,
	})}
	sess, req := runDecisionCompact(t, provider)

	var call1, result1, call2, result2 bool
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			call1 = call1 || call.ID == "call-1"
			call2 = call2 || call.ID == "call-2"
		}
		if msg.ToolResult != nil {
			result1 = result1 || msg.ToolResult.CallID == "call-1"
			result2 = result2 || msg.ToolResult.CallID == "call-2"
		}
	}
	if call1 || result1 || !call2 || !result2 {
		t.Fatalf("pair presence call1=%v result1=%v call2=%v result2=%v", call1, result1, call2, result2)
	}
	original := append([]types.Message(nil), sess.GetHistory()...)
	originalJSON, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(originalJSON), "SECRET-ONE") || !strings.Contains(string(originalJSON), "call-1") {
		t.Fatal("session history was mutated by projection")
	}
	if !reflect.DeepEqual(original, sess.GetHistory()) {
		t.Fatal("session history was mutated by projection (deep equal)")
	}
}

func TestDecisionMicroCompactDropsOnlyResultWhenCallMatters(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 0.8, "result_t1": 0.2,
		"call_t2": 1, "result_t2": 1,
	})}
	_, req := runDecisionCompact(t, provider)

	var callFound, resultFound bool
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			callFound = callFound || call.ID == "call-1"
		}
		if msg.ToolResult != nil && msg.ToolResult.CallID == "call-1" {
			resultFound = true
			if msg.ToolResult.Content == "SECRET-ONE-"+strings.Repeat("x", 1200) || len([]rune(msg.ToolResult.Content)) > 23 {
				t.Fatalf("result was not pruned: %q", msg.ToolResult.Content)
			}
		}
	}
	if !callFound || !resultFound {
		t.Fatalf("call/result presence = %v/%v", callFound, resultFound)
	}
}

func TestDecisionMicroCompactMalformedAnswersKeepOnlyAffectedPair(t *testing.T) {
	provider := &recordingDecisionProvider{response: providers.DecisionResponse{Answers: map[string]providers.DecisionAnswer{
		"call_t1":   providers.NoulAnswer{Noul: math.NaN()},
		"result_t1": providers.NoulAnswer{Noul: 0.1},
		"call_t2":   providers.NoulAnswer{Noul: 0.1},
		"result_t2": providers.NoulAnswer{Noul: 0.1},
	}}}
	_, req := runDecisionCompact(t, provider)

	var call1, result1, call2, result2 bool
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			call1 = call1 || call.ID == "call-1"
			call2 = call2 || call.ID == "call-2"
		}
		if msg.ToolResult != nil {
			result1 = result1 || msg.ToolResult.CallID == "call-1"
			result2 = result2 || msg.ToolResult.CallID == "call-2"
		}
	}
	if !call1 || !result1 || call2 || result2 {
		t.Fatalf("pair presence call1=%v result1=%v call2=%v result2=%v", call1, result1, call2, result2)
	}
}

func TestDecisionMicroCompactProviderFailureFallsBack(t *testing.T) {
	provider := &recordingDecisionProvider{err: errors.New("offline")}
	_, req := runDecisionCompact(t, provider)
	var foundPruned bool
	for _, msg := range req.History() {
		if msg.ToolResult != nil && msg.ToolResult.CallID == "call-1" {
			foundPruned = len([]rune(msg.ToolResult.Content)) <= 23
		}
	}
	if !foundPruned {
		t.Fatal("provider failure did not use deterministic projection")
	}
}

func TestDecisionMicroCompactParentCancellationReturnsError(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(ctx context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		<-ctx.Done()
		return providers.DecisionResponse{}, ctx.Err()
	}}
	sess := session.New("cancelled", nil, session.WithHistory(decisionCompactHistory()...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.BeforeModel(ctx, sess, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if calls := len(provider.snapshot()); calls != 0 {
		t.Fatalf("Evaluate calls after parent cancellation = %d, want 0", calls)
	}
}

func TestDecisionMicroCompactDropsMultipleCallsFromOneMessage(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 0, "result_t1": 0,
		"call_t2": 0, "result_t2": 0,
	})}
	_, req := runDecisionCompact(t, provider)
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			if call.ID == "call-1" || call.ID == "call-2" {
				t.Fatalf("dropped call remains: %s", call.ID)
			}
		}
		if msg.ToolResult != nil && (msg.ToolResult.CallID == "call-1" || msg.ToolResult.CallID == "call-2") {
			t.Fatalf("dropped result remains: %s", msg.ToolResult.CallID)
		}
	}
}

func TestDecisionMicroCompactUsesLocalDeadline(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(ctx context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("Evaluate context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 29*time.Second || remaining > 31*time.Second {
			t.Fatalf("Evaluate deadline remaining = %v", remaining)
		}
		return providers.DecisionResponse{}, errors.New("stop")
	}}
	_, _ = runDecisionCompact(t, provider)
}

func TestDecisionMicroCompactWithoutCandidatesSkipsProvider(t *testing.T) {
	provider := &recordingDecisionProvider{}
	history := []types.Message{
		{Role: types.RoleUser, Content: strings.Repeat("a", 600)},
		{Role: types.RoleAssistant, Content: "one"},
		{Role: types.RoleUser, Content: "two"}, {Role: types.RoleAssistant, Content: "two"},
		{Role: types.RoleUser, Content: "three"}, {Role: types.RoleAssistant, Content: "three"},
		{Role: types.RoleUser, Content: "four"}, {Role: types.RoleAssistant, Content: "four"},
		{Role: types.RoleUser, Content: "five"}, {Role: types.RoleAssistant, Content: "five"},
	}
	sess := session.New("no-candidates", nil, session.WithHistory(history...))
	mgr := New(nil, Config{ContextWindow: 1000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	if calls := len(provider.snapshot()); calls != 0 {
		t.Fatalf("Evaluate calls = %d, want 0", calls)
	}
}

func TestDecisionMicroCompactPreservesLatestOldImage(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 0, "result_t1": 0,
		"call_t2": 1, "result_t2": 1,
	})}
	history := decisionCompactHistory()
	history[0].Image = &types.ImageContent{Type: types.ImageTypeBase64, Data: "image-data"}
	sess := session.New("image", nil, session.WithHistory(history...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, msg := range req.History() {
		found = found || msg.Image != nil && msg.Image.Data == "image-data"
	}
	if !found {
		t.Fatal("latest old image was removed from projection")
	}
	if sess.GetHistory()[0].Image == nil || sess.GetHistory()[0].Image.Data != "image-data" {
		t.Fatal("session image was mutated")
	}
}

func TestDecisionMicroCompactMissingAnswerKeyKeepsAffectedPair(t *testing.T) {
	provider := &recordingDecisionProvider{response: providers.DecisionResponse{Answers: map[string]providers.DecisionAnswer{
		"call_t1": providers.NoulAnswer{Noul: 0.9},
		// result_t1 missing
		"call_t2":   providers.NoulAnswer{Noul: 0.1},
		"result_t2": providers.NoulAnswer{Noul: 0.1},
	}}}
	_, req := runDecisionCompact(t, provider)
	var call1, result1, call2, result2 bool
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			call1 = call1 || call.ID == "call-1"
			call2 = call2 || call.ID == "call-2"
		}
		if msg.ToolResult != nil {
			result1 = result1 || msg.ToolResult.CallID == "call-1"
			result2 = result2 || msg.ToolResult.CallID == "call-2"
		}
	}
	if !call1 || !result1 || call2 || result2 {
		t.Fatalf("pair presence call1=%v result1=%v call2=%v result2=%v", call1, result1, call2, result2)
	}
}

func TestDecisionMicroCompactWrongAnswerTypeKeepsAffectedPair(t *testing.T) {
	provider := &recordingDecisionProvider{response: providers.DecisionResponse{Answers: map[string]providers.DecisionAnswer{
		"call_t1":   providers.NoulAnswer{Noul: 0.1},
		"result_t1": providers.ChoiceAnswer{Choice: "drop"},
		"call_t2":   providers.NoulAnswer{Noul: 0.1},
		"result_t2": providers.NoulAnswer{Noul: 0.1},
	}}}
	_, req := runDecisionCompact(t, provider)
	var call1, result1, call2, result2 bool
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			call1 = call1 || call.ID == "call-1"
			call2 = call2 || call.ID == "call-2"
		}
		if msg.ToolResult != nil {
			result1 = result1 || msg.ToolResult.CallID == "call-1"
			result2 = result2 || msg.ToolResult.CallID == "call-2"
		}
	}
	if !call1 || !result1 || call2 || result2 {
		t.Fatalf("pair presence call1=%v result1=%v call2=%v result2=%v", call1, result1, call2, result2)
	}
}

func TestDecisionMicroCompactNotBeneficialDoesNotFreezeProjection(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 1, "result_t1": 1,
		"call_t2": 1, "result_t2": 1,
	})}
	sess, req := runDecisionCompact(t, provider)
	if sess.EnsureContextState().MicroCompactSourceMessages != 0 {
		t.Fatal("not-beneficial decision projection was frozen")
	}
	if got, want := len(req.History()), len(sess.GetHistory()); got != want {
		t.Fatalf("projected history length = %d, want full history length %d", got, want)
	}
}

func TestDecisionMicroCompactBoundaryPairFallsBack(t *testing.T) {
	history := []types.Message{
		{Role: types.RoleUser, Content: "goal"},
		{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: "call-edge", Name: "fs_read", Arguments: `{"path":"x"}`}}},
		// First tail message is the matching tool result. The pair straddles
		// the boundary; conservative keep means the Jev path must NOT issue a
		// provider call for it.
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-edge", Content: "SECRET-EDGE" + strings.Repeat("z", 800), Success: true}},
		{Role: types.RoleUser, Content: "two"}, {Role: types.RoleAssistant, Content: "two"},
		{Role: types.RoleUser, Content: "three"}, {Role: types.RoleAssistant, Content: "three"},
		{Role: types.RoleUser, Content: "four"}, {Role: types.RoleAssistant, Content: "four"},
		{Role: types.RoleUser, Content: "five"}, {Role: types.RoleAssistant, Content: "five"},
	}
	provider := &recordingDecisionProvider{}
	sess := session.New("boundary", nil, session.WithHistory(history...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	// Boundary pair is the only candidate — collectDecisionPairs cannot find
	// it because the result lives in the tail slice. The path either calls no
	// provider or falls back to deterministic without panic.
	if got := len(provider.snapshot()); got > 0 {
		var saw bool
		for _, msg := range req.History() {
			if msg.ToolResult != nil && msg.ToolResult.CallID == "call-edge" && strings.Contains(msg.ToolResult.Content, "SECRET-EDGE") {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("boundary result body lost; provider was called %d times", got)
		}
	}
}

func TestDecisionMicroCompactFrozenProjectionSkipsProvider(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 0, "result_t1": 0,
		"call_t2": 1, "result_t2": 1,
	})}
	sess, firstReq := runDecisionCompact(t, provider)
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	if calls := len(provider.snapshot()); calls != 1 {
		t.Fatalf("Evaluate calls = %d, want 1", calls)
	}
	if len(req.History()) != len(firstReq.History()) {
		t.Fatalf("frozen projection length = %d, want %d", len(req.History()), len(firstReq.History()))
	}
}
