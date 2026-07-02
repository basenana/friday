package proposals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/teams"
)

type structuredTestClient struct {
	responses      []any
	calls          int
	capturedSystem string
}

func (c *structuredTestClient) Completion(_ context.Context, _ providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	close(resp.Stream)
	close(resp.Err)
	return resp
}

func (c *structuredTestClient) CompletionNonStreaming(_ context.Context, _ providers.Request) (string, error) {
	return "", nil
}

func (c *structuredTestClient) StructuredPredict(_ context.Context, req providers.Request, model any) error {
	c.capturedSystem = req.SystemPrompt()
	if c.calls >= len(c.responses) {
		return fmt.Errorf("unexpected structured predict call %d", c.calls)
	}
	data, err := json.Marshal(c.responses[c.calls])
	if err != nil {
		return err
	}
	c.calls++
	return json.Unmarshal(data, model)
}

type appendSystemPromptHook struct {
	suffix string
}

func (h *appendSystemPromptHook) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	req.AppendSystemPrompt(h.suffix)
	return nil
}

func TestStructuredPredictWithSessionRunsHooksAndPersistsHistory(t *testing.T) {
	client := &structuredTestClient{
		responses: []any{
			Decision{Status: "approved", Comment: "ok"},
		},
	}
	sess := session.New("sess-1", client)
	sess.RegisterHook(&appendSystemPromptHook{suffix: "hook prompt"})

	var got Decision
	if err := structuredPredictWithSession(context.Background(), client, sess, "base prompt", "review prompt", &got); err != nil {
		t.Fatalf("structuredPredictWithSession: %v", err)
	}

	if got.Status != "approved" {
		t.Fatalf("status = %q, want approved", got.Status)
	}
	if !strings.Contains(client.capturedSystem, "hook prompt") {
		t.Fatalf("expected hook system prompt, got %q", client.capturedSystem)
	}

	history := sess.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(history))
	}
	if history[0].Role != types.RoleUser || history[0].Content != "review prompt" {
		t.Fatalf("unexpected user history entry: %+v", history[0])
	}
	if history[1].Role != types.RoleAssistant || !strings.Contains(history[1].Content, `"status":"approved"`) {
		t.Fatalf("unexpected assistant history entry: %+v", history[1])
	}
}

func TestTeamStrategyLeaderSessionIsProposalScoped(t *testing.T) {
	client := &structuredTestClient{
		responses: []any{
			[]Task{{ID: "T01", Title: "plan task"}},
			Decision{Status: "approved", Comment: "ok"},
		},
	}
	sessionsByKey := map[string]*session.Session{}
	var calls []string
	sessionFactory := func(proposalID, assignee string) (*session.Session, error) {
		key := proposalID + ":" + assignee
		calls = append(calls, key)
		if sess, ok := sessionsByKey[key]; ok {
			return sess, nil
		}
		sess := session.New(key, client)
		sessionsByKey[key] = sess
		return sess, nil
	}

	strategy, err := NewTeamStrategy(
		client,
		func(string, []*tools.Tool) agents.Agent { return nil },
		&teams.Team{Name: "alpha"},
		[]teams.Member{{Name: "lead", Role: teams.RoleLeader}},
		nil,
		"",
		nil,
		sessionFactory,
	)
	if err != nil {
		t.Fatalf("NewTeamStrategy: %v", err)
	}

	loader := NewLoader(t.TempDir())
	proposal := &Proposal{ID: "proposal-123", Title: "Scoped leader", Sessions: map[string]string{}}
	if err := loader.InitProposal(proposal, "# Scoped leader"); err != nil {
		t.Fatalf("InitProposal: %v", err)
	}
	if err := loader.SaveTaskDoc(proposal.ID, "T01", "# T01\n\nReview me."); err != nil {
		t.Fatalf("SaveTaskDoc: %v", err)
	}
	SetGlobalLoader(loader)
	t.Cleanup(func() { SetGlobalLoader(nil) })

	if _, err := strategy.Plan(context.Background(), proposal); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := strategy.Review(context.Background(), proposal, &Task{ID: "T01", Title: "plan task"}, "done"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	wantKey := "proposal-123:lead"
	if len(calls) != 2 || calls[0] != wantKey || calls[1] != wantKey {
		t.Fatalf("unexpected sessionFactory calls: %v", calls)
	}
	if len(sessionsByKey[wantKey].GetHistory()) != 4 {
		t.Fatalf("expected plan+review history on proposal-scoped leader session, got %d messages", len(sessionsByKey[wantKey].GetHistory()))
	}
}
