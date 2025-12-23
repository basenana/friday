package types

import (
	"fmt"

	"github.com/google/uuid"
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
	Type     SessionType       `json:"type"`
	Metadata map[string]string `json:"metadata"`
	System   string            `json:"system"`             // system prompt
	Purpose  string            `json:"purpose"`            // something for display
	Summary  string            `json:"summary"`            // summary for quick restart
	Report   string            `json:"report,omitempty"`   // for Agentic final report
	Feedback string            `json:"feedback,omitempty"` // user feedback on the report
}

func NewDummySession() *Session {
	return &Session{
		ID:       fmt.Sprintf("dummy-%s", uuid.New()),
		Type:     SessionTypeChat,
		Metadata: map[string]string{},
	}
}

type SessionPayload struct {
	ContextID string
	History   []Message
}
