package types

import "time"

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

type Document struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata"`

	Title    string `json:"title"`
	MIMEType string `json:"mimetype"`
	Content  string `json:"content"`
}

type Chunk struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Metadata map[string]string `json:"metadata"`
	Content  string            `json:"content"`

	Vector []float64 `json:"-"`
}

const (
	TypeAll      string = ""
	TypeDocument string = "document" // deadly right things
	TypeMemory   string = "memory"   // learned from conversation

	MetadataDocument       string = "friday.document"
	MetadataChunkDocument  string = "friday.chunk_document"
	MetadataChunkIndex     string = "friday.chunk_index"
	MetadataMemory         string = "friday.memory"
	MetadataMemoryType     string = "friday.memory_type"
	MetadataMemoryCategory string = "friday.memory_category"
)
