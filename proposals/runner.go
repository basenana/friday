package proposals

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MaxRetries caps how many times a rejected task is retried before it fails.
const MaxRetries = 3

// Runner is the Go-code-driven DAG scheduler. It owns the loop that picks
// ready tasks, asks the strategy to execute + review each one, and persists
// every state transition to disk. The Runner is what "takes over" the main
// control loop when proposal_run is invoked.
type Runner struct {
	proposal *Proposal
	loader   *Loader
	strategy ExecutionStrategy
}

// NewRunner constructs a Runner. SetGlobalLoader(loader) is called so the
// strategy implementations can read PROPOSAL.md / task briefs without
// threading the loader through every method.
func NewRunner(proposal *Proposal, loader *Loader, strategy ExecutionStrategy) *Runner {
	SetGlobalLoader(loader)
	return &Runner{proposal: proposal, loader: loader, strategy: strategy}
}

// Run executes the Plan → Execute/Review loop → Complete phases.
// It blocks until the DAG completes or fails. Returns a human-readable summary.
func (r *Runner) Run(ctx context.Context) (string, error) {
	// Load any existing tasks (resume case) or plan fresh.
	tasks, err := r.loader.LoadTasks(r.proposal.ID)
	if err != nil && !isNotExist(err) {
		return "", fmt.Errorf("load tasks: %w", err)
	}

	if len(tasks) == 0 {
		// Phase 1: Plan
		planned, err := r.strategy.Plan(ctx, r.proposal)
		if err != nil {
			r.proposal.Status = ProposalFailed
			_ = r.loader.SaveProposal(r.proposal)
			return "", fmt.Errorf("plan: %w", err)
		}
		if err := ValidateDAG(planned); err != nil {
			r.proposal.Status = ProposalFailed
			_ = r.loader.SaveProposal(r.proposal)
			return "", fmt.Errorf("invalid dag: %w", err)
		}
		// Seed readiness: root tasks (no deps) → ready; others → pending.
		for i := range planned {
			if len(planned[i].Deps) == 0 {
				planned[i].Status = TaskReady
			} else {
				planned[i].Status = TaskPending
			}
			if err := r.loader.SaveTaskDoc(r.proposal.ID, planned[i].ID, taskDocFor(planned[i])); err != nil {
				return "", err
			}
		}
		tasks = planned
	} else {
		// Resume: any task left "running" by a crash is reset to ready.
		ResetStaleRunning(tasks)
	}

	r.proposal.Status = ProposalActive
	if err := r.loader.SaveProposal(r.proposal); err != nil {
		return "", err
	}
	if err := r.loader.SaveTasks(r.proposal.ID, tasks); err != nil {
		return "", err
	}

	// Phase 2: Execute + Review loop.
	for {
		pendingReview := pendingReviewTasks(tasks)
		if len(pendingReview) > 0 {
			for _, task := range pendingReview {
				r.resumePendingReview(ctx, tasks, task)
			}
			_ = r.loader.SaveTasks(r.proposal.ID, tasks)
			continue
		}

		ready := ComputeReadyTasks(tasks)
		// Also pick up previously-seeded TaskReady entries that aren't pending.
		ready = append(ready, readyNonPending(tasks)...)
		if len(ready) == 0 {
			if AllApproved(tasks) {
				r.proposal.Status = ProposalCompleted
				break
			}
			if HasUnrecoverable(tasks) {
				r.proposal.Status = ProposalFailed
				break
			}
			// No ready tasks but not terminal — shouldn't happen on a valid DAG.
			r.proposal.Status = ProposalFailed
			break
		}

		for _, taskPtr := range ready {
			task := FindTask(tasks, taskPtr.ID)
			if task == nil {
				continue
			}
			if task.Status.IsTerminal() || task.Status == TaskRunning {
				continue
			}

			task.Status = TaskRunning
			_ = r.loader.SaveTask(r.proposal.ID, task)

			result, err := r.strategy.Execute(ctx, r.proposal, task)
			if err != nil {
				task.Status = TaskFailed
				task.ResultSummary = truncate(err.Error(), 500)
				_ = r.loader.SaveTask(r.proposal.ID, task)
				continue
			}
			if err := r.loader.SaveTaskResult(r.proposal.ID, task.ID, result); err != nil {
				task.Status = TaskFailed
				task.ResultSummary = truncate(fmt.Sprintf("save task result: %v", err), 500)
				_ = r.loader.SaveTask(r.proposal.ID, task)
				continue
			}

			task.Status = TaskPendingReview
			task.ResultSummary = truncate(result, 500)
			_ = r.loader.SaveTask(r.proposal.ID, task)

			decision, err := r.strategy.Review(ctx, r.proposal, task, result)
			if err != nil {
				task.Status = TaskFailed
				_ = r.loader.SaveTask(r.proposal.ID, task)
				continue
			}
			r.applyReviewDecision(tasks, task, decision)
			_ = r.loader.SaveTask(r.proposal.ID, task)
		}

		_ = r.loader.SaveTasks(r.proposal.ID, tasks)
	}

	_ = r.loader.SaveProposal(r.proposal)
	return r.buildSummary(tasks), nil
}

// readyNonPending returns tasks already in TaskReady state (those seeded at
// plan time but not yet executed). ComputeReadyTasks only returns pending
// tasks; we union in the ready ones here.
func readyNonPending(tasks []Task) []*Task {
	var out []*Task
	for i := range tasks {
		if tasks[i].Status == TaskReady {
			out = append(out, &tasks[i])
		}
	}
	return out
}

func pendingReviewTasks(tasks []Task) []*Task {
	var out []*Task
	for i := range tasks {
		if tasks[i].Status == TaskPendingReview {
			out = append(out, &tasks[i])
		}
	}
	return out
}

func (r *Runner) resumePendingReview(ctx context.Context, tasks []Task, task *Task) {
	result, err := r.loader.LoadTaskResult(r.proposal.ID, task.ID)
	if err != nil {
		task.Status = TaskFailed
		task.ResultSummary = truncate(fmt.Sprintf("resume review: %v", err), 500)
		_ = r.loader.SaveTask(r.proposal.ID, task)
		return
	}
	decision, err := r.strategy.Review(ctx, r.proposal, task, result)
	if err != nil {
		task.Status = TaskFailed
		task.ResultSummary = truncate(fmt.Sprintf("resume review: %v", err), 500)
		_ = r.loader.SaveTask(r.proposal.ID, task)
		return
	}
	r.applyReviewDecision(tasks, task, decision)
	_ = r.loader.SaveTask(r.proposal.ID, task)
}

func (r *Runner) applyReviewDecision(tasks []Task, task *Task, decision Decision) {
	switch decision.Status {
	case "approved":
		task.Status = TaskApproved
		for _, id := range RecalculateAfterApproval(tasks, task.ID) {
			if nt := FindTask(tasks, id); nt != nil && nt.Status == TaskPending {
				nt.Status = TaskReady
			}
		}
	case "rejected":
		task.RetryCount++
		if task.RetryCount >= MaxRetries {
			task.Status = TaskFailed
		} else {
			task.Status = TaskReady
		}
	case "failed":
		task.Status = TaskFailed
	}
}

// taskDocFor writes the initial task brief from the planned Task struct.
// Subsequent runs do not overwrite this — the planner may emit richer briefs.
func taskDocFor(t Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s %s\n\n", t.ID, t.Title)
	if len(t.Deps) > 0 {
		fmt.Fprintf(&b, "Depends on: %s\n\n", strings.Join(t.Deps, ", "))
	}
	if t.Assignee != "" {
		fmt.Fprintf(&b, "Assignee: %s\n\n", t.Assignee)
	}
	fmt.Fprintf(&b, "## Acceptance criteria\n\n- [ ] Implementation matches the title intent\n- [ ] No regressions in dependent areas\n")
	return b.String()
}

func (r *Runner) buildSummary(tasks []Task) string {
	approved := 0
	failed := 0
	other := 0
	for _, t := range tasks {
		switch t.Status {
		case TaskApproved:
			approved++
		case TaskFailed, TaskCancelled:
			failed++
		default:
			other++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Proposal %s — %s\n", r.proposal.ID, r.proposal.Status)
	fmt.Fprintf(&b, "Tasks: %d approved, %d failed, %d other\n\n", approved, failed, other)
	for _, t := range tasks {
		marker := "✓"
		switch t.Status {
		case TaskFailed, TaskCancelled:
			marker = "✗"
		case TaskApproved:
			marker = "✓"
		default:
			marker = "·"
		}
		fmt.Fprintf(&b, "  %s %s %s (%s)\n", marker, t.ID, t.Title, t.Status)
		if t.ResultSummary != "" {
			fmt.Fprintf(&b, "      %s\n", t.ResultSummary)
		}
	}
	if r.proposal.Status == ProposalFailed && failed > 0 {
		fmt.Fprintf(&b, "\nProposal failed: %d task(s) failed.\n", failed)
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	// Don't import os here; just match on the typical prefix.
	return strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "does not exist")
}

// touchUpdate bumps the proposal's UpdatedAt timestamp without changing status.
// Useful for callers that mutate auxiliary state (e.g. comments) between phases.
func (r *Runner) touchUpdate() {
	r.proposal.UpdatedAt = time.Now().UTC()
	_ = r.loader.SaveProposal(r.proposal)
}
