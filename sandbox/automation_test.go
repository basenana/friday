package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/basenana/friday/core/providers"
)

type fakeDecisionProvider struct {
	fn    func(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error)
	calls int
	req   providers.DecisionRequest
}

func (f *fakeDecisionProvider) Evaluate(ctx context.Context, req providers.DecisionRequest) (providers.DecisionResponse, error) {
	f.calls++
	f.req = req
	return f.fn(ctx, req)
}

func safeDecisionResponse(values ...float64) providers.DecisionResponse {
	keys := []string{"environment_damage", "data_loss", "remote_side_effect"}
	answers := make(map[string]providers.DecisionAnswer, len(values))
	for i, value := range values {
		answers[keys[i]] = providers.NoulAnswer{Noul: value}
	}
	return providers.DecisionResponse{Answers: answers}
}

func TestCommandAutomationBuildsRiskRequestAndAllowsLowRisk(t *testing.T) {
	provider := &fakeDecisionProvider{fn: func(_ context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		return safeDecisionResponse(0.1, 0.2, 0.59), nil
	}}
	automation, err := NewCommandAutomation(provider, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := automation.Assess(context.Background(), CommandRequest{
		Command: "git status && go test ./sandbox",
		Workdir: "/tmp/project",
		Mode: CommandForeground,
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.RequiresApproval {
		t.Fatal("low-risk command unexpectedly requires approval")
	}
	if provider.req.SessionID != "session-1" || len(provider.req.Questions) != 3 {
		t.Fatalf("unexpected decision request: %#v", provider.req)
	}
	for _, key := range []string{"environment_damage", "data_loss", "remote_side_effect"} {
		question, ok := provider.req.Questions[key].(providers.NoulQuestion)
		if !ok || question.Criteria == nil || question.Criteria.True == nil || question.Criteria.False == nil {
			t.Fatalf("question %q lacks explicit Noul criteria: %#v", key, provider.req.Questions[key])
		}
	}
	raw, err := json.Marshal(provider.req.State)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Command != "git status && go test ./sandbox" || decoded.Workdir != "/tmp/project" || decoded.Mode != "foreground" {
		t.Fatalf("unexpected state header: %#v", decoded)
	}
	state := string(raw)
	for _, want := range []string{"git", "status", "go", "test"} {
		if !strings.Contains(state, want) {
			t.Fatalf("state %s does not contain %q", state, want)
		}
	}
}

func TestCommandAutomationRequiresApprovalAtThreshold(t *testing.T) {
	provider := &fakeDecisionProvider{fn: func(_ context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		return safeDecisionResponse(0.1, 0.6, 0.2), nil
	}}
	automation, _ := NewCommandAutomation(provider, 0.6)
	assessment, err := automation.Assess(context.Background(), CommandRequest{Command: "custom", Mode: CommandBackground})
	if err != nil {
		t.Fatal(err)
	}
	if !assessment.RequiresApproval || assessment.Risks["data_loss"] != 0.6 {
		t.Fatalf("assessment = %#v, want approval at threshold", assessment)
	}
}

func TestCommandAutomationRejectsMalformedAnswers(t *testing.T) {
	tests := []struct {
		name string
		resp providers.DecisionResponse
	}{
		{name: "missing", resp: safeDecisionResponse(0.1, 0.2)},
		{name: "wrong type", resp: providers.DecisionResponse{Answers: map[string]providers.DecisionAnswer{
			"environment_damage": providers.ChoiceAnswer{Choice: "no"},
			"data_loss": providers.NoulAnswer{Noul: 0},
			"remote_side_effect": providers.NoulAnswer{Noul: 0},
		}}},
		{name: "nan", resp: safeDecisionResponse(math.NaN(), 0, 0)},
		{name: "infinity", resp: safeDecisionResponse(math.Inf(1), 0, 0)},
		{name: "negative", resp: safeDecisionResponse(-0.1, 0, 0)},
		{name: "over one", resp: safeDecisionResponse(1.1, 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeDecisionProvider{fn: func(_ context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
				return tc.resp, nil
			}}
			automation, _ := NewCommandAutomation(provider, 0.6)
			if _, err := automation.Assess(context.Background(), CommandRequest{Command: "custom"}); err == nil {
				t.Fatal("Assess accepted malformed response")
			}
		})
	}
}

func TestCommandAutomationPropagatesProviderErrorAndCancellation(t *testing.T) {
	want := errors.New("provider failed")
	provider := &fakeDecisionProvider{fn: func(_ context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		return providers.DecisionResponse{}, want
	}}
	automation, _ := NewCommandAutomation(provider, 0.6)
	if _, err := automation.Assess(context.Background(), CommandRequest{Command: "custom"}); !errors.Is(err, want) {
		t.Fatalf("Assess error = %v, want %v", err, want)
	}

	provider.fn = func(ctx context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		<-ctx.Done()
		return providers.DecisionResponse{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := automation.Assess(ctx, CommandRequest{Command: "custom"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Assess error = %v, want context.Canceled", err)
	}
}

func TestNewCommandAutomationValidatesInputs(t *testing.T) {
	provider := &fakeDecisionProvider{fn: func(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error) {
		return providers.DecisionResponse{}, nil
	}}
	for _, threshold := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := NewCommandAutomation(provider, threshold); err == nil {
			t.Fatalf("accepted threshold %v", threshold)
		}
	}
	if _, err := NewCommandAutomation(nil, 0.6); err == nil {
		t.Fatal("accepted nil provider")
	}
}
