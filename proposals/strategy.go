package proposals

import "context"

// ExecutionStrategy defines how a proposal is planned, executed, and reviewed.
// Implementations decide who runs each phase (LLM agent, human, etc.) — the
// ProposalRunner is a pure DAG scheduler that only depends on this interface.
type ExecutionStrategy interface {
	// Plan reads the proposal and produces a task DAG.
	Plan(ctx context.Context, proposal *Proposal) ([]Task, error)

	// Execute runs a single task. Returns the execution result text.
	Execute(ctx context.Context, proposal *Proposal, task *Task) (string, error)

	// Review judges a task's execution result. Returns approve/reject/fail.
	Review(ctx context.Context, proposal *Proposal, task *Task, result string) (Decision, error)
}
