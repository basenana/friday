package memory

import (
	"time"

	"github.com/basenana/friday/core/types"
)

type SessionHistory struct {
	ID           string
	CreatedAt    time.Time
	Messages     []types.Message
	MessageCount int
}

type ExtractionResult struct {
	SessionID       string             `json:"session_id"`
	DailyEntries    []MemoryEntry      `json:"daily_entries"`
	LongTermEntries []MemoryEntry      `json:"long_term_entries"`
	UserPreferences []PreferenceUpdate `json:"user_preferences"`
}

type MemoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
}

type PreferenceUpdate struct {
	Field   string `json:"field"`
	Value   string `json:"value"`
	Context string `json:"context"`
}
