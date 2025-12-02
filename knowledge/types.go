package knowledge

import "context"

type Base interface {
	Get(ctx context.Context, documentID string) (*Document, error)
	Insert(ctx context.Context, doc *Document) error
	Update(ctx context.Context, doc *Document) error
	Delete(ctx context.Context, documentID string) error

	Query(ctx context.Context, documentType Type, query string) ([]Artifact, error)
}

type Artifact interface {
	isArtifact()
}

type Text struct {
	DocumentID  string `json:"document_id"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

func NewText(documentID, text string) *Text {
	return &Text{DocumentID: documentID, ContentType: "text/plain", Content: text}
}

type Type string

const (
	TypeAll Type = ""

	TypeDocument   Type = "document"   // deadly right things
	TypeReflection Type = "reflection" // learned from final report/result
	TypeRethink    Type = "rethink"    // learned from conversation
)

type Document struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Type        Type     `json:"type"`
	ContentType string   `json:"content_type"` // text/markdown text/html text/plain
	Content     string   `json:"content"`
	Keywords    []string `json:"keywords"`
}
