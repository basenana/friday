package teams

import "time"

// MemberRole identifies a member's place in the team hierarchy.
type MemberRole string

const (
	RoleLeader MemberRole = "leader"
	RoleMember MemberRole = "member"
)

// MemberRef is the roster entry inside team.json. File references keep team.json small.
type MemberRef struct {
	Name string `json:"name"`
	Role MemberRole `json:"role"`
	Model string `json:"model,omitempty"`
}

// Team is the on-disk team definition.
type Team struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Members     []MemberRef `json:"members"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Member is a fully loaded member (frontmatter + instructions).
type Member struct {
	Name         string      `json:"name"`
	Role         MemberRole  `json:"role"`
	Model        string      `json:"model,omitempty"`
	Skills       []string    `json:"skills,omitempty"`
	ToolsAllow   []string    `json:"tools_allow,omitempty"`
	Instructions string      `json:"instructions,omitempty"`
}

// IsLeader reports whether this member holds the leader role.
func (m *Member) IsLeader() bool { return m.Role == RoleLeader }

// Anchor points a comment at an optional (proposal, task) coordinate.
type Anchor struct {
	ProposalID string `json:"proposal_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
}

// CommentKind categorizes the intent of a comment.
type CommentKind string

const (
	CommentKindReview    CommentKind = "review"
	CommentKindNote      CommentKind = "note"
	CommentKindQuestion  CommentKind = "question"
	CommentKindProgress  CommentKind = "progress"
)

// Comment is one entry in a team's append-only comments.jsonl log.
type Comment struct {
	TS    time.Time   `json:"ts"`
	From  string      `json:"from"`
	To    []string    `json:"to,omitempty"`
	Text  string      `json:"text"`
	Anchor Anchor      `json:"anchor,omitempty"`
	Kind  CommentKind `json:"kind,omitempty"`
}
