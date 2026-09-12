package planning

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type contextPlanRepository struct {
	latest *Artifact
	err    error
}

func (r *contextPlanRepository) ProposePlan(_ string, plan Artifact) (*Artifact, error) {
	r.latest = &plan
	return &plan, nil
}
func (r *contextPlanRepository) SavePlan(_ string, plan Artifact) error {
	r.latest = &plan
	return nil
}
func (r *contextPlanRepository) LoadPlan(_, _ string) (*Artifact, error) {
	return r.latest, r.err
}
func (r *contextPlanRepository) LoadLatestPlan(string) (*Artifact, error) {
	return r.latest, r.err
}

func TestApprovedPlanContextHookInjectsStableMarkdownIntoFirstUserMessage(t *testing.T) {
	plan := &Artifact{
		ID: "plan-changing-metadata", SessionID: "root", Version: 42,
		Status: ArtifactAccepted, Title: "Not injected", Markdown: "## Summary\nImplement the stable plan.",
	}
	hook := NewApprovedPlanContextHook(&contextPlanRepository{latest: plan})
	sess := session.New("root", nil, session.WithHistory(
		types.Message{Role: types.RoleAgent, Content: "summary"},
		types.Message{Role: types.RoleUser, Content: "original request", Tokens: 99},
		types.Message{Role: types.RoleAssistant, Content: "response"},
	))
	req := providers.NewRequest("", sess.GetHistory()...)

	if err := hook.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	want := "<approved_plan>\n" + plan.Markdown + "\n</approved_plan>\n\noriginal request"
	if got := req.History()[1].Content; got != want {
		t.Fatalf("injected content = %q, want %q", got, want)
	}
	if got := req.History()[1].Tokens; got != 0 {
		t.Fatalf("mutated message retained stale token count %d", got)
	}
	if strings.Contains(req.History()[1].Content, plan.ID) || strings.Contains(req.History()[1].Content, plan.Title) || strings.Contains(req.History()[1].Content, "42") {
		t.Fatalf("injection contains plan metadata: %q", req.History()[1].Content)
	}
	if got := sess.GetHistory()[1].Content; got != "original request" {
		t.Fatalf("request-local injection changed session history: %q", got)
	}

	first := req.History()[1].Content
	if err := hook.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	if got := req.History()[1].Content; got != first {
		t.Fatalf("second injection was not idempotent: %q", got)
	}
}

func TestApprovedPlanContextHookFiltersByStatus(t *testing.T) {
	for _, status := range []ArtifactStatus{ArtifactProposed, ArtifactSuperseded} {
		t.Run(string(status), func(t *testing.T) {
			repo := &contextPlanRepository{latest: &Artifact{Status: status, Markdown: "not approved"}}
			hook := NewApprovedPlanContextHook(repo)
			sess := session.New("root", nil)
			req := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "request"})
			if err := hook.BeforeModel(context.Background(), sess, req); err != nil {
				t.Fatal(err)
			}
			if got := req.History()[0].Content; got != "request" {
				t.Fatalf("status %q injected plan: %q", status, got)
			}
			if got := hook.ReservedTokens(sess); got != 0 {
				t.Fatalf("status %q reserved %d tokens", status, got)
			}
		})
	}
}

func TestApprovedPlanContextHookCreatesUserMessageWhenMissing(t *testing.T) {
	hook := NewApprovedPlanContextHook(&contextPlanRepository{latest: &Artifact{Status: ArtifactAccepted, Markdown: "the plan"}})
	sess := session.New("root", nil)
	req := providers.NewRequest("", types.Message{Role: types.RoleAgent, Content: "compacted summary"})
	if err := hook.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	history := req.History()
	if len(history) != 2 || history[0].Role != types.RoleUser || history[0].Content != "<approved_plan>\nthe plan\n</approved_plan>" {
		t.Fatalf("fallback history = %#v", history)
	}
}

func TestApprovedPlanContextHookPropagatesLoadErrorAndSkipsChildren(t *testing.T) {
	wantErr := errors.New("plan store unavailable")
	hook := NewApprovedPlanContextHook(&contextPlanRepository{err: wantErr})
	root := session.New("root", nil)
	if err := hook.BeforeModel(context.Background(), root, providers.NewRequest("")); !errors.Is(err, wantErr) {
		t.Fatalf("BeforeModel error = %v, want %v", err, wantErr)
	}

	child := root.Fork()
	req := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "delegated task"})
	if err := hook.BeforeModel(context.Background(), child, req); err != nil {
		t.Fatalf("child hook error = %v", err)
	}
	if got := req.History()[0].Content; got != "delegated task" {
		t.Fatalf("child received root plan context: %q", got)
	}

	temporary := session.New("temporary", nil, session.WithTemporary(true))
	temporaryReq := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "ephemeral task"})
	if err := hook.BeforeModel(context.Background(), temporary, temporaryReq); err != nil {
		t.Fatalf("temporary hook error = %v", err)
	}
}

func TestApprovedPlanContextHookReservesInjectedTokens(t *testing.T) {
	hook := NewApprovedPlanContextHook(&contextPlanRepository{latest: &Artifact{Status: ArtifactAccepted, Markdown: strings.Repeat("plan ", 100)}})
	sess := session.New("root", nil)
	if got := hook.ReservedTokens(sess); got <= 0 {
		t.Fatalf("ReservedTokens = %d, want positive", got)
	}
}
