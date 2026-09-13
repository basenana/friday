package actor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type planTestRuntime struct{}

func (planTestRuntime) CollaborationMode(string) collaboration.Mode { return collaboration.ModePlan }

type planTestController struct {
	mode collaboration.Mode
	err  error
}

func (c *planTestController) CollaborationMode(string) collaboration.Mode { return c.mode }
func (c *planTestController) SetMode(_ string, mode collaboration.Mode) error {
	if c.err != nil {
		return c.err
	}
	c.mode = mode
	return nil
}

type planTestRepo struct{ latest *planning.Artifact }

func (r *planTestRepo) ProposePlan(_ string, plan planning.Artifact) (*planning.Artifact, error) {
	plan.Version = 1
	if r.latest != nil {
		plan.Version = r.latest.Version + 1
	}
	copy := plan
	r.latest = &copy
	return &copy, nil
}

func (r *planTestRepo) SavePlan(_ string, plan planning.Artifact) error {
	copy := plan
	r.latest = &copy
	return nil
}

func TestRequestUserInputRoundTrip(t *testing.T) {
	a := New(nil, session.New("session", nil))
	sub := a.Subscribe()
	defer sub.Close()
	resultCh := make(chan *tools.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := makeRequestUserInputTool(a).Handler(context.Background(), &tools.Request{SessionID: "session", Arguments: map[string]any{
			"question_1": "Which scope?",
			"options_1":  []any{"Small — minimal change", "Large — complete change"},
		}})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	var formID string
	select {
	case evt := <-sub.Events():
		if evt.Name != events.CustomFormRequested {
			t.Fatalf("event = %q", evt.Name)
		}
		var body events.FormRequestedBody
		if err := events.DecodePayload(evt, &body); err != nil {
			t.Fatal(err)
		}
		formID = body.FormID
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for planning question")
	}
	if err := a.SubmitForm(formID, map[string]any{"question_1": "Other", "question_1_other": "Medium"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-resultCh:
		if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].(tools.TextContent).Text, "Medium") {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for planning answer")
	}
}

func TestRequestUserInputTimeoutEmitsCancellation(t *testing.T) {
	a := New(nil, session.New("session", nil), WithPlanning(planTestRuntime{}, &planTestRepo{}))
	sub := a.Subscribe()
	defer sub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *tools.Result, 1)
	go func() {
		result, _ := makeRequestUserInputTool(a).Handler(ctx, &tools.Request{Arguments: map[string]any{
			"question_1": "Which scope?",
			"options_1":  []any{"Small", "Large"},
		}})
		done <- result
	}()

	var formID string
	select {
	case evt := <-sub.Events():
		var body events.FormRequestedBody
		if evt.Name != events.CustomFormRequested || events.DecodePayload(evt, &body) != nil {
			t.Fatalf("unexpected event: %+v", evt)
		}
		formID = body.FormID
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for form request")
	}
	cancel()
	select {
	case result := <-done:
		if result == nil || !result.IsError {
			t.Fatalf("timeout result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not return after cancellation")
	}
	select {
	case evt := <-sub.Events():
		var body events.FormCancelledBody
		if evt.Name != events.CustomFormCancelled || events.DecodePayload(evt, &body) != nil || body.FormID != formID {
			t.Fatalf("cancellation event = %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("missing form cancellation event")
	}
}
func (r *planTestRepo) LoadPlan(_, _ string) (*planning.Artifact, error)    { return r.latest, nil }
func (r *planTestRepo) LoadLatestPlan(_ string) (*planning.Artifact, error) { return r.latest, nil }

func TestSubmitPlanPersistsArtifactAndMarksTurn(t *testing.T) {
	repo := &planTestRepo{}
	a := New(nil, session.New("session", nil), WithPlanning(planTestRuntime{}, repo))
	tool := makeSubmitPlanTool(a)
	markdown := "# Plan\n## Summary\nS\n## Implementation Changes\nC\n## Test Plan\nT\n## Assumptions\nA"
	result, err := tool.Handler(context.Background(), &tools.Request{SessionID: "session", Arguments: map[string]any{"markdown": markdown}})
	if err != nil || result.IsError {
		t.Fatalf("submit failed: result=%+v err=%v", result, err)
	}
	if repo.latest == nil || repo.latest.Version != 1 || repo.latest.Title != "Plan" || repo.latest.Markdown != markdown {
		t.Fatalf("saved plan = %+v", repo.latest)
	}
	if !a.planSubmitted.Load() {
		t.Fatal("actor was not marked plan-complete")
	}
}

func TestAssembleRequestToolsUsesShallowCardTools(t *testing.T) {
	repo := &planTestRepo{}
	a := New(nil, session.New("session", nil), WithPlanning(planTestRuntime{}, repo))
	names := map[string]bool{}
	for _, tool := range a.assembleRequestTools() {
		names[tool.Name] = true
	}
	if !names["request_user_input"] || !names["show_image"] || !names["show_mermaid"] || !names["show_html"] || !names["show_diff"] || !names["show_table"] || !names[planning.SubmitPlanToolName] {
		t.Fatalf("plan tools missing: %v", names)
	}
	for _, legacy := range []string{"emit_card", "request_form", "update_card"} {
		if names[legacy] {
			t.Fatalf("legacy tool %q exposed: %v", legacy, names)
		}
	}
}

func TestEnterPlanModePersistsAndEmitsModeChanged(t *testing.T) {
	controller := &planTestController{mode: collaboration.ModeDefault}
	a := New(nil, session.New("session", nil), WithPlanning(controller, &planTestRepo{}), WithAgentPlanEntry(true))
	sub := a.Subscribe()
	defer sub.Close()

	result, err := makeEnterPlanModeTool(a).Handler(context.Background(), &tools.Request{Arguments: map[string]any{}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("enter plan mode: result=%+v err=%v", result, err)
	}
	if controller.mode != collaboration.ModePlan {
		t.Fatalf("mode = %q", controller.mode)
	}
	select {
	case evt := <-sub.Events():
		if evt.Name != events.CustomModeChanged {
			t.Fatalf("event = %q", evt.Name)
		}
		var body events.ModeChangedBody
		if events.DecodePayload(evt, &body) != nil || body.Mode != string(collaboration.ModePlan) || body.Source != "agent" || body.Reason != "" {
			t.Fatalf("mode event = %+v", body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mode change")
	}
}

func TestEnterPlanModeFailureDoesNotEmitModeChanged(t *testing.T) {
	controller := &planTestController{mode: collaboration.ModeDefault, err: context.DeadlineExceeded}
	a := New(nil, session.New("session", nil), WithPlanning(controller, &planTestRepo{}), WithAgentPlanEntry(true))
	sub := a.Subscribe()
	defer sub.Close()
	result, err := makeEnterPlanModeTool(a).Handler(context.Background(), &tools.Request{Arguments: map[string]any{}})
	if err != nil || result == nil || !result.IsError || controller.mode != collaboration.ModeDefault {
		t.Fatalf("failed transition: result=%+v err=%v mode=%q", result, err, controller.mode)
	}
	select {
	case evt := <-sub.Events():
		t.Fatalf("unexpected event after failed transition: %+v", evt)
	default:
	}
}

func TestCollaborationHandlersRemainRegisteredAcrossModeChange(t *testing.T) {
	controller := &planTestController{mode: collaboration.ModeDefault}
	a := New(nil, session.New("session", nil), WithPlanning(controller, &planTestRepo{}), WithAgentPlanEntry(true))
	names := map[string]bool{}
	for _, tool := range a.assembleRequestTools() {
		names[tool.Name] = true
	}
	for _, name := range []string{collaboration.EnterPlanModeToolName, collaboration.RequestUserInputToolName, collaboration.SubmitPlanToolName} {
		if !names[name] {
			t.Fatalf("tool %q missing from handler registry: %v", name, names)
		}
	}
	headless := New(nil, session.New("session", nil), WithPlanning(controller, &planTestRepo{}))
	for _, tool := range headless.assembleRequestTools() {
		if tool.Name == collaboration.EnterPlanModeToolName {
			t.Fatal("headless actor exposed an approval-dependent plan entry tool")
		}
	}
}

func TestPlanOnlyToolsRejectDefaultMode(t *testing.T) {
	controller := &planTestController{mode: collaboration.ModeDefault}
	a := New(nil, session.New("session", nil), WithPlanning(controller, &planTestRepo{}))
	markdown := "# Plan\n## Summary\nS\n## Implementation Changes\nC\n## Test Plan\nT\n## Assumptions\nA"
	result, err := makeSubmitPlanTool(a).Handler(context.Background(), &tools.Request{Arguments: map[string]any{"markdown": markdown}})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("default-mode submit result=%+v err=%v", result, err)
	}
}

func TestSubmitPlanRequiresMarkdownHeadings(t *testing.T) {
	repo := &planTestRepo{}
	a := New(nil, session.New("session", nil), WithPlanning(planTestRuntime{}, repo))
	result, err := makeSubmitPlanTool(a).Handler(context.Background(), &tools.Request{
		SessionID: "session",
		Arguments: map[string]any{
			"markdown": "This sentence mentions summary, implementation changes, test plan, and assumptions.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || repo.latest != nil {
		t.Fatalf("invalid plan accepted: result=%+v plan=%+v", result, repo.latest)
	}
}
