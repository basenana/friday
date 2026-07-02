package proposals

import (
	"strings"
	"testing"
)

func TestValidateDAG_Acyclic(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Deps: nil},
		{ID: "T02", Deps: []string{"T01"}},
		{ID: "T03", Deps: []string{"T01", "T02"}},
	}
	if err := ValidateDAG(tasks); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateDAG_Cycle(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Deps: []string{"T03"}},
		{ID: "T02", Deps: []string{"T01"}},
		{ID: "T03", Deps: []string{"T02"}},
	}
	err := ValidateDAG(tasks)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidateDAG_UnknownDep(t *testing.T) {
	tasks := []Task{{ID: "T01", Deps: []string{"T99"}}}
	if err := ValidateDAG(tasks); err == nil {
		t.Fatal("expected unknown-dep error")
	}
}

func TestValidateDAG_Duplicate(t *testing.T) {
	tasks := []Task{{ID: "T01"}, {ID: "T01"}}
	if err := ValidateDAG(tasks); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestValidateDAG_SelfDep(t *testing.T) {
	tasks := []Task{{ID: "T01", Deps: []string{"T01"}}}
	if err := ValidateDAG(tasks); err == nil {
		t.Fatal("expected self-dep error")
	}
}

func TestComputeReadyTasks_Linear(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Status: TaskPending},
		{ID: "T02", Status: TaskPending, Deps: []string{"T01"}},
		{ID: "T03", Status: TaskPending, Deps: []string{"T02"}},
	}
	ready := ComputeReadyTasks(tasks)
	if len(ready) != 1 || ready[0].ID != "T01" {
		t.Fatalf("expected only T01 ready, got %+v", ready)
	}
}

func TestComputeReadyTasks_Diamond(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Status: TaskApproved},
		{ID: "T02", Status: TaskPending, Deps: []string{"T01"}}, // ready
		{ID: "T03", Status: TaskPending, Deps: []string{"T01"}}, // ready
		{ID: "T04", Status: TaskPending, Deps: []string{"T02", "T03"}},
	}
	ready := ComputeReadyTasks(tasks)
	if len(ready) != 2 {
		t.Fatalf("expected T02 and T03 ready, got %d", len(ready))
	}
}

func TestRecalculateAfterApproval(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Status: TaskApproved},
		{ID: "T02", Status: TaskPending, Deps: []string{"T01"}},
	}
	got := RecalculateAfterApproval(tasks, "T01")
	if len(got) != 1 || got[0] != "T02" {
		t.Fatalf("expected T02 newly ready, got %v", got)
	}
}

func TestRecalculateAfterApproval_NotReadyYet(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Status: TaskApproved},
		{ID: "T02", Status: TaskPending, Deps: []string{"T01", "T03"}},
		{ID: "T03", Status: TaskPending},
	}
	// Approving T01 should not mark T02 newly ready (T03 still pending).
	got := RecalculateAfterApproval(tasks, "T01")
	if len(got) != 0 {
		t.Fatalf("expected no newly ready, got %v", got)
	}
}

func TestAllApproved(t *testing.T) {
	if !AllApproved([]Task{{ID: "T01", Status: TaskApproved}, {ID: "T02", Status: TaskApproved}}) {
		t.Fatal("expected all approved")
	}
	if AllApproved([]Task{{ID: "T01", Status: TaskApproved}, {ID: "T02", Status: TaskPending}}) {
		t.Fatal("expected not all approved")
	}
}

func TestHasUnrecoverable(t *testing.T) {
	if !HasUnrecoverable([]Task{{ID: "T01", Status: TaskFailed}}) {
		t.Fatal("expected failed")
	}
	if HasUnrecoverable([]Task{{ID: "T01", Status: TaskPending}}) {
		t.Fatal("expected no failure")
	}
}

func TestResetStaleRunning(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Status: TaskRunning},
		{ID: "T02", Status: TaskApproved},
		{ID: "T03", Status: TaskRunning},
	}
	reset := ResetStaleRunning(tasks)
	if len(reset) != 2 {
		t.Fatalf("expected 2 reset, got %v", reset)
	}
	for i := range tasks {
		if tasks[i].Status == TaskRunning {
			t.Fatalf("T%s still running", tasks[i].ID)
		}
	}
}

func TestFindTask(t *testing.T) {
	tasks := []Task{{ID: "T01"}, {ID: "T02"}}
	if FindTask(tasks, "T02") == nil {
		t.Fatal("expected T02")
	}
	if FindTask(tasks, "T99") != nil {
		t.Fatal("expected nil for unknown")
	}
}
