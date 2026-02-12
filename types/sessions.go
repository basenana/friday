package types

import (
	"time"

	coretypes "github.com/basenana/friday/core/types"
)

type SessionType string

const (
	SessionTypeChat    SessionType = "Chat"
	SessionTypeAgentic SessionType = "Agentic"

	MetadataSessionState       = "friday.state"
	MetadataSessionStateOpen   = "open"
	MetadataSessionStateClosed = "closed"

	SessionHookBeforeModel  = "before_model"
	SessionHookAfterModel   = "after_model"
	SessionHookBeforeClosed = "before_closed"
)

type Session struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Metadata map[string]string `json:"metadata"`
	System   string            `json:"system"`             // system prompt
	Purpose  string            `json:"purpose"`            // something for display
	Summary  string            `json:"summary"`            // summary for quick restart
	Report   string            `json:"report,omitempty"`   // for Agentic final report
	Feedback string            `json:"feedback,omitempty"` // user feedback on the report

	// Tree structure support
	ParentID   string    `json:"parent_id,omitempty"`   // parent session ID
	ForkedFrom string    `json:"forked_from,omitempty"` // forked from session ID
	CreatedAt  time.Time `json:"created_at"`
}

type Message coretypes.Message
