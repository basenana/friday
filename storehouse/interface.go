package storehouse

import (
	"context"

	"github.com/basenana/friday/tools"
)

type Sotrehouse interface {
	Save(ctx context.Context, chunks ...*Chunk) ([]*Chunk, error)
	Get(ctx context.Context, id string) (*Chunk, error)
	Delete(ctx context.Context, id string) error
	Filter(ctx context.Context, chunkType string, metadata map[string]string) ([]*Chunk, error)
	SearchTools() []*tools.Tool
}

type Vector interface {
	QueryVector(ctx context.Context, chunkType string, vector []float64, k int) ([]*Chunk, error)
	SemanticQuery(ctx context.Context, chunkType, query string, k int) ([]*Chunk, error)
}

type SearchEngine interface {
	QueryLanguage(ctx context.Context, chunkType string, query string) ([]*Chunk, error)
}
