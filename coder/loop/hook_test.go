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

func TestLoopToolsArePhaseSpecificAndRootOnly(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		phase      phase
		finishTool string
	}{
		{name: "develop", phase: phaseDevelop},
		{name: "update", phase: phaseUpdate, finishTool: "finish_devloop"},
		{name: "review", phase: phaseReview, finishTool: "finish_reviewloop"},
		{name: "revise", phase: phaseRevise},
		{name: "development recovery", phase: phaseRecoveryDevelop},
		{name: "review recovery", phase: phaseRecoveryReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := session.New("root", nil)
			if err := writeState(ctx, root, StateActive); err != nil {
				t.Fatal(err)
			}
			if err := writePhase(ctx, root, tc.phase); err != nil {
				t.Fatal(err)
			}

			rootReq := &api.Request{}
			if err := NewHook().BeforeAgent(ctx, root, rootReq); err != nil {
				t.Fatal(err)
			}
			wantNames := []string{"working_note_read", "working_note_append", "working_note_edit", "working_note_replace"}
			if tc.finishTool != "" {
				wantNames = append(wantNames, tc.finishTool)
			}
			if got := toolNames(rootReq.Tools); !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("tools = %v, want %v", got, wantNames)
			}

			childReq := &api.Request{}
			if err := NewHook().BeforeAgent(ctx, root.Fork(), childReq); err != nil {
				t.Fatal(err)
			}
			if got := toolNames(childReq.Tools); !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("child tools = %v, want %v", got, wantNames)
			}
			denied, err := childReq.Tools[0].Handler(ctx, &tools.Request{SessionID: "child", SessionRecords: root.Fork()})
			if err != nil || !denied.IsError || !strings.Contains(tools.Res2Str(denied), "root loop session") {
				t.Fatalf("child result = %#v, err = %v", denied, err)
			}
		})
	}
}

func TestWorkingNoteTools(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateActive); err != nil {
		t.Fatal(err)
	}
	if err := writePhase(ctx, root, phaseDevelop); err != nil {
		t.Fatal(err)
	}
	req := &api.Request{}
	if err := NewHook().BeforeAgent(ctx, root, req); err != nil {
		t.Fatal(err)
	}
	byName := toolsByName(req.Tools)
	callTool(t, byName["working_note_replace"], root, map[string]any{"content": "alpha alpha"})
	callTool(t, byName["working_note_edit"], root, map[string]any{"old_text": "alpha", "new_text": "beta"})
	callTool(t, byName["working_note_append"], root, map[string]any{"content": "tail"})
	result := callTool(t, byName["working_note_read"], root, nil)
	if text := tools.Res2Str(result); !strings.Contains(text, "beta alpha") || !strings.Contains(text, "tail") {
		t.Fatalf("working note = %q", text)
	}
}

func TestHookInjectsTwoCyclePromptOnlyForRoot(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	if err := writeState(ctx, root, StateActive); err != nil {
		t.Fatal(err)
	}
	req := providers.NewRequest("hello")
	if err := NewHook().BeforeModel(ctx, root, req); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"one continuous Loop", "develop -> update", "review -> revise", "finish_devloop", "Only finish_reviewloop completes"} {
		if !strings.Contains(req.SystemPrompt(), want) {
			t.Fatalf("system prompt missing %q: %q", want, req.SystemPrompt())
		}
	}
	childReq := providers.NewRequest("hello")
	before := childReq.SystemPrompt()
	if err := NewHook().BeforeModel(ctx, root.Fork(), childReq); err != nil {
		t.Fatal(err)
	}
	if childReq.SystemPrompt() != before {
		t.Fatalf("child system prompt = %q", childReq.SystemPrompt())
	}
}

func TestFinishDevLoopHandsOffWithoutCompleting(t *testing.T) {
	for _, state := range []State{StateActive, StateSuspended} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			root := session.New("root", nil)
			if err := writeState(ctx, root, state); err != nil {
				t.Fatal(err)
			}
			if err := writePhase(ctx, root, phaseUpdate); err != nil {
				t.Fatal(err)
			}
			result, err := finishDevLoop(ctx, &tools.Request{SessionID: root.ID, SessionRecords: root})
			if err != nil || result.IsError {
				t.Fatalf("finishDevLoop() = %#v, %v", result, err)
			}
			if got, err := readState(ctx, root); err != nil || got != state {
				t.Fatalf("state = %q, %v; want %q", got, err, state)
			}
			if got, err := readPhase(ctx, root); err != nil || got != phaseReview {
				t.Fatalf("phase = %q, %v; want review", got, err)
			}
			if text := tools.Res2Str(result); !strings.Contains(text, "remains active") || !strings.Contains(text, "Final review is next") {
				t.Fatalf("result = %q", text)
			}
		})
	}
}

func TestFinishReviewLoopCompletes(t *testing.T) {
	for _, state := range []State{StateActive, StateSuspended} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			root := session.New("root", nil)
			if err := writeState(ctx, root, state); err != nil {
				t.Fatal(err)
			}
			if err := writePhase(ctx, root, phaseReview); err != nil {
				t.Fatal(err)
			}
			result, err := finishReviewLoop(ctx, &tools.Request{SessionID: root.ID, SessionRecords: root})
			if err != nil || result.IsError {
				t.Fatalf("finishReviewLoop() = %#v, %v", result, err)
			}
			if got, err := readState(ctx, root); err != nil || got != StateCompleted {
				t.Fatalf("state = %q, %v; want completed", got, err)
			}
		})
	}
}

func TestFinishToolsGuideWithoutMutationOutsideTheirPhases(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   tools.ToolHandlerFunc
		at   phase
		want string
	}{
		{name: "development handoff", fn: finishDevLoop, at: phaseDevelop, want: "only available during update"},
		{name: "review completion", fn: finishReviewLoop, at: phaseRevise, want: "only available during review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := session.New("root", nil)
			if err := writeState(ctx, root, StateActive); err != nil {
				t.Fatal(err)
			}
			if err := writePhase(ctx, root, tc.at); err != nil {
				t.Fatal(err)
			}
			result, err := tc.fn(ctx, &tools.Request{SessionID: root.ID, SessionRecords: root})
			if err != nil || result.IsError || !strings.Contains(tools.Res2Str(result), tc.want) {
				t.Fatalf("result = %#v, %v", result, err)
			}
			if got, _ := readState(ctx, root); got != StateActive {
				t.Fatalf("state = %q; want active", got)
			}
			if got, _ := readPhase(ctx, root); got != tc.at {
				t.Fatalf("phase = %q; want %q", got, tc.at)
			}
		})
	}
}

type phaseReadFailingRecords struct{ err error }

func (r phaseReadFailingRecords) ReadRecord(context.Context, string) ([]byte, error) {
	return nil, r.err
}

func (phaseReadFailingRecords) UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error {
	return nil
}

func TestFinishToolsReportPhaseReadFailure(t *testing.T) {
	want := errors.New("phase store unavailable")
	for _, fn := range []tools.ToolHandlerFunc{finishDevLoop, finishReviewLoop} {
		result, err := fn(context.Background(), &tools.Request{SessionID: "root", SessionRecords: phaseReadFailingRecords{err: want}})
		if err != nil || !result.IsError || !strings.Contains(tools.Res2Str(result), want.Error()) {
			t.Fatalf("finish tool = %#v, %v", result, err)
		}
	}
}

func TestFinishToolDescriptionsDefineDistinctBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		phase phase
		name  string
		wants []string
	}{
		{phase: phaseUpdate, name: "finish_devloop", wants: []string{"without completing", "no known development work", "stays active"}},
		{phase: phaseReview, name: "finish_reviewloop", wants: []string{"after independent final review", "all blockers", "Blocking Findings"}},
	} {
		root := session.New("root", nil)
		_ = writeState(ctx, root, StateActive)
		_ = writePhase(ctx, root, tc.phase)
		req := &api.Request{}
		if err := NewHook().BeforeAgent(ctx, root, req); err != nil {
			t.Fatal(err)
		}
		description := toolsByName(req.Tools)[tc.name].GetDescription()
		for _, want := range tc.wants {
			if !strings.Contains(description, want) {
				t.Fatalf("%s description missing %q: %q", tc.name, want, description)
			}
		}
	}
}

func TestHookKeepsLoopCapabilitiesWhileSuspended(t *testing.T) {
	ctx := context.Background()
	root := session.New("root", nil)
	_ = writeState(ctx, root, StateSuspended)
	_ = writePhase(ctx, root, phaseUpdate)
	agentReq := &api.Request{}
	if err := NewHook().BeforeAgent(ctx, root, agentReq); err != nil {
		t.Fatal(err)
	}
	if _, ok := toolsByName(agentReq.Tools)["finish_devloop"]; !ok {
		t.Fatal("suspended update did not expose finish_devloop")
	}
	modelReq := providers.NewRequest("hello")
	if err := NewHook().BeforeModel(ctx, root, modelReq); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(modelReq.SystemPrompt(), "autonomous Ralph Loop") {
		t.Fatalf("suspended Loop prompt = %q", modelReq.SystemPrompt())
	}
}

func toolNames(list []*tools.Tool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Name)
	}
	return names
}

func toolsByName(list []*tools.Tool) map[string]*tools.Tool {
	result := make(map[string]*tools.Tool, len(list))
	for _, tool := range list {
		result[tool.Name] = tool
	}
	return result
}

func callTool(t *testing.T, tool *tools.Tool, sess *session.Session, args map[string]any) *tools.Result {
	t.Helper()
	result, err := tool.Handler(context.Background(), &tools.Request{Arguments: args, SessionID: sess.ID, SessionRecords: sess})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
