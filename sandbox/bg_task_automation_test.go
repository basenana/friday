package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
)

func TestBackgroundTaskAutomationAuthorizesBeforeStart(t *testing.T) {
	exec := approvalTestExecutor(nil, nil)
	provider := decisionProviderWith(0.1, 0.2, 0.3)
	automation, _ := NewCommandAutomation(provider, 0.6)
	tm := NewTaskManager(exec, NewCommandGate(exec, nil, automation))

	task, err := tm.Start(context.Background(), CommandRequest{Command: "printf background-ok", Workdir: t.TempDir(), Mode: CommandBackground, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("background task did not finish")
	case <-tm.tasks[task.ID].done:
	}
	got, _ := tm.Get(task.ID)
	if got.Stdout != "background-ok" {
		t.Fatalf("stdout = %q, want background-ok", got.Stdout)
	}
	if decision, _ := exec.Permission().Check("printf anything"); decision != Deny {
		t.Fatal("automation should not grant the executable")
	}
}

func TestBackgroundTaskAutomationFailureHeadlessDoesNotStart(t *testing.T) {
	exec := approvalTestExecutor(nil, nil)
	provider := &fakeDecisionProvider{fn: func(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error) {
		return providers.DecisionResponse{}, errors.New("offline")
	}}
	automation, _ := NewCommandAutomation(provider, 0.6)
	tm := NewTaskManager(exec, NewCommandGate(exec, nil, automation))
	if _, err := tm.Start(context.Background(), CommandRequest{Command: "printf no", Workdir: t.TempDir(), Mode: CommandBackground}); !IsDenied(err) {
		t.Fatalf("error = %v, want permission denial", err)
	}
	if tasks := tm.List(""); len(tasks) != 0 {
		t.Fatalf("tasks = %#v, want none", tasks)
	}
}

func TestBackgroundTaskCancelledBeforeAuthorizationDoesNotStart(t *testing.T) {
	exec := approvalTestExecutor(nil, nil)
	provider := &fakeDecisionProvider{fn: func(ctx context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
		<-ctx.Done()
		return providers.DecisionResponse{}, ctx.Err()
	}}
	automation, _ := NewCommandAutomation(provider, 0.6)
	tm := NewTaskManager(exec, NewCommandGate(exec, nil, automation))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tm.Start(ctx, CommandRequest{Command: "printf no", Workdir: t.TempDir(), Mode: CommandBackground}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if tasks := tm.List(""); len(tasks) != 0 {
		t.Fatalf("tasks = %#v, want none", tasks)
	}
}
