package types

import "time"

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
