package planning

import "time"

type ArtifactStatus string

const (
	ArtifactProposed   ArtifactStatus = "proposed"
	ArtifactAccepted   ArtifactStatus = "accepted"
	ArtifactSuperseded ArtifactStatus = "superseded"
)

// Artifact is a versioned implementation plan. Its content remains stable;
// only lifecycle fields change when it is accepted or superseded.
type Artifact struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	Version    int            `json:"version"`
	Title      string         `json:"title"`
	Markdown   string         `json:"markdown"`
	Status     ArtifactStatus `json:"status"`
	CreatedAt  time.Time      `json:"created_at"`
	AcceptedAt *time.Time     `json:"accepted_at,omitempty"`
}

// Repository is implemented by the embedding session store.
type Repository interface {
	// ProposePlan commits a new latest version. Implementations assign Version
	// while serializing concurrent proposals for the same session.
	ProposePlan(sessionID string, plan Artifact) (*Artifact, error)
	SavePlan(sessionID string, plan Artifact) error
	LoadPlan(sessionID, planID string) (*Artifact, error)
	LoadLatestPlan(sessionID string) (*Artifact, error)
}
