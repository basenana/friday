package sessions

import (
	"context"
	"time"

	"github.com/basenana/friday/core/actor/events"
	actorsink "github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

// EventStore is an optional capability implemented by stores that persist the
// actor event stream used by rich TUI transcript replay. Store intentionally
// does not embed it so existing/custom session backends remain compatible.
type EventStore interface {
	OpenEventSink(context.Context, string) (actorsink.EventSink, error)
	LoadEvents(context.Context, string) ([]events.Event, error)
}

// MetadataStore is the optional structured metadata capability used by the
// TUI. Existing/custom Store implementations are not required to support it.
type MetadataStore interface {
	UpdateMeta(sessionID string, patch SessionMetaPatch) error
}

// PlanningStore is the optional Plan Mode persistence capability.
type PlanningStore interface {
	planning.Repository
}

// Relation describes a persisted session that is private to a root session's
// lifecycle (for example an associated worker). Related sessions are deliberately
// not part of project or global-current selection.
type Relation struct {
	Version   int       `json:"version"`
	RootID    string    `json:"root_id"`
	Key       string    `json:"key"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const RelationKindAssociated = "associated"

// RelationStore is the optional persistence capability used by a bound
// SessionLifecycle. AcquireRelationLock serializes the read/create/write
// sequence across processes; callers must always invoke the returned unlock.
type RelationStore interface {
	GetRelation(rootID, key string) (*Relation, error)
	PutRelation(Relation) error
	DeleteRelation(rootID, key string) error
	ListRelations(rootID string) ([]Relation, error)
	AcquireRelationLock(rootID, key string) (unlock func(), err error)
}

// SessionMeta represents metadata for a session
type SessionMeta struct {
	ID           string         `json:"id"`
	Alias        string         `json:"alias,omitempty"`
	Name         string         `json:"name,omitempty"`
	Archived     bool           `json:"archived"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	MessageCount int            `json:"message_count"`
	Summary      string         `json:"summary,omitempty"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	Runtime      SessionRuntime `json:"runtime,omitempty"`
	LatestPlanID string         `json:"latest_plan_id,omitempty"`
}

type ModelSelection struct {
	Model string `json:"model,omitempty"`
}

type SessionRuntime struct {
	Mode   collaboration.Mode `json:"mode,omitempty"`
	Model  ModelSelection     `json:"model,omitempty"`
	Effort string             `json:"effort,omitempty"`
}

type SessionMetaPatch struct {
	Name         *string
	Archived     *bool
	Runtime      *SessionRuntime
	Mode         *collaboration.Mode
	Model        *ModelSelection
	Effort       *string
	LatestPlanID *string
}

// Store defines the interface for session storage operations
type Store interface {
	// Initialization
	EnsureDir() error

	// Session lifecycle
	Create(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error)
	Load(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error)
	Delete(sessionID string) error

	// Query
	List() ([]SessionMeta, error)
	ListActive() ([]SessionMeta, error)
	GetMeta(sessionID string) (*SessionMeta, error)

	// Update
	UpdateAlias(sessionID, alias string) error
	Archive(sessionID string) error
	Unarchive(sessionID string) error

	// Messages
	LoadMessages(sessionID string) ([]types.Message, error)
}
