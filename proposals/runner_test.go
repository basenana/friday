package proposals

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeStrategy is a controllable ExecutionStrategy for runner tests.
type fakeStrategy struct {
	mu sync.Mutex

	planTasks []Task
	planErr   error

	executeResult map[string]string // taskID -> result
	executeErr    map[string]error

	reviewDecision map[string]Decision
	reviewErr      map[string]error

	executeCalls []string
	reviewCalls  []string
}

func (f *fakeStrategy) Plan(ctx context.Context, p *Proposal) ([]Task, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}
	out := make([]Task, len(f.planTasks))
	copy(out, f.planTasks)
	return out, nil
}

func (f *fakeStrategy) Execute(ctx context.Context, p *Proposal, t *Task) (string, error) {
	f.mu.Lock()
	f.executeCalls = append(f.executeCalls, t.ID)
	f.mu.Unlock()
	if err, ok := f.executeErr[t.ID]; ok {
		return "", err
	}
	if r, ok := f.executeResult[t.ID]; ok {
		return r, nil
	}
	return "default-result-" + t.ID, nil
}

func (f *fakeStrategy) Review(ctx context.Context, p *Proposal, t *Task, result string) (Decision, error) {
	f.mu.Lock()
	f.reviewCalls = append(f.reviewCalls, t.ID)
	f.mu.Unlock()
	if err, ok := f.reviewErr[t.ID]; ok {
		return Decision{}, err
	}
	if d, ok := f.reviewDecision[t.ID]; ok {
		return d, nil
	}
	return Decision{Status: "approved", Comment: "ok"}, nil
}

func newRunnerTestEnv(t *testing.T, tasks []Task) (*Loader, *Proposal) {
	t.Helper()
	dir := t.TempDir()
	loader := NewLoader(dir)
	p := &Proposal{ID: "test-prop", Title: "Test", Status: ProposalDraft, Sessions: map[string]string{}}
	if err := loader.InitProposal(p, "# Test proposal\n\nDesign doc body."); err != nil {
		t.Fatalf("InitProposal: %v", err)
	}
	// Pre-seed task docs so loaders don't need a planning call.
	for _, tk := range tasks {
		_ = loader.SaveTaskDoc(p.ID, tk.ID, "# "+tk.Title)
	}
	return loader, p
}

func TestRunner_LinearDAG_HappyPath(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Title: "first"},
		{ID: "T02", Title: "second", Deps: []string{"T01"}},
		{ID: "T03", Title: "third", Deps: []string{"T02"}},
	}
	loader, p := newRunnerTestEnv(t, tasks)

	strategy := &fakeStrategy{planTasks: tasks}
	runner := NewRunner(p, loader, strategy)

	summary, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify execution order is topological.
	strategy.mu.Lock()
	defer strategy.mu.Unlock()
	want := []string{"T01", "T02", "T03"}
	if len(strategy.executeCalls) != 3 {
		t.Fatalf("expected 3 executions, got %d: %v", len(strategy.executeCalls), strategy.executeCalls)
	}
	for i, id := range want {
		if strategy.executeCalls[i] != id {
			t.Errorf("execute[%d] = %s, want %s (calls=%v)", i, strategy.executeCalls[i], id, strategy.executeCalls)
		}
	}
	if p.Status != ProposalCompleted {
		t.Errorf("status = %s, want completed", p.Status)
	}
	if summary == "" {
		t.Error("expected non-empty summary")
	}

	// Verify all tasks persisted as approved.
	persisted, err := loader.LoadTasks(p.ID)
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	for _, tk := range persisted {
		if tk.Status != TaskApproved {
			t.Errorf("task %s status = %s, want approved", tk.ID, tk.Status)
		}
	}
}

func TestRunner_ParallelDAG(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Title: "root1"},
		{ID: "T02", Title: "root2"},
		{ID: "T03", Title: "join", Deps: []string{"T01", "T02"}},
	}
	loader, p := newRunnerTestEnv(t, tasks)
	strategy := &fakeStrategy{planTasks: tasks}
	runner := NewRunner(p, loader, strategy)

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	strategy.mu.Lock()
	defer strategy.mu.Unlock()
	// T03 must execute AFTER both T01 and T02.
	if len(strategy.executeCalls) != 3 {
		t.Fatalf("expected 3 executions, got %v", strategy.executeCalls)
	}
	if strategy.executeCalls[2] != "T03" {
		t.Errorf("expected T03 last, got order %v", strategy.executeCalls)
	}
}

func TestRunner_RejectionRetry(t *testing.T) {
	tasks := []Task{{ID: "T01", Title: "only"}}
	loader, p := newRunnerTestEnv(t, tasks)
	strategy := &fakeStrategy{
		planTasks:      tasks,
		reviewDecision: map[string]Decision{
			// First review rejects, second approves (track via call count).
		},
	}
	// Use a stateful review that rejects first then approves.
	calls := 0
	strategy.reviewDecision = nil
	wrapped := &rejectOnceThenApprove{inner: strategy, calls: &calls}
	runner := NewRunner(p, loader, wrapped)

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	strategy.mu.Lock()
	defer strategy.mu.Unlock()
	if len(strategy.executeCalls) != 2 {
		t.Fatalf("expected 2 executions (retry), got %d: %v", len(strategy.executeCalls), strategy.executeCalls)
	}
	if p.Status != ProposalCompleted {
		t.Errorf("status = %s, want completed", p.Status)
	}
}

type rejectOnceThenApprove struct {
	inner *fakeStrategy
	calls *int
}

func (r *rejectOnceThenApprove) Plan(ctx context.Context, p *Proposal) ([]Task, error) {
	return r.inner.Plan(ctx, p)
}
func (r *rejectOnceThenApprove) Execute(ctx context.Context, p *Proposal, t *Task) (string, error) {
	return r.inner.Execute(ctx, p, t)
}
func (r *rejectOnceThenApprove) Review(ctx context.Context, p *Proposal, t *Task, result string) (Decision, error) {
	*r.calls++
	if *r.calls == 1 {
		return Decision{Status: "rejected", Comment: "redo"}, nil
	}
	return Decision{Status: "approved", Comment: "ok"}, nil
}

func TestRunner_ExecuteFailure(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Title: "fails"},
		{ID: "T02", Title: "never-runs", Deps: []string{"T01"}},
	}
	loader, p := newRunnerTestEnv(t, tasks)
	strategy := &fakeStrategy{
		planTasks:  tasks,
		executeErr: map[string]error{"T01": fmt.Errorf("boom")},
	}
	runner := NewRunner(p, loader, strategy)

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	strategy.mu.Lock()
	defer strategy.mu.Unlock()
	if len(strategy.executeCalls) != 1 || strategy.executeCalls[0] != "T01" {
		t.Errorf("expected only T01 executed, got %v", strategy.executeCalls)
	}
	if p.Status != ProposalFailed {
		t.Errorf("status = %s, want failed", p.Status)
	}
}

func TestRunner_RestartsStaleRunning(t *testing.T) {
	tasks := []Task{{ID: "T01", Title: "resume-me"}}
	loader, p := newRunnerTestEnv(t, tasks)

	// Pre-seed an interrupted task: T01 stuck in running.
	if err := loader.SaveTasks(p.ID, []Task{{ID: "T01", Title: "resume-me", Status: TaskRunning}}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	// Also persist the proposal so the loader sees it as already existing.
	_ = os.WriteFile(filepath.Join(loader.ProposalDir(p.ID), "proposal.json"), []byte("{}"), 0644)

	strategy := &fakeStrategy{} // Plan won't be called because tasks already exist.
	runner := NewRunner(p, loader, strategy)

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.Status != ProposalCompleted {
		t.Errorf("status = %s, want completed", p.Status)
	}
}

func TestRunner_ResumesPendingReview(t *testing.T) {
	tasks := []Task{{ID: "T01", Title: "review-me"}}
	loader, p := newRunnerTestEnv(t, tasks)

	if err := loader.SaveTaskResult(p.ID, "T01", "finished work"); err != nil {
		t.Fatalf("SaveTaskResult: %v", err)
	}
	if err := loader.SaveTasks(p.ID, []Task{{ID: "T01", Title: "review-me", Status: TaskPendingReview, ResultSummary: "finished work"}}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}

	strategy := &fakeStrategy{
		reviewDecision: map[string]Decision{
			"T01": {Status: "approved", Comment: "looks good"},
		},
	}
	runner := NewRunner(p, loader, strategy)

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	strategy.mu.Lock()
	defer strategy.mu.Unlock()
	if len(strategy.executeCalls) != 0 {
		t.Fatalf("expected no execute retry, got %v", strategy.executeCalls)
	}
	if len(strategy.reviewCalls) != 1 || strategy.reviewCalls[0] != "T01" {
		t.Fatalf("expected one resumed review for T01, got %v", strategy.reviewCalls)
	}
	if p.Status != ProposalCompleted {
		t.Errorf("status = %s, want completed", p.Status)
	}
}

func TestRunner_InvalidPlanCycle(t *testing.T) {
	tasks := []Task{
		{ID: "T01", Deps: []string{"T02"}},
		{ID: "T02", Deps: []string{"T01"}},
	}
	loader, p := newRunnerTestEnv(t, tasks)
	strategy := &fakeStrategy{planTasks: tasks}
	runner := NewRunner(p, loader, strategy)

	_, err := runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if p.Status != ProposalFailed {
		t.Errorf("status = %s, want failed", p.Status)
	}
}
