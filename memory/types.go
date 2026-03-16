package memory

import (
	"time"

	"github.com/basenana/friday/core/types"
)

type Memory struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Category   string            `json:"category"`
	Overview   string            `json:"overview"`
	Details    string            `json:"details"`  // Cause, Process, and Result
	Relevant   string            `json:"relevant"` // Related people or things
	Comment    string            `json:"comment"`  // Subjective evaluation or remarks
	Metadata   map[string]string `json:"metadata"`
	UsageCount int               `json:"usage_count"`
	CreatedAt  time.Time         `json:"created_at"`
	LastUsedAt time.Time         `json:"last_used_at"`
}

type SessionHistory struct {
	ID           string
	CreatedAt    time.Time
	Messages     []types.Message
	MessageCount int
}
