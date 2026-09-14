package loop

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

func TestWorkingNoteToolsAndRootPermission(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateActive); err != nil {
		t.Fatal(err)
	}
	hook := NewHook()
	req := &api.Request{}
	if err := hook.BeforeAgent(ctx, root, req); err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"working_note_read", "working_note_append", "working_note_edit", "working_note_replace", "finish_loop"}
	var gotNames []string
	for _, tool := range req.Tools {
		gotNames = append(gotNames, tool.Name)
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("tools = %v, want %v", gotNames, wantNames)
	}

	byName := make(map[string]*tools.Tool)
	for _, tool := range req.Tools {
		byName[tool.Name] = tool
	}
	call := func(name string, args map[string]any) *tools.Result {
		result, err := byName[name].Handler(ctx, &tools.Request{Arguments: args, SessionID: root.ID, SessionRecords: root})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	call("working_note_replace", map[string]any{"content": "alpha alpha"})
	call("working_note_edit", map[string]any{"old_text": "alpha", "new_text": "beta"})
	call("working_note_append", map[string]any{"content": "tail"})
	result := call("working_note_read", nil)
	if text := tools.Res2Str(result); !strings.Contains(text, "beta alpha") || !strings.Contains(text, "tail") {
		t.Fatalf("working note = %q", text)
	}
	if err := writePhase(ctx, root, phaseUpdate); err != nil {
		t.Fatal(err)
	}
	finish := call("finish_loop", nil)
	if finish.IsError {
		t.Fatalf("finish_loop = %s", tools.Res2Str(finish))
	}
	if state, err := readState(ctx, root); err != nil || state != StateCompleted {
		t.Fatalf("state after finish = %q, err = %v", state, err)
	}
	if err := writeState(ctx, root, StateActive); err != nil {
		t.Fatal(err)
	}

	child := root.Fork()
	childReq := &api.Request{}
	if err := hook.BeforeAgent(ctx, child, childReq); err != nil {
		t.Fatal(err)
	}
	if len(childReq.Tools) != len(req.Tools) {
		t.Fatalf("child tools = %d, root tools = %d", len(childReq.Tools), len(req.Tools))
	}
	for i := range req.Tools {
		if !reflect.DeepEqual(req.Tools[i].JsonSchema(), childReq.Tools[i].JsonSchema()) || req.Tools[i].Name != childReq.Tools[i].Name {
			t.Fatalf("tool %d differs between root and child", i)
		}
	}
	denied, err := childReq.Tools[0].Handler(ctx, &tools.Request{SessionID: child.ID, SessionRecords: child})
	if err != nil || !denied.IsError || !strings.Contains(tools.Res2Str(denied), "root loop session") {
		t.Fatalf("child result = %#v, err = %v", denied, err)
	}
}

func TestHookInjectsStablePromptOnlyForRoot(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateActive); err != nil {
		t.Fatal(err)
	}
	hook := NewHook()
	req := providers.NewRequest("hello")
	if err := hook.BeforeModel(ctx, root, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.SystemPrompt(), "call working_note_read immediately") {
		t.Fatalf("missing working note fallback: %q", req.SystemPrompt())
	}
	if !strings.Contains(req.SystemPrompt(), "finish_loop is phase restricted") ||
		!strings.Contains(req.SystemPrompt(), "No useful or actionable work remains") {
		t.Fatalf("missing complete-plan finish constraint: %q", req.SystemPrompt())
	}
	childReq := providers.NewRequest("hello")
	before := childReq.SystemPrompt()
	if err := hook.BeforeModel(ctx, root.Fork(), childReq); err != nil {
		t.Fatal(err)
	}
	if childReq.SystemPrompt() != before {
		t.Fatalf("child system prompt = %q", childReq.SystemPrompt())
	}
}

func TestFinishLoopDescriptionRequiresAllWorkingNotePlans(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateActive); err != nil {
		t.Fatal(err)
	}
	req := &api.Request{}
	if err := NewHook().BeforeAgent(ctx, root, req); err != nil {
		t.Fatal(err)
	}
	for _, tool := range req.Tools {
		if tool.Name != "finish_loop" {
			continue
		}
		description := tool.GetDescription()
		if !strings.Contains(description, "only during the final update phase") ||
			!strings.Contains(description, "Completing only the current selected task") ||
			!strings.Contains(description, "no unfinished, uncertain, unverified, useful, or actionable work remaining") {
			t.Fatalf("finish_loop description = %q", description)
		}
		return
	}
	t.Fatal("finish_loop tool not found")
}

func TestFinishLoopAcceptsSuspendedState(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateSuspended); err != nil {
		t.Fatal(err)
	}
	if err := writePhase(ctx, root, phaseUpdate); err != nil {
		t.Fatal(err)
	}

	result, err := finishLoop(ctx, &tools.Request{SessionID: root.ID, SessionRecords: root})
	if err != nil || result.IsError {
		t.Fatalf("finishLoop() = %#v, %v", result, err)
	}
	state, err := readState(ctx, root)
	if err != nil || state != StateCompleted {
		t.Fatalf("state = %q, %v; want completed", state, err)
	}
}

func TestFinishLoopGuidesWithoutFailureOutsideUpdatePhase(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase phase
		raw   string
	}{
		{name: "bootstrap", phase: phaseBootstrap},
		{name: "select", phase: phaseSelect},
		{name: "develop", phase: phaseDevelop},
		{name: "review", phase: phaseReview},
		{name: "recovery", phase: phaseRecovery},
		{name: "missing"},
		{name: "unknown", raw: "future-phase"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := session.New("root", nil)
			if err := writeState(ctx, root, StateActive); err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.phase != phaseUnknown:
				if err := writePhase(ctx, root, tc.phase); err != nil {
					t.Fatal(err)
				}
			case tc.raw != "":
				if err := root.UpdateRecord(ctx, phaseNamespace, func([]byte) ([]byte, error) {
					return []byte(tc.raw), nil
				}); err != nil {
					t.Fatal(err)
				}
			}

			result, err := finishLoop(ctx, &tools.Request{SessionID: root.ID, SessionRecords: root})
			if err != nil || result.IsError {
				t.Fatalf("finishLoop() = %#v, %v; want successful guidance", result, err)
			}
			text := tools.Res2Str(result)
			if !strings.Contains(text, "may only complete the Loop during the final update phase") ||
				!strings.Contains(text, "Do not retry finish_loop in this turn") ||
				!strings.Contains(text, "update the Working Note") {
				t.Fatalf("guidance = %q", text)
			}
			if state, err := readState(ctx, root); err != nil || state != StateActive {
				t.Fatalf("state after denied finish = %q, %v; want active", state, err)
			}
		})
	}
}

func TestFinishLoopGuidancePreservesSuspendedState(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateSuspended); err != nil {
		t.Fatal(err)
	}
	if err := writePhase(ctx, root, phaseRecovery); err != nil {
		t.Fatal(err)
	}
	result, err := finishLoop(ctx, &tools.Request{SessionID: root.ID, SessionRecords: root})
	if err != nil || result.IsError {
		t.Fatalf("finishLoop() = %#v, %v; want successful guidance", result, err)
	}
	if state, err := readState(ctx, root); err != nil || state != StateSuspended {
		t.Fatalf("state after denied finish = %q, %v; want suspended", state, err)
	}
}

type phaseReadFailingRecords struct{ err error }

func (r phaseReadFailingRecords) ReadRecord(context.Context, string) ([]byte, error) {
	return nil, r.err
}

func (phaseReadFailingRecords) UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error {
	return nil
}

func TestFinishLoopReportsPhaseReadFailure(t *testing.T) {
	want := errors.New("phase store unavailable")
	result, err := finishLoop(context.Background(), &tools.Request{
		SessionID:      "root",
		SessionRecords: phaseReadFailingRecords{err: want},
	})
	if err != nil || !result.IsError || !strings.Contains(tools.Res2Str(result), want.Error()) {
		t.Fatalf("finishLoop() = %#v, %v", result, err)
	}
}

func TestHookKeepsLoopCapabilitiesWhileSuspended(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateSuspended); err != nil {
		t.Fatal(err)
	}
	hook := NewHook()
	agentReq := &api.Request{}
	if err := hook.BeforeAgent(ctx, root, agentReq); err != nil {
		t.Fatal(err)
	}
	if len(agentReq.Tools) == 0 {
		t.Fatal("suspended Loop did not expose Working Note tools")
	}
	modelReq := providers.NewRequest("hello")
	if err := hook.BeforeModel(ctx, root, modelReq); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(modelReq.SystemPrompt(), "autonomous Ralph Loop") {
		t.Fatalf("suspended Loop prompt = %q", modelReq.SystemPrompt())
	}
}
