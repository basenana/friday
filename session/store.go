package session

import (
	"time"

	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

// SessionMeta represents metadata for a session
type SessionMeta struct {
	ID           string    `json:"id"`
	Alias        string    `json:"alias,omitempty"`
	Archived     bool      `json:"archived"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Summary      string    `json:"summary,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
}

// Store defines the interface for session storage operations
type Store interface {
	// Initialization
	EnsureDir() error

	// Session lifecycle
	Create(sessionID string, opts ...coresession.Option) (*coresession.Session, error)
	Load(sessionID string, opts ...coresession.Option) (*coresession.Session, error)
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
