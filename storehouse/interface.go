package storehouse

import (
	"context"

	"github.com/basenana/friday/types"
)

type Storehouse interface {
	ListSessions(ctx context.Context, filter map[string]string, includesClosed bool) ([]*types.Session, error)
	CreateSession(ctx context.Context, session *types.Session) (*types.Session, error)
	UpdateSession(ctx context.Context, session *types.Session) error
	OpenSession(ctx context.Context, sessionID string) (*types.Session, error)
	AppendMessages(ctx context.Context, sessionID string, message ...*types.Message) error
	ListMessages(ctx context.Context, sessionID string) ([]*types.Message, error)
	CloseSession(ctx context.Context, sessionID string) error

	GetMemory(ctx context.Context, memoryID string) (*types.Memory, error)
	ListMemoryCategories(ctx context.Context, memoryType string) ([]string, error)
	AppendMemories(ctx context.Context, memory ...*types.Memory) error
	FilterMemories(ctx context.Context, filter map[string]string) ([]*types.Memory, error)
	ForgetMemory(ctx context.Context, memoryID string) error
	ForgetMemories(ctx context.Context, shouldForget func(*types.Memory) bool, lastUsedDaysAgo int) error

	ListDocuments(ctx context.Context) ([]*types.Document, error)
	GetDocument(ctx context.Context, docID string) (*types.Document, error)
	CreateDocument(ctx context.Context, document *types.Document) error
	UpdateDocument(ctx context.Context, document *types.Document) error
	DeleteDocument(ctx context.Context, docID string) error

	SaveChunks(ctx context.Context, chunkList ...*types.Chunk) ([]*types.Chunk, error)
	GetChunk(ctx context.Context, id string) (*types.Chunk, error)
	DeleteChunk(ctx context.Context, id string) error
	FilterChunks(ctx context.Context, chunkType string, filter map[string]string) ([]*types.Chunk, error)
}

type Vector interface {
	QueryVector(ctx context.Context, chunkType string, filter map[string]string, vector []float64, k int) ([]*types.Chunk, error)
	SemanticQuery(ctx context.Context, chunkType string, filter map[string]string, query string, k int) ([]*types.Chunk, error)
}

type SearchEngine interface {
	QueryLanguage(ctx context.Context, query string) ([]*types.Document, error)
}
