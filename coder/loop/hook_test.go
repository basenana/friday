package loop

import (
	"context"
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
	finish := call("finish_loop", nil)
	if finish.IsError {
		t.Fatalf("finish_loop = %s", tools.Res2Str(finish))
	}
	if state, err := readState(ctx, root); err != nil || state != StateFinishing {
		t.Fatalf("state after finish = %q, err = %v", state, err)
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
	childReq := providers.NewRequest("hello")
	before := childReq.SystemPrompt()
	if err := hook.BeforeModel(ctx, root.Fork(), childReq); err != nil {
		t.Fatal(err)
	}
	if childReq.SystemPrompt() != before {
		t.Fatalf("child system prompt = %q", childReq.SystemPrompt())
	}
}
