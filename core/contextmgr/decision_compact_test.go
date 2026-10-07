package contextmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
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

func TestDecisionMicroCompactProviderFailureKeepsFailedBatch(t *testing.T) {
	provider := &recordingDecisionProvider{err: errors.New("offline")}
	sess, req := runDecisionCompact(t, provider)
	if got, want := len(req.History()), len(sess.GetHistory()); got != want {
		t.Fatalf("provider failure projection length = %d, want full history length %d", got, want)
	}
	for _, msg := range req.History() {
		if msg.ToolResult != nil && msg.ToolResult.CallID == "call-1" && !strings.HasPrefix(msg.ToolResult.Content, "SECRET-ONE-") {
			t.Fatalf("failed batch result was changed: %q", msg.ToolResult.Content)
		}
	}
	if _, err := sess.ReadRecord(context.Background(), decisionCacheNamespace); !errors.Is(err, session.ErrRecordNotFound) {
		t.Fatalf("cache read error = %v, want ErrRecordNotFound", err)
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

func decisionBatchHistory(pairCount int) []types.Message {
	calls := make([]types.ToolCall, pairCount)
	history := []types.Message{{Role: types.RoleUser, Content: "batch goal"}}
	for i := range pairCount {
		id := fmt.Sprintf("call-%03d", i+1)
		calls[i] = types.ToolCall{ID: id, Name: "tool", Arguments: fmt.Sprintf(`{"pair":%d,"marker":"ARG-%03d"}`, i+1, i+1)}
	}
	history = append(history, types.Message{Role: types.RoleAssistant, ToolCalls: calls})
	for i := range pairCount {
		history = append(history, types.Message{
			Role:    types.RoleTool,
			Content: fmt.Sprintf("SHADOW-%03d", i+1),
			ToolResult: &types.ToolResult{
				CallID:  calls[i].ID,
				Content: fmt.Sprintf("SECRET-%03d-%s", i+1, strings.Repeat("x", 80)),
				Success: true,
			},
		})
	}
	history = append(history, types.Message{Role: types.RoleAssistant, Content: "old answer"})
	for i := 0; i < 4; i++ {
		history = append(history,
			types.Message{Role: types.RoleUser, Content: fmt.Sprintf("recent %d", i+1)},
			types.Message{Role: types.RoleAssistant, Content: "recent answer"},
		)
	}
	return history
}

func runDecisionBatchCompact(t *testing.T, pairCount int, provider providers.DecisionProvider) (*session.Session, providers.Request) {
	t.Helper()
	sess := session.New("decision-batch-session", nil, session.WithHistory(decisionBatchHistory(pairCount)...))
	mgr := New(nil, Config{
		ContextWindow:      1000,
		SoftThresholdRatio: 0.01,
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

func dropAllDecisionAnswers(req providers.DecisionRequest) providers.DecisionResponse {
	answers := make(map[string]providers.DecisionAnswer, len(req.Questions))
	for key := range req.Questions {
		answers[key] = providers.NoulAnswer{Noul: 0}
	}
	return providers.DecisionResponse{Answers: answers}
}

func TestDecisionMicroCompactBatchLimits(t *testing.T) {
	for _, tt := range []struct {
		pairs         int
		wantQuestions []int
	}{
		{pairs: 50, wantQuestions: []int{100}},
		{pairs: 51, wantQuestions: []int{100, 2}},
		{pairs: 137, wantQuestions: []int{100, 100, 74}},
	} {
		t.Run(fmt.Sprintf("pairs_%d", tt.pairs), func(t *testing.T) {
			provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
				return dropAllDecisionAnswers(req), nil
			}}
			_, _ = runDecisionBatchCompact(t, tt.pairs, provider)
			requests := provider.snapshot()
			got := make([]int, len(requests))
			for i, req := range requests {
				got[i] = len(req.Questions)
			}
			slices.Sort(got)
			want := append([]int(nil), tt.wantQuestions...)
			slices.Sort(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("question counts = %v, want %v", got, want)
			}
		})
	}
}

func TestDecisionMicroCompactBatchStartsAllRequestsConcurrently(t *testing.T) {
	const batches = 3
	var entered sync.WaitGroup
	entered.Add(batches)
	release := make(chan struct{})
	provider := &recordingDecisionProvider{fn: func(ctx context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		entered.Done()
		select {
		case <-release:
			return dropAllDecisionAnswers(req), nil
		case <-ctx.Done():
			return providers.DecisionResponse{}, ctx.Err()
		}
	}}
	done := make(chan error, 1)
	go func() {
		sess := session.New("concurrent-batches", nil, session.WithHistory(decisionBatchHistory(137)...))
		mgr := New(nil, Config{ContextWindow: 1000, SoftThresholdRatio: 0.01, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
		req := providers.NewRequest("", sess.GetHistory()...)
		done <- mgr.BeforeModel(context.Background(), sess, req)
	}()

	allEntered := make(chan struct{})
	go func() { entered.Wait(); close(allEntered) }()
	select {
	case <-allEntered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("not all batches entered Evaluate concurrently")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDecisionMicroCompactBatchStateIsIsolatedAndOmitsResultBodies(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		return dropAllDecisionAnswers(req), nil
	}}
	_, _ = runDecisionBatchCompact(t, 51, provider)
	requests := provider.snapshot()
	if len(requests) != 2 {
		t.Fatalf("Evaluate calls = %d, want 2", len(requests))
	}
	seenArguments := make(map[string]bool)
	for _, req := range requests {
		state, ok := req.State.(decisionCompactState)
		if !ok {
			t.Fatalf("state type = %T", req.State)
		}
		batchArguments := 0
		for _, msg := range state.History {
			for _, call := range msg.ToolCalls {
				batchArguments++
				if seenArguments[call.Arguments] {
					t.Fatalf("arguments appeared in multiple batches: %s", call.Arguments)
				}
				seenArguments[call.Arguments] = true
			}
		}
		if batchArguments*2 != len(req.Questions) {
			t.Fatalf("state calls = %d, questions = %d", batchArguments, len(req.Questions))
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "SECRET-") || strings.Contains(string(encoded), "SHADOW-") {
			t.Fatalf("batch state leaked tool result body: %s", encoded)
		}
	}
	if len(seenArguments) != 51 {
		t.Fatalf("unique arguments = %d, want 51", len(seenArguments))
	}
}

func TestDecisionMicroCompactCacheAvoidsRepeatProviderCallBelowGate(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		answers := make(map[string]providers.DecisionAnswer, len(req.Questions))
		for key := range req.Questions {
			answers[key] = providers.NoulAnswer{Noul: 1}
		}
		return providers.DecisionResponse{Answers: answers}, nil
	}}
	sess := session.New("cache-below-gate", nil, session.WithHistory(decisionCompactHistory()...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	for i := 0; i < 2; i++ {
		req := providers.NewRequest("", sess.GetHistory()...)
		if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
			t.Fatal(err)
		}
		if got, want := len(req.History()), len(sess.GetHistory()); got != want {
			t.Fatalf("run %d projected history = %d, want %d", i+1, got, want)
		}
	}
	if calls := len(provider.snapshot()); calls != 1 {
		t.Fatalf("Evaluate calls = %d, want 1", calls)
	}
	data, err := sess.ReadRecord(context.Background(), decisionCacheNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET-") || strings.Contains(string(data), `"path"`) {
		t.Fatalf("cache leaked tool content: %s", data)
	}
	var cache decisionEpochCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	epoch, ok := decisionEpochKey(sess.ID, sess.GetHistory())
	if !ok || cache.Epoch != epoch {
		t.Fatalf("cache epoch = %q, want %q", cache.Epoch, epoch)
	}
	if len(cache.Decisions) != 2 || cache.Decisions["call-1"].Action != "keep" || cache.Decisions["call-2"].Action != "keep" {
		t.Fatalf("cached decisions = %#v", cache.Decisions)
	}
}

func TestDecisionMicroCompactBeneficialProjectionCachesBeforeFreezing(t *testing.T) {
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 0, "result_t1": 0,
		"call_t2": 0, "result_t2": 0,
	})}
	sess, first := runDecisionCompact(t, provider)
	if len(first.History()) >= len(sess.GetHistory()) {
		t.Fatal("beneficial projection was not applied")
	}
	if sess.EnsureContextState().MicroCompactSourceMessages == 0 {
		t.Fatal("beneficial projection was not frozen")
	}
	data, err := sess.ReadRecord(context.Background(), decisionCacheNamespace)
	if err != nil {
		t.Fatal(err)
	}
	var cache decisionEpochCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if len(cache.Decisions) != 2 || cache.Decisions["call-1"].Action != "drop_call" || cache.Decisions["call-2"].Action != "drop_call" {
		t.Fatalf("cached decisions = %#v", cache.Decisions)
	}

	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	second := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, second); err != nil {
		t.Fatal(err)
	}
	if calls := len(provider.snapshot()); calls != 1 {
		t.Fatalf("Evaluate calls = %d, want 1 after frozen fast path", calls)
	}
	if !reflect.DeepEqual(first.History(), second.History()) {
		t.Fatal("frozen projection changed")
	}
}

func TestDecisionMicroCompactBatchFailureKeepsOnlyFailedBatchAndCachesSuccesses(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		for _, msg := range req.State.(decisionCompactState).History {
			for _, call := range msg.ToolCalls {
				if strings.Contains(call.Arguments, "ARG-051") {
					return providers.DecisionResponse{}, errors.New("batch failed")
				}
			}
		}
		return dropAllDecisionAnswers(req), nil
	}}
	sess, req := runDecisionBatchCompact(t, 51, provider)
	var kept51, kept1 bool
	for _, msg := range req.History() {
		for _, call := range msg.ToolCalls {
			kept1 = kept1 || call.ID == "call-001"
			kept51 = kept51 || call.ID == "call-051"
		}
	}
	if kept1 || !kept51 {
		t.Fatalf("kept successful/failed batch calls = %v/%v, want false/true", kept1, kept51)
	}

	epoch, ok := decisionEpochKey(sess.ID, sess.GetHistory())
	if !ok {
		t.Fatal("missing epoch")
	}
	cache := loadDecisionCache(context.Background(), sess, epoch)
	if len(cache.Decisions) != 50 || cache.Decisions["call-051"].Action != "" {
		t.Fatalf("cached decisions = %d, failed pair = %#v", len(cache.Decisions), cache.Decisions["call-051"])
	}
}

func TestDecisionMicroCompactParentCancellationCancelsAllBatchesWithoutCacheWrite(t *testing.T) {
	const batches = 3
	var entered sync.WaitGroup
	entered.Add(batches)
	finished := make(chan struct{}, batches)
	provider := &recordingDecisionProvider{fn: func(ctx context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		entered.Done()
		<-ctx.Done()
		finished <- struct{}{}
		return providers.DecisionResponse{}, ctx.Err()
	}}
	sess := session.New("cancel-batches", nil, session.WithHistory(decisionBatchHistory(137)...))
	mgr := New(nil, Config{ContextWindow: 1000, SoftThresholdRatio: 0.01, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.BeforeModel(ctx, sess, req) }()
	allEntered := make(chan struct{})
	go func() { entered.Wait(); close(allEntered) }()
	select {
	case <-allEntered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("not all batches entered Evaluate")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := len(finished); got != batches {
		t.Fatalf("finished batches before return = %d, want %d", got, batches)
	}
	if _, err := sess.ReadRecord(context.Background(), decisionCacheNamespace); !errors.Is(err, session.ErrRecordNotFound) {
		t.Fatalf("cache read error = %v, want ErrRecordNotFound", err)
	}
}

func TestDecisionMicroCompactBatchCompletionOrderDoesNotChangeProjection(t *testing.T) {
	run := func(delayFirst bool) []types.Message {
		provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
			_, first := req.Questions["call_t1"]
			if first == delayFirst {
				time.Sleep(20 * time.Millisecond)
			}
			return dropAllDecisionAnswers(req), nil
		}}
		_, req := runDecisionBatchCompact(t, 51, provider)
		return req.History()
	}
	firstLate, secondLate := run(true), run(false)
	if !reflect.DeepEqual(firstLate, secondLate) {
		t.Fatal("batch completion order changed projection")
	}
}

func TestDecisionMicroCompactBatchUsesIndependentDeadlines(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(ctx context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("batch context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 29*time.Second || remaining > 31*time.Second {
			t.Fatalf("batch deadline remaining = %v", remaining)
		}
		return dropAllDecisionAnswers(req), nil
	}}
	_, _ = runDecisionBatchCompact(t, 51, provider)
	if calls := len(provider.snapshot()); calls != 2 {
		t.Fatalf("Evaluate calls = %d, want 2", calls)
	}
}

func TestDecisionMicroCompactMalformedPairIsNotCached(t *testing.T) {
	var calls int
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		calls++
		answers := make(map[string]providers.DecisionAnswer, len(req.Questions))
		for key := range req.Questions {
			answers[key] = providers.NoulAnswer{Noul: 1}
		}
		if _, ok := answers["call_t1"]; ok {
			answers["call_t1"] = providers.NoulAnswer{Noul: math.NaN()}
		}
		return providers.DecisionResponse{Answers: answers}, nil
	}}
	sess := session.New("malformed-cache", nil, session.WithHistory(decisionCompactHistory()...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	for i := 0; i < 2; i++ {
		req := providers.NewRequest("", sess.GetHistory()...)
		if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("Evaluate calls = %d, want 2 for uncached malformed pair", calls)
	}
	requests := provider.snapshot()
	if len(requests[1].Questions) != 2 {
		t.Fatalf("second request questions = %d, want 2 for one unresolved pair", len(requests[1].Questions))
	}
}

func TestDecisionMicroCompactCacheMissesChangedPairWithinEpoch(t *testing.T) {
	history := decisionSingleOldPairHistory("result-one")
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		answers := make(map[string]providers.DecisionAnswer, len(req.Questions))
		for key := range req.Questions {
			answers[key] = providers.NoulAnswer{Noul: 1}
		}
		return providers.DecisionResponse{Answers: answers}, nil
	}}
	sess := session.New("pair-hash-miss", nil, session.WithHistory(history...))
	mgr := New(nil, Config{ContextWindow: 1000, SoftThresholdRatio: 0.01, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	for _, content := range []string{"result-one", "result-two"} {
		updated := decisionSingleOldPairHistory(content)
		if err := sess.ReplaceHistory(updated...); err != nil {
			t.Fatal(err)
		}
		req := providers.NewRequest("", sess.GetHistory()...)
		if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
			t.Fatal(err)
		}
	}
	if calls := len(provider.snapshot()); calls != 2 {
		t.Fatalf("Evaluate calls = %d, want 2 after pair hash change", calls)
	}
}

func TestDecisionMicroCompactCacheEvaluatesOnlyNewCandidates(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		answers := make(map[string]providers.DecisionAnswer, len(req.Questions))
		for key := range req.Questions {
			answers[key] = providers.NoulAnswer{Noul: 1}
		}
		return providers.DecisionResponse{Answers: answers}, nil
	}}
	sess := session.New("new-candidate", nil, session.WithHistory(decisionSingleOldPairHistory("result")...))
	mgr := New(nil, Config{ContextWindow: 1000, SoftThresholdRatio: 0.01, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	first := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, first); err != nil {
		t.Fatal(err)
	}
	call := &types.Message{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: "tail-new", Name: "tail", Arguments: `{}`}}}
	result := &types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "tail-new", Content: "tail", Success: true}}
	sess.AppendMessage(call, result)
	second := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, second); err != nil {
		t.Fatal(err)
	}
	requests := provider.snapshot()
	if len(requests) != 2 {
		t.Fatalf("Evaluate calls = %d, want 2", len(requests))
	}
	if len(requests[0].Questions) != 2 || len(requests[1].Questions) != 2 {
		t.Fatalf("question counts = %d, %d; want 2, 2", len(requests[0].Questions), len(requests[1].Questions))
	}
}

func TestDecisionMicroCompactNewUserEpochReplacesRecord(t *testing.T) {
	provider := &recordingDecisionProvider{fn: func(_ context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
		answers := make(map[string]providers.DecisionAnswer, len(req.Questions))
		for key := range req.Questions {
			answers[key] = providers.NoulAnswer{Noul: 1}
		}
		return providers.DecisionResponse{Answers: answers}, nil
	}}
	sess := session.New("new-user-epoch", nil, session.WithHistory(decisionCompactHistory()...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	first := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, first); err != nil {
		t.Fatal(err)
	}
	oldEpoch, ok := decisionEpochKey(sess.ID, sess.GetHistory())
	if !ok {
		t.Fatal("missing old epoch")
	}
	user := &types.Message{Role: types.RoleUser, Content: "new turn"}
	answer := &types.Message{Role: types.RoleAssistant, Content: "continue"}
	sess.AppendMessage(user, answer)
	second := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, second); err != nil {
		t.Fatal(err)
	}
	newEpoch, ok := decisionEpochKey(sess.ID, sess.GetHistory())
	if !ok || newEpoch == oldEpoch {
		t.Fatalf("new epoch = %q, old epoch = %q", newEpoch, oldEpoch)
	}
	data, err := sess.ReadRecord(context.Background(), decisionCacheNamespace)
	if err != nil {
		t.Fatal(err)
	}
	var cache decisionEpochCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if cache.Epoch != newEpoch {
		t.Fatalf("record epoch = %q, want %q", cache.Epoch, newEpoch)
	}
	if calls := len(provider.snapshot()); calls != 2 {
		t.Fatalf("Evaluate calls = %d, want 2", calls)
	}
}

func TestDecisionMicroCompactCacheIOFailureDoesNotBlockProjection(t *testing.T) {
	store := &memoryDecisionRecordStore{records: make(map[string][]byte), err: errors.New("record unavailable")}
	provider := &recordingDecisionProvider{response: decisionAnswers(map[string]float64{
		"call_t1": 1, "result_t1": 1,
		"call_t2": 1, "result_t2": 1,
	})}
	sess := session.New("cache-io-failure", nil, session.WithRecordStore(store), session.WithHistory(decisionCompactHistory()...))
	mgr := New(nil, Config{ContextWindow: 2000, SoftThresholdRatio: 0.1, HardThresholdRatio: 2, MaxToolResultChars: 20, DecisionProvider: provider})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel returned cache I/O error: %v", err)
	}
	if calls := len(provider.snapshot()); calls != 1 {
		t.Fatalf("Evaluate calls = %d, want 1", calls)
	}
}

func decisionSingleOldPairHistory(resultContent string) []types.Message {
	history := []types.Message{
		{Role: types.RoleUser, Content: "same epoch"},
		{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: "call-old", Name: "tool", Arguments: `{"value":1}`}}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-old", Content: resultContent, Success: true}},
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("tail-%d", i)
		history = append(history,
			types.Message{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: id, Name: "tail", Arguments: `{}`}}},
			types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: id, Content: "tail", Success: true}},
		)
	}
	return history
}
