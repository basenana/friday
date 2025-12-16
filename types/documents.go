package types

import "time"

type Memory struct {
	Overview string            `json:"overview"`
	Details  string            `json:"details"`  // Cause, Process, and Result
	Relevant string            `json:"relevant"` // Related people or things
	Comment  string            `json:"comment"`  // Subjective evaluation or remarks
	Metadata map[string]string `json:"metadata"`
	Time     time.Time         `json:"time"`
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
	TypeReview   string = "review"   // learned from final report/result
	TypeMemory   string = "memory"   // learned from conversation

	MetadataChunkDocument string = "friday.chunk_document"
	MetadataChunkIndex    string = "friday.chunk_index"
)
