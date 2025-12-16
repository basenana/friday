package storehouse

import (
	"context"
	"github.com/basenana/friday/types"

	"github.com/basenana/friday/tools"
)

type Storehouse interface {
	FilterSessions(ctx context.Context, filter map[string]string) ([]*types.Session, error)
	OpenSession(ctx context.Context, session *types.Session) (*types.Session, error)
	AppendMessages(ctx context.Context, sessionID string, message ...*types.Message) error
	CloseSession(ctx context.Context, sessionID string) error

	AppendMemories(ctx context.Context, memory ...*types.Memory) error
	FilterMemories(ctx context.Context, filter map[string]string) ([]*types.Memory, error)

	GetDocument(ctx context.Context, docID string) (*types.Document, error)
	CreateDocument(ctx context.Context, document *types.Document) error
	UpdateDocument(ctx context.Context, document *types.Document) error
	DeleteDocument(ctx context.Context, docID string) error

	SaveChunks(ctx context.Context, chunks ...*types.Chunk) ([]*types.Chunk, error)
	GetChunk(ctx context.Context, id string) (*types.Chunk, error)
	DeleteChunk(ctx context.Context, id string) error
	FilterChunks(ctx context.Context, chunkType string, filter map[string]string) ([]*types.Chunk, error)
	SearchTools() []*tools.Tool
}

type Vector interface {
	QueryVector(ctx context.Context, chunkType string, vector []float64, k int) ([]*types.Chunk, error)
	SemanticQuery(ctx context.Context, chunkType, query string, k int) ([]*types.Chunk, error)
}

type SearchEngine interface {
	QueryLanguage(ctx context.Context, chunkType string, query string) ([]*types.Chunk, error)
}
