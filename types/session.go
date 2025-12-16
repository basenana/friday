package types

type SessionType string

const (
	SessionTypeChat    SessionType = "Chat"
	SessionTypeAgentic SessionType = "Agentic"
)

type Session struct {
	ID       string            `json:"id"`
	Type     SessionType       `json:"type"`
	Metadata map[string]string `json:"metadata"`
	System   string            `json:"system"`           // system prompt
	Purpose  string            `json:"purpose"`          // something for display
	Summary  string            `json:"summary"`          // summary for quick restart
	Report   string            `json:"report,omitempty"` // for Agentic final report
}
