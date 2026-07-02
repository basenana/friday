package proposals

import "time"

// ProposalStatus is the lifecycle state of a proposal.
type ProposalStatus string

const (
	ProposalDraft     ProposalStatus = "draft"
	ProposalActive    ProposalStatus = "active"
	ProposalCompleted ProposalStatus = "completed"
	ProposalFailed    ProposalStatus = "failed"
	ProposalCancelled ProposalStatus = "cancelled"
)

// TaskStatus is the per-task lifecycle state inside a proposal DAG.
type TaskStatus string

const (
	TaskPending       TaskStatus = "pending"        // deps not yet satisfied
	TaskReady         TaskStatus = "ready"          // deps approved, waiting for execution
	TaskRunning       TaskStatus = "running"
	TaskPendingReview TaskStatus = "pending_review" // Execute returned, awaiting Review
	TaskApproved      TaskStatus = "approved"
	TaskFailed        TaskStatus = "failed"
	TaskCancelled     TaskStatus = "cancelled"
)

// IsTerminal reports whether a task status is terminal (no further transitions).
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskApproved, TaskFailed, TaskCancelled:
		return true
	}
	return false
}

// Proposal is the on-disk proposal metadata + status + session map.
type Proposal struct {
	ID         string                  `json:"id"`
	Title      string                  `json:"title"`
	Status     ProposalStatus          `json:"status"`
	OwningTeam string                  `json:"owning_team,omitempty"`
	Sessions   map[string]string       `json:"sessions,omitempty"` // assignee → session ID
	CreatedAt  time.Time               `json:"created_at"`
	UpdatedAt  time.Time               `json:"updated_at"`
}

// Task is one node in the proposal DAG.
type Task struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Status        TaskStatus `json:"status"`
	Assignee      string    `json:"assignee,omitempty"`
	Deps          []string  `json:"deps,omitempty"`
	ResultSummary string    `json:"result_summary,omitempty"`
	RetryCount    int       `json:"retry_count,omitempty"`
}

// Decision is the output of a Review call.
type Decision struct {
	Status  string `json:"status"` // "approved" | "rejected" | "failed"
	Comment string `json:"comment"`
}
